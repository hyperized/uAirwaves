// Package selflocate estimates the receiver's geographic
// location from a stream of ADSB position fixes using horizon-
// circle intersection.
//
// Each received aircraft position with a known altitude
// constrains the receiver to lie within the aircraft's radio
// horizon. The receiver itself must therefore live inside the
// intersection of every such constraint circle; with enough
// observations — particularly low-altitude approach traffic —
// the intersection shrinks to a small region centred on the
// receiver.
//
// The package is GPS-free and internet-free: it consumes the
// same ADSB stream uAirwaves already decodes for the scope view.
// Use it as a fallback when gpsd is unavailable (e.g. cold-start
// on the uConsole before satellite lock).
//
// Locator is the only public type. Construction takes functional
// options; the algorithm is passive — the caller drives Observe
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
// "inside every circle" region's bounding box at the finest grid
// level — a loose upper bound on how far the estimate could be
// from the true receiver position.
type Fix struct {
	Latitude           float64
	Longitude          float64
	ConfidenceRadiusNm float64
	ObservationCount   int
}

// Locator buffers observed aircraft positions and produces a
// receiver-location estimate on demand. Zero value is not
// usable; construct via New. Thread-safe.
type Locator struct {
	mu sync.RWMutex

	maxObservations     int
	minObservations     int
	maxAltitudeForReady float64

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

// New constructs a Locator with sensible defaults. Options
// override defaults; unknown or invalid options have no effect.
func New(opts ...Option) *Locator {
	loc := &Locator{
		maxObservations:     defaultMaxObservations,
		minObservations:     defaultMinObservations,
		maxAltitudeForReady: defaultMaxAltitudeForReady,
	}

	for _, opt := range opts {
		opt(loc)
	}

	return loc
}

// Observe records an aircraft position fix. lat/lon must be a
// real (non-zero) position and altFt must be > 0 — observations
// failing these gates are silently dropped so callers can pass
// raw CPR-decoded values without pre-validation.
func (l *Locator) Observe(lat, lon, altFt float64) {
	if lat == 0 && lon == 0 {
		return
	}

	if altFt <= 0 {
		return
	}

	obs := observation{
		lat:        lat,
		lon:        lon,
		horizonNm:  horizonCoefficient * math.Sqrt(altFt),
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

	if !hasLowAltitude(obs, l.maxAltitudeForReady) {
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

// hasLowAltitude reports whether the observation set includes at
// least one fix whose horizon is tight enough to materially
// shrink the intersection. The threshold is expressed as an
// altitude ceiling (ft) and translated to a horizon ceiling so
// the per-observation check is a single float comparison rather
// than a sqrt per call.
func hasLowAltitude(obs []observation, maxAltFt float64) bool {
	horizonCeiling := horizonCoefficient * math.Sqrt(maxAltFt)

	for _, fix := range obs {
		if fix.horizonNm <= horizonCeiling {
			return true
		}
	}

	return false
}

// solve runs the recursive grid search and packages the result
// into a Fix. Caller must have validated readiness.
func solve(obs []observation) Fix {
	centerLat, centerLon, halfWidth := initialSearchRegion(obs)

	bestLat, bestLon := centerLat, centerLon
	confRadius := halfWidth

	for range gridSearchLevels {
		var spread float64

		bestLat, bestLon, spread = gridSearch(obs, bestLat, bestLon, halfWidth)
		confRadius = spread
		halfWidth /= gridSearchShrinkFactor
	}

	return Fix{
		Latitude:           bestLat,
		Longitude:          bestLon,
		ConfidenceRadiusNm: confRadius,
		ObservationCount:   len(obs),
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
// point it counts how many horizon circles contain it; the best
// estimate is the centroid of points sharing the maximum count.
// The third return is the bounding-box half-diagonal of those
// best points — a loose confidence radius.
//
//nolint:nonamedreturns // (lat, lon, spread) reads clearer named at this signature.
func gridSearch(obs []observation, centerLat, centerLon, halfWidth float64) (lat, lon, spread float64) {
	step := halfWidth / float64(gridSearchHalfSize)
	cosLat := cosLatFloor(centerLat)

	var bestCount, tied int

	var sumLat, sumLon float64

	var minLat, maxLat, minLon, maxLon float64

	for i := -gridSearchHalfSize; i <= gridSearchHalfSize; i++ {
		for j := -gridSearchHalfSize; j <= gridSearchHalfSize; j++ {
			candidateLat := centerLat + float64(i)*step/nauticalMilePerDegree
			candidateLon := centerLon + float64(j)*step/(nauticalMilePerDegree*cosLat)

			count := countCirclesContaining(obs, candidateLat, candidateLon)

			switch {
			case count > bestCount:
				bestCount = count
				sumLat, sumLon = candidateLat, candidateLon
				minLat, maxLat = candidateLat, candidateLat
				minLon, maxLon = candidateLon, candidateLon
				tied = 1
			case count == bestCount && bestCount > 0:
				sumLat += candidateLat
				sumLon += candidateLon
				minLat = math.Min(minLat, candidateLat)
				maxLat = math.Max(maxLat, candidateLat)
				minLon = math.Min(minLon, candidateLon)
				maxLon = math.Max(maxLon, candidateLon)
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
		return centerLat, centerLon, halfWidth
	}

	return sumLat / float64(tied), sumLon / float64(tied),
		boundingHalfDiagonalNm(minLat, maxLat, minLon, maxLon, cosLat, step)
}

// boundingHalfDiagonalNm converts the lat/lon bounding box of
// the best-grid-points into a half-diagonal in nautical miles.
// Floored at step/2 so a single-best-point grid still reports
// a non-zero radius — the true intersection region must be at
// least the grid step wide or the search would have found more
// tied points.
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
