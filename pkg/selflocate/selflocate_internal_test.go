package selflocate

import (
	"math"
	"testing"
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

// TestHasLowAltitudeRejectsAllHighAltitudeObservations covers
// the readiness gate when every observation is above the
// ceiling: hasLowAltitude must return false so Estimate
// declines, instead of returning a wide unusable fix.
func TestHasLowAltitudeRejectsAllHighAltitudeObservations(t *testing.T) {
	t.Parallel()

	obs := []observation{
		{horizonNm: horizonCoefficient * math.Sqrt(35000)},
		{horizonNm: horizonCoefficient * math.Sqrt(40000)},
	}

	if hasLowAltitude(obs, 20000) {
		t.Error("hasLowAltitude returned true for an all-high-altitude set")
	}
}

// TestHasLowAltitudeAcceptsAtLeastOneLow ensures a single
// low-altitude entry is sufficient to clear the gate.
func TestHasLowAltitudeAcceptsAtLeastOneLow(t *testing.T) {
	t.Parallel()

	obs := []observation{
		{horizonNm: horizonCoefficient * math.Sqrt(35000)},
		{horizonNm: horizonCoefficient * math.Sqrt(5000)}, // ~87 nm
		{horizonNm: horizonCoefficient * math.Sqrt(40000)},
	}

	if !hasLowAltitude(obs, 20000) {
		t.Error("hasLowAltitude returned false despite a 5000 ft observation")
	}
}

// TestInitialSearchRegionPicksSmallestHorizon pins the anchor
// rule: the first-level search is centred on the observation
// with the tightest horizon (smallest constraint), because the
// receiver is provably inside that circle.
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
// centre and the input half-width as the confidence radius.
func TestGridSearchFallsBackWhenNoCircleHit(t *testing.T) {
	t.Parallel()

	// Single observation 1000 nm away with a 50 nm horizon — no
	// candidate inside the search box can possibly intersect it.
	obs := []observation{{lat: 0.0, lon: 0.0, horizonNm: 50}}

	gotLat, gotLon, gotSpread := gridSearch(obs, 52.0, 4.0, 25.0)
	if gotLat != 52.0 || gotLon != 4.0 || gotSpread != 25.0 {
		t.Errorf("degenerate gridSearch = (%v, %v, %v), want (52.0, 4.0, 25.0)",
			gotLat, gotLon, gotSpread)
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
		horizonNm := horizonCoefficient * math.Sqrt(altFt)
		// Place fix at half-horizon distance on a 12° bearing
		// sweep — guaranteed inside the horizon of the receiver
		// at (receiverLat, receiverLon).
		bearingRad := float64(index) * (math.Pi / 30)
		offsetNm := horizonNm / 2
		dLat := offsetNm * math.Cos(bearingRad) / nauticalMilePerDegree
		dLon := offsetNm * math.Sin(bearingRad) / (nauticalMilePerDegree * math.Cos(receiverLat*deg2Rad))

		out = append(out, observation{
			lat:       receiverLat + dLat,
			lon:       receiverLon + dLon,
			horizonNm: horizonNm,
		})
	}

	return out
}
