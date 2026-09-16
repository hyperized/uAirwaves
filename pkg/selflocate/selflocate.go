// Package selflocate estimates the receiver's geographic
// location from a stream of ADSB position fixes using horizon-
// circle intersection.
//
// Each received aircraft position with a known altitude
// constrains the receiver to lie within the aircraft's radio
// horizon. The receiver itself must therefore live inside the
// intersection of every such constraint circle; with enough
// observations, particularly low-altitude approach traffic, the
// intersection shrinks to a small region centred on the receiver.
//
// The horizon is the 4/3-earth one, computed from both ends of
// the link (aircraft altitude and receiver antenna height) and
// widened by a fixed margin, because a circle that is too tight
// is not a conservative error: it excludes the true receiver
// position and the search converges on the aircraft instead.
// Observations below minObservationAltitudeFt are dropped for the
// same reason. Where the remaining circles still disagree, Fix
// reports how many of them exclude the estimate and widens the
// confidence radius to the region that would win without them.
//
// The package is GPS-free and internet-free: it consumes the
// same ADSB stream uAirwaves already decodes for the scope view.
// Use it as a fallback when gpsd is unavailable (e.g. cold-start
// on the uConsole before satellite lock).
//
// Locator is the only public type. Construction takes functional
// options; the algorithm is passive. The caller drives Observe
// from the ADSB ingest path and polls Estimate at whatever
// cadence makes sense for the surrounding application.
package selflocate

import (
	"math"
	"sync"
	"time"
)

const (
	// horizonCoefficient maps altitude (ft) to radio horizon (nm)
	// under the 4/3-earth model: d_nm = 1.23 * sqrt(h_ft). The
	// constant already incorporates the atmospheric refraction
	// bonus over the geometric horizon, which is the convention
	// every ADSB / aviation reference uses.
	horizonCoefficient = 1.23

	// defaultAntennaHeightFt is the assumed height of the receiver
	// antenna above ground. The horizon is the sum of both ends of
	// the link, d_nm = 1.23 * (sqrt(h_aircraft) + sqrt(h_antenna)),
	// so leaving the antenna out understates every circle; 30 ft of
	// antenna is worth 6.7 nm on its own. 30 ft spans what this
	// receiver realistically sits on, a whip on a windowsill up to
	// a short mast on a house, and erring high is the safe
	// direction: a circle that is slightly too wide costs a little
	// confidence, one that is too tight costs correctness.
	defaultAntennaHeightFt = 30

	// horizonMargin scales every horizon after the formula.
	// Reception regularly beats the nominal 4/3-earth horizon by a
	// few percent through ducting and because the aircraft's
	// antenna sits above the barometric reference its altitude
	// reports. 15 % of slack keeps an aircraft the receiver
	// genuinely heard from excluding the receiver's true position.
	horizonMargin = 1.15

	// minObservationAltitudeFt drops surface and rolling traffic.
	// An aircraft on a runway reports a near-zero barometric
	// altitude, giving it a horizon of a few nm, yet receivers
	// routinely hear it from ten times that distance. Those are the
	// tightest circles in the set, so initialSearchRegion anchors
	// on them, and one of them is enough to drag the whole estimate
	// onto the airport.
	minObservationAltitudeFt = 300

	// nauticalMilePerDegree is the great-circle distance for one
	// degree of latitude. The longitude direction needs an
	// additional cos(latitude) scaling that the distance function
	// applies inline.
	nauticalMilePerDegree = 60.0

	// deg2Rad converts degrees to radians.
	deg2Rad = math.Pi / 180

	// minCosLat floors the longitude-scaling factor so positions
	// near the poles don't blow up the grid spacing. ADSB receivers
	// can in principle operate anywhere, but at cos(lat)=0.1 the
	// approximation is already wildly off; the floor stops a
	// numerical blow-up while leaving the result obviously coarse.
	minCosLat = 0.1

	// defaultMaxObservations bounds the ring buffer. 500
	// observations is ~25 minutes of moderately busy traffic; older
	// fixes contribute little new information once the intersection
	// has tightened. The bound also caps grid-search compute time.
	defaultMaxObservations = 500

	// defaultMinObservations is the lower bound below which Estimate
	// declines to return a fix. Fewer than ~30 observations leave
	// the intersection too loose to materially help downstream
	// consumers (e.g. CPR locally-unambiguous decoding needs the
	// receiver position within ~90 nm).
	defaultMinObservations = 30

	// defaultMaxAltitudeForReady is the upper altitude (ft) for the
	// "have we seen at least one low plane" gate. Without a low-
	// altitude observation every horizon circle is 200+ nm wide,
	// the intersection is broad, and the resulting estimate is
	// useless. A 20 000 ft ceiling catches departure / arrival
	// traffic at every Class C airport.
	defaultMaxAltitudeForReady = 20000

	// gridSearchLevels is how many recursive refinements the
	// search runs. Each level shrinks the search radius by
	// gridSearchShrinkFactor, so 4 levels takes a ±~200 nm initial
	// region down to ±~0.02 nm — comfortably finer than the target
	// 30 nm accuracy of self-locate.
	gridSearchLevels = 4

	// gridSearchHalfSize is half the linear count of grid points
	// per level (the full grid is 2*gridSearchHalfSize+1 wide). 5
	// gives an 11x11 grid = 121 candidate positions per level —
	// fast even with 500 observations (121 * 500 = 60.5k distance
	// calculations per level, 242k per Estimate over 4 levels).
	gridSearchHalfSize = 5

	// gridSearchShrinkFactor is how much the search half-width
	// shrinks between levels. A factor of 10 means each level
	// zooms in by an order of magnitude.
	gridSearchShrinkFactor = 10

	// confidenceGridHalfSize is half the linear count of grid
	// points in the confidence scan (33x33 = 1089 candidates). The
	// scan spans the whole initial search region, not the refined
	// box the estimate came from, because the region that satisfies
	// the constraints is routinely tens of nm wide while the final
	// grid spans a fraction of a nm. Resolution is deliberately
	// coarse: the result is an upper bound on the error, not a
	// precision figure. It costs 1089 * len(obs) distance
	// calculations per Estimate, about two thirds of the 5.4 ms a
	// capped 500-observation Estimate takes on an M2 Pro
	// (BenchmarkEstimate). The uConsole is slower by roughly an
	// order of magnitude and calls Estimate every 15 s.
	confidenceGridHalfSize = 16
)

// observation is a single buffered ADSB position fix plus its
// derived radio horizon. Stored once at Observe time so the
// grid search doesn't recompute the horizon per candidate point.
type observation struct {
	lat, lon, horizonNm float64
	receivedAt          time.Time
}

// Fix is the estimated receiver position with confidence
// metadata. ConfidenceRadiusNm is the half-diagonal of the
// plausible region's bounding box, floored at one grid cell of
// the search's finest level: a loose upper bound on how far the
// estimate could be from the true receiver position, never a
// claim of precision the grid cannot support.
//
// Violated counts the observations whose horizon circle does not
// contain the estimate. Zero means every circle agrees. Anything
// higher means the constraints are mutually inconsistent, the
// estimate is a compromise, and ConfidenceRadiusNm has been
// widened to the region that would win if those circles were
// dropped. Callers that display a position should surface the
// doubt rather than hide it.
type Fix struct {
	Latitude           float64
	Longitude          float64
	ConfidenceRadiusNm float64
	ObservationCount   int
	Violated           int
}

// Locator buffers observed aircraft positions and produces a
// receiver-location estimate on demand. Zero value is not
// usable; construct via New. Thread-safe.
//
// Only observations is mutex-guarded. The configuration fields
// are written once in New and read without the lock afterwards,
// which is why Option applies at construction and nowhere else.
type Locator struct {
	mu sync.RWMutex

	maxObservations     int
	minObservations     int
	maxAltitudeForReady float64
	antennaHeightFt     float64

	observations []observation
}

// Option configures a Locator at construction time.
type Option func(*Locator)

// WithMaxObservations sets the ring-buffer cap. Older fixes are
// dropped once the buffer exceeds this size. n must be > 0;
// nonsense values are ignored so callers can safely pass
// user-supplied integers without pre-validating them.
func WithMaxObservations(n int) Option {
	return func(l *Locator) {
		if n > 0 {
			l.maxObservations = n
		}
	}
}

// WithMinObservations sets the minimum observation count before
// Estimate returns ok=true. n must be > 0; nonsense values are
// ignored.
func WithMinObservations(n int) Option {
	return func(l *Locator) {
		if n > 0 {
			l.minObservations = n
		}
	}
}

// WithMaxAltitudeForReady sets the upper altitude (ft) for the
// readiness gate. At least one observation must be at or below
// this altitude before Estimate will return a fix; otherwise
// the intersection of high-altitude horizons is too broad to
// be useful. altFt must be > 0; nonsense values are ignored.
func WithMaxAltitudeForReady(altFt float64) Option {
	return func(l *Locator) {
		if altFt > 0 {
			l.maxAltitudeForReady = altFt
		}
	}
}

// WithAntennaHeightFt sets the receiver antenna height above
// ground in feet, the second term of the horizon formula. Raise
// it for a rooftop or mast install; the default assumes
// defaultAntennaHeightFt. Zero is accepted and means a receiver
// at ground level; negative values are ignored.
func WithAntennaHeightFt(ft float64) Option {
	return func(l *Locator) {
		if ft >= 0 {
			l.antennaHeightFt = ft
		}
	}
}

// New constructs a Locator with sensible defaults. Options
// override defaults; unknown or invalid options have no effect.
func New(opts ...Option) *Locator {
	loc := &Locator{
		maxObservations:     defaultMaxObservations,
		minObservations:     defaultMinObservations,
		maxAltitudeForReady: defaultMaxAltitudeForReady,
		antennaHeightFt:     defaultAntennaHeightFt,
	}

	for _, opt := range opts {
		opt(loc)
	}

	return loc
}

// Observe records an aircraft position fix. lat/lon must be a
// real (non-zero) position, altFt must be > 0, and altFt must be
// at or above minObservationAltitudeFt. Observations failing
// these gates are silently dropped so callers can pass raw
// CPR-decoded values without pre-validation.
func (l *Locator) Observe(lat, lon, altFt float64) {
	if lat == 0 && lon == 0 {
		return
	}

	if altFt <= 0 {
		return
	}

	if altFt < minObservationAltitudeFt {
		return
	}

	obs := observation{
		lat:        lat,
		lon:        lon,
		horizonNm:  horizonNmFor(altFt, l.antennaHeightFt),
		receivedAt: time.Now(),
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.observations = append(l.observations, obs)

	if len(l.observations) > l.maxObservations {
		l.observations = l.observations[len(l.observations)-l.maxObservations:]
	}
}

// ObservationCount returns the number of buffered observations.
// Provided for status displays that want to surface the
// pre-Estimate progress without paying the grid-search cost.
func (l *Locator) ObservationCount() int {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return len(l.observations)
}

// Estimate computes the most likely receiver position given the
// current observations. Returns (Fix{}, false) when the readiness
// gates aren't met — too few observations, or none low enough to
// tighten the intersection.
func (l *Locator) Estimate() (Fix, bool) {
	obs := l.snapshotObservations()

	if len(obs) < l.minObservations {
		return Fix{}, false
	}

	if !hasTightHorizon(obs, horizonNmFor(l.maxAltitudeForReady, l.antennaHeightFt)) {
		return Fix{}, false
	}

	return solve(obs), true
}

// snapshotObservations returns a stable value copy under a
// short RLock so the rest of the solve path runs lock-free.
func (l *Locator) snapshotObservations() []observation {
	l.mu.RLock()
	defer l.mu.RUnlock()

	out := make([]observation, len(l.observations))
	copy(out, l.observations)

	return out
}

// horizonNmFor returns the radio horizon in nautical miles for an
// aircraft at altFt seen by an antenna antennaFt above ground:
//
//	d_nm = horizonMargin * 1.23 * (sqrt(alt_ft) + sqrt(antenna_ft))
//
// Both ends of the link contribute, which the single-term form
// d = 1.23 * sqrt(alt) silently omits. See the horizonMargin and
// defaultAntennaHeightFt comments for why the result is then
// deliberately padded.
func horizonNmFor(altFt, antennaFt float64) float64 {
	return horizonMargin * horizonCoefficient * (math.Sqrt(altFt) + math.Sqrt(antennaFt))
}

// hasTightHorizon reports whether the observation set includes at
// least one fix whose horizon is small enough to materially
// shrink the intersection. The ceiling arrives as a horizon
// rather than an altitude so the per-observation check is a
// single float comparison, and so the ceiling passes through the
// same antenna-height and margin terms the observations did.
func hasTightHorizon(obs []observation, horizonCeilingNm float64) bool {
	for _, fix := range obs {
		if fix.horizonNm <= horizonCeilingNm {
			return true
		}
	}

	return false
}

// solve runs the recursive grid search and packages the result
// into a Fix. Caller must have validated readiness.
func solve(obs []observation) Fix {
	bestLat, bestLon, halfWidth := initialSearchRegion(obs)
	searchHalfWidth := halfWidth

	var finestStep float64

	for range gridSearchLevels {
		bestLat, bestLon = gridSearch(obs, bestLat, bestLon, halfWidth)
		finestStep = halfWidth / float64(gridSearchHalfSize)
		halfWidth /= gridSearchShrinkFactor
	}

	violated := len(obs) - countCirclesContaining(obs, bestLat, bestLon)
	confRadius := plausibleSpreadNm(obs, bestLat, bestLon, searchHalfWidth, violated)

	return Fix{
		Latitude:  bestLat,
		Longitude: bestLon,
		// finestStep is the floor the Fix contract promises. The
		// scan's own floor is a coarse half-cell and therefore
		// larger; this keeps the promise true if that ever changes.
		ConfidenceRadiusNm: math.Max(confRadius, finestStep),
		ObservationCount:   len(obs),
		Violated:           violated,
	}
}

// initialSearchRegion returns the centre and half-width of the
// first-level search box. We anchor on the observation with the
// smallest horizon (the tightest single constraint) and size
// the box to that horizon — the receiver is guaranteed to lie
// inside, by definition of "we received this plane".
func initialSearchRegion(obs []observation) (float64, float64, float64) {
	smallest := obs[0]

	for _, fix := range obs[1:] {
		if fix.horizonNm < smallest.horizonNm {
			smallest = fix
		}
	}

	return smallest.lat, smallest.lon, smallest.horizonNm
}

// gridSearch evaluates an (2*gridSearchHalfSize+1)^2 grid around
// (centerLat, centerLon) at ±halfWidth nm. For each candidate
// point it counts how many horizon circles contain it, and
// returns the centroid of the points sharing the maximum count.
// When no candidate is inside any circle the input centre comes
// back unchanged, leaving the next level to try a tighter box.
//
// It reports position only. The spread of the tied points is not
// a usable confidence radius: it shrinks with every level of
// zoom whether or not the underlying region does, which is how
// an estimate 17 nm from the receiver came to be published with a
// confidence of 0.01 nm. plausibleSpreadNm measures that instead.
func gridSearch(obs []observation, centerLat, centerLon, halfWidth float64) (float64, float64) {
	step := halfWidth / float64(gridSearchHalfSize)
	cosLat := cosLatFloor(centerLat)

	var bestCount, tied int

	var sumLat, sumLon float64

	for i := -gridSearchHalfSize; i <= gridSearchHalfSize; i++ {
		for j := -gridSearchHalfSize; j <= gridSearchHalfSize; j++ {
			candidateLat := centerLat + float64(i)*step/nauticalMilePerDegree
			candidateLon := centerLon + float64(j)*step/(nauticalMilePerDegree*cosLat)

			count := countCirclesContaining(obs, candidateLat, candidateLon)

			switch {
			case count > bestCount:
				bestCount = count
				sumLat, sumLon = candidateLat, candidateLon
				tied = 1
			case count == bestCount && bestCount > 0:
				sumLat += candidateLat
				sumLon += candidateLon
				tied++
			default:
				// Candidate is inside fewer circles than the best
				// so far — ignored. Listed explicitly to satisfy
				// revive's enforce-switch-style and to make the
				// three branches read symmetrically.
			}
		}
	}

	if tied == 0 {
		return centerLat, centerLon
	}

	return sumLat / float64(tied), sumLon / float64(tied)
}

// plausibleSpreadNm returns the half-diagonal, in nautical miles,
// of the region the receiver could occupy: every candidate within
// ±halfWidth of the estimate that sits inside enough horizon
// circles to beat the estimate's own agreement.
//
// With violated == 0 the threshold is every circle, so the scan
// bounds the true intersection. With circles that disagree the
// estimate is a compromise no consistent position occupies, and
// the threshold drops to (contained - violated): the region that
// would win if the offending circles were dropped. The clamp at
// one keeps a set with more dissent than agreement from bounding
// candidates that satisfy nothing at all.
//
// The measurement is an upper bound, and a truncated one where
// the region runs past the scan box. That is the safe direction:
// the alternative, a radius measured inside the search's final
// zoom, reports a fraction of a nautical mile regardless of how
// wrong the estimate is.
func plausibleSpreadNm(obs []observation, centerLat, centerLon, halfWidth float64, violated int) float64 {
	// The estimate is inside (len(obs) - violated) circles, so the
	// threshold described above works out as len(obs) - 2*violated.
	minCount := max(len(obs)-2*violated, 1)
	step := halfWidth / float64(confidenceGridHalfSize)
	cosLat := cosLatFloor(centerLat)

	minLat, maxLat := centerLat, centerLat
	minLon, maxLon := centerLon, centerLon

	for i := -confidenceGridHalfSize; i <= confidenceGridHalfSize; i++ {
		for j := -confidenceGridHalfSize; j <= confidenceGridHalfSize; j++ {
			candidateLat := centerLat + float64(i)*step/nauticalMilePerDegree
			candidateLon := centerLon + float64(j)*step/(nauticalMilePerDegree*cosLat)

			if countCirclesContaining(obs, candidateLat, candidateLon) < minCount {
				continue
			}

			minLat = math.Min(minLat, candidateLat)
			maxLat = math.Max(maxLat, candidateLat)
			minLon = math.Min(minLon, candidateLon)
			maxLon = math.Max(maxLon, candidateLon)
		}
	}

	return boundingHalfDiagonalNm(minLat, maxLat, minLon, maxLon, cosLat, step)
}

// boundingHalfDiagonalNm converts a lat/lon bounding box into a
// half-diagonal in nautical miles. Floored at step/2 so a box
// that collapsed onto a single grid point still reports a
// non-zero radius: the region it stands for is at least a cell
// across, or the scan would have qualified more than one point.
func boundingHalfDiagonalNm(minLat, maxLat, minLon, maxLon, cosLat, step float64) float64 {
	dLatNm := (maxLat - minLat) * nauticalMilePerDegree
	dLonNm := (maxLon - minLon) * nauticalMilePerDegree * cosLat
	halfDiag := math.Sqrt(dLatNm*dLatNm+dLonNm*dLonNm) / 2

	if halfDiag < step/2 {
		return step / 2
	}

	return halfDiag
}

// countCirclesContaining returns the number of observation
// horizon-circles that contain the point (lat, lon).
func countCirclesContaining(obs []observation, lat, lon float64) int {
	count := 0

	for _, fix := range obs {
		if distanceNm(lat, lon, fix.lat, fix.lon) <= fix.horizonNm {
			count++
		}
	}

	return count
}

// distanceNm returns the great-circle distance between two
// points in nautical miles using the flat-Earth approximation.
// Accurate to ~1 % over the receiver's typical operating range
// (< 250 nm); chosen over Haversine for raw speed in the grid
// search's inner loop where this function dominates.
func distanceNm(lat1, lon1, lat2, lon2 float64) float64 {
	dLat := lat1 - lat2
	midLat := (lat1 + lat2) / 2
	dLon := (lon1 - lon2) * math.Cos(midLat*deg2Rad)

	return math.Sqrt(dLat*dLat+dLon*dLon) * nauticalMilePerDegree
}

// cosLatFloor returns cos(lat) floored at minCosLat to keep the
// longitude scaling sane near the poles.
func cosLatFloor(lat float64) float64 {
	cosLat := math.Cos(lat * deg2Rad)
	if cosLat < minCosLat {
		return minCosLat
	}

	return cosLat
}
