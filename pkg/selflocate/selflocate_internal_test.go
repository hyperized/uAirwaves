package selflocate

import (
	"math"
	"testing"
)

// receiverNearSchiphol is the reported receiver position: a
// uConsole roughly 30 km south-west of Schiphol.
const (
	receiverNearSchipholLat = 52.31
	receiverNearSchipholLon = 4.77

	// runwayAircraftLat/Lon is an aircraft rolling on a runway,
	// 17.6 nm from the receiver, which the receiver hears anyway.
	runwayAircraftLat = 52.05
	runwayAircraftLon = 4.55

	// runwayAircraftAltFt is what such an aircraft reports: a
	// barometric altitude barely off the ground.
	runwayAircraftAltFt = 50
)

// TestHorizonCoefficient pins the 1.23 * sqrt(h_ft) formula
// against known-good textbook values. A regression here would
// shift every horizon circle and silently destroy the algorithm.
func TestHorizonCoefficient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		altFt     float64
		wantNm    float64
		tolerance float64
	}{
		{name: "FL350 cruise", altFt: 35000, wantNm: 230.0, tolerance: 1.0},
		{name: "FL100 descent", altFt: 10000, wantNm: 123.0, tolerance: 1.0},
		{name: "1500 ft final", altFt: 1500, wantNm: 48.0, tolerance: 1.0},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := horizonCoefficient * math.Sqrt(testCase.altFt)
			if math.Abs(got-testCase.wantNm) > testCase.tolerance {
				t.Errorf("horizon for %.0f ft = %.1f nm, want %.1f ± %.1f",
					testCase.altFt, got, testCase.wantNm, testCase.tolerance)
			}
		})
	}
}

// TestHorizonNmForAddsAntennaAndMargin pins the two terms the
// single-term formula omitted. Both widen every circle, and a
// circle that is too tight is what excluded the true receiver
// position in the field.
func TestHorizonNmForAddsAntennaAndMargin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		altFt     float64
		antennaFt float64
		wantNm    float64
	}{
		{name: "margin only at ground level", altFt: 10000, antennaFt: 0, wantNm: 141.45},
		{name: "default 30 ft antenna adds 7.7 nm", altFt: 10000, antennaFt: 30, wantNm: 149.20},
		{name: "10 m mast", altFt: 10000, antennaFt: 32.8, wantNm: 149.56},
		{name: "runway traffic clears the reported distance", altFt: 50, antennaFt: 30, wantNm: 17.75},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := horizonNmFor(testCase.altFt, testCase.antennaFt)
			if math.Abs(got-testCase.wantNm) > 0.01 {
				t.Errorf("horizonNmFor(%.1f, %.1f) = %.3f nm, want %.2f",
					testCase.altFt, testCase.antennaFt, got, testCase.wantNm)
			}
		})
	}
}

// TestHorizonNmForMatchesTheDocumentedFormula checks the result
// against the formula spelled out in the doc comment rather than
// against a precomputed number, so a change to either constant
// has to be a deliberate one.
func TestHorizonNmForMatchesTheDocumentedFormula(t *testing.T) {
	t.Parallel()

	const (
		altFt     = 4500.0
		antennaFt = 12.0
	)

	want := horizonMargin * horizonCoefficient * (math.Sqrt(altFt) + math.Sqrt(antennaFt))
	if got := horizonNmFor(altFt, antennaFt); got != want {
		t.Errorf("horizonNmFor(%.0f, %.0f) = %v, want %v", altFt, antennaFt, got, want)
	}
}

// TestObserveStoresAntennaWidenedHorizon covers the Observe-side
// half of the antenna option: the horizon is computed once, at
// Observe time, so the option has to reach it there.
func TestObserveStoresAntennaWidenedHorizon(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		opts      []Option
		antennaFt float64
	}{
		{name: "default antenna", opts: nil, antennaFt: defaultAntennaHeightFt},
		{name: "rooftop mast", opts: []Option{WithAntennaHeightFt(60)}, antennaFt: 60},
		{name: "ground level", opts: []Option{WithAntennaHeightFt(0)}, antennaFt: 0},
		{name: "negative ignored", opts: []Option{WithAntennaHeightFt(-5)}, antennaFt: defaultAntennaHeightFt},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			loc := New(testCase.opts...)
			loc.Observe(52.0, 4.0, 10000)

			want := horizonNmFor(10000, testCase.antennaFt)
			if got := loc.observations[0].horizonNm; got != want {
				t.Errorf("stored horizon = %.3f nm, want %.3f", got, want)
			}
		})
	}
}

// TestDistanceNmFlatEarthSelfDistance verifies the distance
// function returns zero for identical points.
func TestDistanceNmFlatEarthSelfDistance(t *testing.T) {
	t.Parallel()

	if got := distanceNm(52.0, 4.0, 52.0, 4.0); got != 0 {
		t.Errorf("distance from a point to itself = %v, want 0", got)
	}
}

// TestDistanceNmOneDegreeLatitude pins the 60 nm / degree
// constant. One degree of latitude is by definition 60 nm.
func TestDistanceNmOneDegreeLatitude(t *testing.T) {
	t.Parallel()

	got := distanceNm(52.0, 4.0, 53.0, 4.0)
	if math.Abs(got-60.0) > 0.5 {
		t.Errorf("one degree latitude = %.2f nm, want 60.00 ± 0.5", got)
	}
}

// TestDistanceNmOneDegreeLongitudeAtLat60 covers the cos(lat)
// scaling on the longitude axis. At 60° N, one degree of
// longitude is ~30 nm (cos(60°) = 0.5).
func TestDistanceNmOneDegreeLongitudeAtLat60(t *testing.T) {
	t.Parallel()

	got := distanceNm(60.0, 4.0, 60.0, 5.0)
	if math.Abs(got-30.0) > 0.5 {
		t.Errorf("one degree longitude at 60° = %.2f nm, want 30.00 ± 0.5", got)
	}
}

// TestCosLatFloorClampsNearPole pins the minCosLat guard so the
// longitude scaling can't blow up to division-by-zero near the
// poles.
func TestCosLatFloorClampsNearPole(t *testing.T) {
	t.Parallel()

	if got := cosLatFloor(89.999); got != minCosLat {
		t.Errorf("near-pole cosLatFloor = %v, want floor %v", got, minCosLat)
	}

	if got := cosLatFloor(0.0); math.Abs(got-1.0) > 1e-9 {
		t.Errorf("equatorial cosLatFloor = %v, want 1.0", got)
	}
}

// TestHasTightHorizonRejectsAllHighAltitudeObservations covers
// the readiness gate when every observation is above the
// ceiling: hasTightHorizon must return false so Estimate
// declines, instead of returning a wide unusable fix.
func TestHasTightHorizonRejectsAllHighAltitudeObservations(t *testing.T) {
	t.Parallel()

	obs := []observation{
		{horizonNm: horizonNmFor(35000, defaultAntennaHeightFt)},
		{horizonNm: horizonNmFor(40000, defaultAntennaHeightFt)},
	}

	if hasTightHorizon(obs, horizonNmFor(20000, defaultAntennaHeightFt)) {
		t.Error("hasTightHorizon returned true for an all-high-altitude set")
	}
}

// TestHasTightHorizonAcceptsAtLeastOneLow ensures a single
// low-altitude entry is sufficient to clear the gate.
func TestHasTightHorizonAcceptsAtLeastOneLow(t *testing.T) {
	t.Parallel()

	obs := []observation{
		{horizonNm: horizonNmFor(35000, defaultAntennaHeightFt)},
		{horizonNm: horizonNmFor(5000, defaultAntennaHeightFt)},
		{horizonNm: horizonNmFor(40000, defaultAntennaHeightFt)},
	}

	if !hasTightHorizon(obs, horizonNmFor(20000, defaultAntennaHeightFt)) {
		t.Error("hasTightHorizon returned false despite a 5000 ft observation")
	}
}

// TestInitialSearchRegionPicksSmallestHorizon pins the anchor
// rule: the first-level search is centred on the observation
// with the tightest horizon (smallest constraint), because the
// receiver is provably inside that circle. The altitude floor in
// Observe is what keeps that promise honest.
func TestInitialSearchRegionPicksSmallestHorizon(t *testing.T) {
	t.Parallel()

	obs := []observation{
		{lat: 50.0, lon: 5.0, horizonNm: 200},
		{lat: 52.0, lon: 4.0, horizonNm: 60}, // smallest
		{lat: 54.0, lon: 3.0, horizonNm: 150},
	}

	gotLat, gotLon, gotHalfWidth := initialSearchRegion(obs)
	if gotLat != 52.0 || gotLon != 4.0 || gotHalfWidth != 60 {
		t.Errorf("initialSearchRegion = (%v, %v, %v), want (52.0, 4.0, 60)",
			gotLat, gotLon, gotHalfWidth)
	}
}

// TestCountCirclesContainingFullyInside covers the all-in case:
// a point at the centroid of every observation should be inside
// every horizon circle (since horizons are generous).
func TestCountCirclesContainingFullyInside(t *testing.T) {
	t.Parallel()

	obs := []observation{
		{lat: 52.0, lon: 4.0, horizonNm: 100},
		{lat: 52.5, lon: 4.5, horizonNm: 100},
		{lat: 51.5, lon: 3.5, horizonNm: 100},
	}

	if got := countCirclesContaining(obs, 52.0, 4.0); got != 3 {
		t.Errorf("countCirclesContaining at centroid = %d, want 3", got)
	}
}

// TestCountCirclesContainingOutsideEverything covers the
// fully-outside case: a point far away from every observation
// should be inside zero circles.
func TestCountCirclesContainingOutsideEverything(t *testing.T) {
	t.Parallel()

	obs := []observation{
		{lat: 52.0, lon: 4.0, horizonNm: 50},
		{lat: 52.5, lon: 4.5, horizonNm: 50},
	}

	// 30° south of every observation: way outside any horizon.
	if got := countCirclesContaining(obs, 22.0, 4.0); got != 0 {
		t.Errorf("countCirclesContaining far away = %d, want 0", got)
	}
}

// TestBoundingHalfDiagonalNmFloorsAtHalfStep pins the
// minimum-radius floor: when the bounding box collapses to a
// single point (single tied best), the returned half-diagonal
// must still reflect the grid resolution, not zero — otherwise
// the confidence radius would lie about precision.
func TestBoundingHalfDiagonalNmFloorsAtHalfStep(t *testing.T) {
	t.Parallel()

	got := boundingHalfDiagonalNm(52.0, 52.0, 4.0, 4.0, 0.6, 10.0)
	if got != 5.0 {
		t.Errorf("collapsed bounding box at step=10 = %v, want 5.0 (floor)", got)
	}
}

// TestBoundingHalfDiagonalNmReturnsActualWhenAboveFloor covers
// the non-floored path so we see both branches.
func TestBoundingHalfDiagonalNmReturnsActualWhenAboveFloor(t *testing.T) {
	t.Parallel()

	// 1° lat × 1° lon at cos=1.0 = 60 nm × 60 nm box -> half-diag
	// = sqrt(60² + 60²) / 2 ≈ 42.4 nm. Step=1 gives floor=0.5 nm.
	got := boundingHalfDiagonalNm(52.0, 53.0, 4.0, 5.0, 1.0, 1.0)
	want := math.Sqrt(60*60+60*60) / 2

	if math.Abs(got-want) > 0.1 {
		t.Errorf("bounding half-diagonal = %v, want %v", got, want)
	}
}

// TestGridSearchFallsBackWhenNoCircleHit pins the degenerate
// branch: gridSearch must not crash or NaN when no candidate
// point falls inside any horizon circle. It returns the input
// centre so the next, tighter level can try again.
func TestGridSearchFallsBackWhenNoCircleHit(t *testing.T) {
	t.Parallel()

	// Single observation 1000 nm away with a 50 nm horizon — no
	// candidate inside the search box can possibly intersect it.
	obs := []observation{{lat: 0.0, lon: 0.0, horizonNm: 50}}

	gotLat, gotLon := gridSearch(obs, 52.0, 4.0, 25.0)
	if gotLat != 52.0 || gotLon != 4.0 {
		t.Errorf("degenerate gridSearch = (%v, %v), want (52.0, 4.0)", gotLat, gotLon)
	}
}

// TestSolveConvergesNearSyntheticReceiver runs the full solve
// path on a synthetic observation set generated around a known
// receiver. The estimate must land within 30 nm of the true
// receiver — the target accuracy uAirwaves needs for CPR
// locally-unambiguous decoding.
func TestSolveConvergesNearSyntheticReceiver(t *testing.T) {
	t.Parallel()

	const (
		receiverLat = 52.0
		receiverLon = 4.0
		toleranceNm = 30.0
	)

	obs := syntheticObservations(receiverLat, receiverLon)
	fix := solve(obs)

	gotDistNm := distanceNm(fix.Latitude, fix.Longitude, receiverLat, receiverLon)
	if gotDistNm > toleranceNm {
		t.Errorf("solve estimate (%.4f, %.4f) is %.1f nm from receiver, want < %.0f nm",
			fix.Latitude, fix.Longitude, gotDistNm, toleranceNm)
	}
}

// TestRunwayAircraftNoLongerCapturesTheEstimate is the field
// defect, both halves of it. A receiver 17.6 nm from Schiphol
// hears an aircraft rolling on a runway at 50 ft. Under the
// single-term horizon formula that aircraft's circle is 8.7 nm
// wide, so it excludes the receiver, and being the tightest
// circle in the set it is also what the search anchors on: the
// estimate lands on the runway and the confidence rule calls it
// pinpoint. The altitude floor, the antenna term and the margin
// each remove that outcome on their own.
func TestRunwayAircraftNoLongerCapturesTheEstimate(t *testing.T) {
	t.Parallel()

	fixes := schipholFixes()

	t.Run("pre-fix horizons hand the estimate to the runway", func(t *testing.T) {
		t.Parallel()

		obs := legacyObservations(fixes)
		estimate := solve(obs)

		runwayErrNm := distanceNm(estimate.Latitude, estimate.Longitude, runwayAircraftLat, runwayAircraftLon)
		if runwayErrNm > 1.0 {
			t.Errorf("pre-fix estimate is %.2f nm from the runway aircraft, want it captured (< 1 nm)", runwayErrNm)
		}

		receiverErrNm := distanceNm(estimate.Latitude, estimate.Longitude,
			receiverNearSchipholLat, receiverNearSchipholLon)
		if receiverErrNm < 15.0 {
			t.Errorf("pre-fix estimate is %.2f nm from the receiver, want the documented >15 nm error", receiverErrNm)
		}

		// The confidence rule that shipped with those horizons
		// measured the tied best points at the search's finest
		// level, which reported a hundredth of a nautical mile for
		// that 17 nm error.
		_, _, halfWidth := initialSearchRegion(obs)
		finest := halfWidth / math.Pow(gridSearchShrinkFactor, gridSearchLevels-1)

		oldConfidence := legacyTiedSpreadNm(obs, estimate.Latitude, estimate.Longitude, finest)
		if oldConfidence > 0.1 {
			t.Errorf("pre-fix confidence = %.4f nm, want the documented near-zero value", oldConfidence)
		}

		t.Logf("pre-fix: %.2f nm error, %.4f nm confidence", receiverErrNm, oldConfidence)
	})

	t.Run("post-fix the receiver wins and the confidence covers the error", func(t *testing.T) {
		t.Parallel()

		loc := New()
		for _, aircraft := range fixes {
			loc.Observe(aircraft.lat, aircraft.lon, aircraft.altFt)
		}

		if got := loc.ObservationCount(); got != len(fixes)-1 {
			t.Fatalf("buffered %d observations, want %d (the runway aircraft dropped)", got, len(fixes)-1)
		}

		estimate, ok := loc.Estimate()
		if !ok {
			t.Fatal("Estimate returned ok=false on the Schiphol scenario")
		}

		errNm := distanceNm(estimate.Latitude, estimate.Longitude,
			receiverNearSchipholLat, receiverNearSchipholLon)
		if errNm > 5.0 {
			t.Errorf("post-fix estimate is %.2f nm from the receiver, want < 5 nm", errNm)
		}

		if estimate.ConfidenceRadiusNm < errNm {
			t.Errorf("confidence %.2f nm is smaller than the %.2f nm error it needs to cover",
				estimate.ConfidenceRadiusNm, errNm)
		}

		if estimate.Violated != 0 {
			t.Errorf("Violated = %d on a consistent set, want 0", estimate.Violated)
		}

		t.Logf("post-fix: %.2f nm error, %.2f nm confidence", errNm, estimate.ConfidenceRadiusNm)
	})
}

// TestSolveReportsViolatedCircles covers the constraint-conflict
// accounting. Violated is how many circles exclude the estimate,
// and it drives the threshold the confidence scan uses.
func TestSolveReportsViolatedCircles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		obs          []observation
		wantViolated int
		wantMinConf  float64
	}{
		{
			name:         "consistent set violates nothing",
			obs:          syntheticObservations(52.0, 4.0),
			wantViolated: 0,
			wantMinConf:  1.0,
		},
		{
			// Eight coincident tight circles agree; two wide ones
			// 185 nm east do not reach them. The search sits in the
			// majority, so the threshold stays above the clamp.
			name: "minority dissent keeps the threshold above the clamp",
			obs: append(repeatObservation(8, observation{lat: 52.0, lon: 4.0, horizonNm: 30}),
				repeatObservation(2, observation{lat: 52.0, lon: 9.0, horizonNm: 100})...),
			wantViolated: 2,
			wantMinConf:  20.0,
		},
		{
			// The tightest circle is the odd one out, so the search
			// anchors inside it and the majority is what gets
			// violated. The threshold clamps at one.
			name: "majority dissent clamps the threshold at one",
			obs: append(repeatObservation(2, observation{lat: 52.0, lon: 4.0, horizonNm: 50}),
				observation{lat: 52.0, lon: 10.0, horizonNm: 10}),
			wantViolated: 2,
			wantMinConf:  5.0,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			fix := solve(testCase.obs)
			if fix.Violated != testCase.wantViolated {
				t.Errorf("Violated = %d, want %d", fix.Violated, testCase.wantViolated)
			}

			if fix.ConfidenceRadiusNm < testCase.wantMinConf {
				t.Errorf("ConfidenceRadiusNm = %.2f nm, want at least %.2f",
					fix.ConfidenceRadiusNm, testCase.wantMinConf)
			}
		})
	}
}

// TestSolveConfidenceNeverBelowOneFinestGridCell pins the floor
// the Fix contract promises. Reporting a radius finer than the
// grid that produced it is the lie this package used to tell.
func TestSolveConfidenceNeverBelowOneFinestGridCell(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		obs  []observation
	}{
		{name: "synthetic ring", obs: syntheticObservations(52.0, 4.0)},
		{name: "schiphol scenario", obs: observationsFrom(schipholFixes(), defaultAntennaHeightFt)},
		{name: "single circle", obs: []observation{{lat: 52.0, lon: 4.0, horizonNm: 40}}},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, _, halfWidth := initialSearchRegion(testCase.obs)
			finestStep := halfWidth / math.Pow(gridSearchShrinkFactor, gridSearchLevels-1) / gridSearchHalfSize

			if got := solve(testCase.obs).ConfidenceRadiusNm; got < finestStep {
				t.Errorf("ConfidenceRadiusNm = %v, want at least one finest grid cell (%v)", got, finestStep)
			}
		})
	}
}

// aircraftFix is a synthetic ADSB position report, the shape
// Observe takes. Kept separate from observation so the scenario
// fixtures can be turned into either the current or the pre-fix
// horizons.
type aircraftFix struct {
	lat, lon, altFt float64
}

// schipholFixes builds the reported field scenario: 40 aircraft
// between 3000 and 35000 ft ringing the receiver, each within its
// own horizon, plus one aircraft rolling on a Schiphol runway
// 17.6 nm away that the receiver hears regardless.
func schipholFixes() []aircraftFix {
	const aircraftCount = 40

	altitudes := []float64{3000, 7000, 12000, 20000, 35000}
	out := make([]aircraftFix, 0, aircraftCount+1)

	for index := range aircraftCount {
		altFt := altitudes[index%len(altitudes)]
		// Half the nominal horizon on a full bearing sweep, so
		// every aircraft is comfortably inside its own circle
		// under either horizon formula.
		offsetNm := horizonCoefficient * math.Sqrt(altFt) / 2
		bearingRad := float64(index) * (2 * math.Pi / aircraftCount)

		out = append(out, aircraftFix{
			lat: receiverNearSchipholLat + offsetNm*math.Cos(bearingRad)/nauticalMilePerDegree,
			lon: receiverNearSchipholLon + offsetNm*math.Sin(bearingRad)/
				(nauticalMilePerDegree*math.Cos(receiverNearSchipholLat*deg2Rad)),
			altFt: altFt,
		})
	}

	return append(out, aircraftFix{lat: runwayAircraftLat, lon: runwayAircraftLon, altFt: runwayAircraftAltFt})
}

// observationsFrom converts scenario fixes the way Observe does,
// altitude floor included.
func observationsFrom(fixes []aircraftFix, antennaFt float64) []observation {
	out := make([]observation, 0, len(fixes))

	for _, fix := range fixes {
		if fix.altFt < minObservationAltitudeFt {
			continue
		}

		out = append(out, observation{lat: fix.lat, lon: fix.lon, horizonNm: horizonNmFor(fix.altFt, antennaFt)})
	}

	return out
}

// legacyObservations converts scenario fixes the way Observe did
// before this package accounted for antenna height, reception
// margin and surface traffic: a bare 1.23 * sqrt(alt), no floor.
func legacyObservations(fixes []aircraftFix) []observation {
	out := make([]observation, 0, len(fixes))

	for _, fix := range fixes {
		out = append(out, observation{
			lat:       fix.lat,
			lon:       fix.lon,
			horizonNm: horizonCoefficient * math.Sqrt(fix.altFt),
		})
	}

	return out
}

// legacyTiedSpreadNm is the confidence rule this package used to
// apply: the bounding half-diagonal of the tied best points at
// the search's finest level. It lives in the test file because
// the number it produces is the defect, and the regression test
// needs to keep producing it.
func legacyTiedSpreadNm(obs []observation, centerLat, centerLon, halfWidth float64) float64 {
	step := halfWidth / float64(gridSearchHalfSize)
	cosLat := cosLatFloor(centerLat)

	var bestCount, tied int

	var minLat, maxLat, minLon, maxLon float64

	for i := -gridSearchHalfSize; i <= gridSearchHalfSize; i++ {
		for j := -gridSearchHalfSize; j <= gridSearchHalfSize; j++ {
			candidateLat := centerLat + float64(i)*step/nauticalMilePerDegree
			candidateLon := centerLon + float64(j)*step/(nauticalMilePerDegree*cosLat)

			count := countCirclesContaining(obs, candidateLat, candidateLon)
			if count < bestCount {
				continue
			}

			if count > bestCount {
				bestCount, tied = count, 0
				minLat, maxLat = candidateLat, candidateLat
				minLon, maxLon = candidateLon, candidateLon
			}

			minLat, maxLat = math.Min(minLat, candidateLat), math.Max(maxLat, candidateLat)
			minLon, maxLon = math.Min(minLon, candidateLon), math.Max(maxLon, candidateLon)
			tied++
		}
	}

	if tied == 0 {
		return halfWidth
	}

	return boundingHalfDiagonalNm(minLat, maxLat, minLon, maxLon, cosLat, step)
}

// repeatObservation returns count copies of one observation, for
// constraint sets where the geometry matters and the individual
// aircraft do not.
func repeatObservation(count int, template observation) []observation {
	out := make([]observation, count)
	for index := range out {
		out[index] = template
	}

	return out
}

// syntheticObservations builds a representative observation set:
// 60 fixes ringing the receiver at varied bearings and altitudes
// (1500–38000 ft) — every fix is within its own horizon of the
// receiver, which is the invariant Observe relies on. Used by
// internal and external tests; lives here because it manipulates
// the unexported observation type.
func syntheticObservations(receiverLat, receiverLon float64) []observation {
	const obsCount = 60

	out := make([]observation, 0, obsCount)
	altitudes := []float64{1500, 5000, 12000, 25000, 38000}

	for index := range obsCount {
		altFt := altitudes[index%len(altitudes)]
		// Place fix at half-horizon distance on a 12° bearing
		// sweep — guaranteed inside the horizon of the receiver
		// at (receiverLat, receiverLon).
		bearingRad := float64(index) * (math.Pi / 30)
		offsetNm := horizonCoefficient * math.Sqrt(altFt) / 2
		dLat := offsetNm * math.Cos(bearingRad) / nauticalMilePerDegree
		dLon := offsetNm * math.Sin(bearingRad) / (nauticalMilePerDegree * math.Cos(receiverLat*deg2Rad))

		out = append(out, observation{
			lat:       receiverLat + dLat,
			lon:       receiverLon + dLon,
			horizonNm: horizonNmFor(altFt, defaultAntennaHeightFt),
		})
	}

	return out
}
