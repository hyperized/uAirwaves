package selflocate_test

import (
	"math"
	"sync"
	"testing"

	"github.com/hyperized/uAirwaves/pkg/selflocate"
)

// TestNewWithDefaults covers the default construction path: no
// options, no panics, ObservationCount starts at zero, Estimate
// declines with ok=false.
func TestNewWithDefaults(t *testing.T) {
	t.Parallel()

	loc := selflocate.New()

	if got := loc.ObservationCount(); got != 0 {
		t.Errorf("fresh Locator ObservationCount = %d, want 0", got)
	}

	if _, ok := loc.Estimate(); ok {
		t.Error("fresh Locator Estimate returned ok=true, want false")
	}
}

// TestObserveSkipsZeroPosition covers the (0, 0) sentinel reject
// path. Zero lat/lon are the "unresolved" marker in pkg/airplane
// and must not enter the observation buffer.
func TestObserveSkipsZeroPosition(t *testing.T) {
	t.Parallel()

	loc := selflocate.New()
	loc.Observe(0, 0, 30000)

	if got := loc.ObservationCount(); got != 0 {
		t.Errorf("after Observe(0, 0, 30000): ObservationCount = %d, want 0", got)
	}
}

// TestObserveSkipsNonPositiveAltitude pins the altitude > 0 gate.
// Without altitude the horizon formula can't run.
func TestObserveSkipsNonPositiveAltitude(t *testing.T) {
	t.Parallel()

	loc := selflocate.New()
	loc.Observe(52.0, 4.0, 0)
	loc.Observe(52.0, 4.0, -100)

	if got := loc.ObservationCount(); got != 0 {
		t.Errorf("after non-positive altitude observes: ObservationCount = %d, want 0", got)
	}
}

// TestObserveRejectsBelowTheAltitudeFloor pins the surface-traffic
// gate. An aircraft rolling on a runway reports a barometric
// altitude near zero, which yields a horizon of a few nm; the
// receiver hears it from far outside that circle, and because it
// is the tightest circle in the set the search anchors on it.
func TestObserveRejectsBelowTheAltitudeFloor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		altFt     float64
		wantCount int
	}{
		{name: "rolling on the runway", altFt: 50, wantCount: 0},
		{name: "200 ft is still surface traffic", altFt: 200, wantCount: 0},
		{name: "just below the floor", altFt: 299, wantCount: 0},
		{name: "on the floor", altFt: 300, wantCount: 1},
		{name: "400 ft is airborne", altFt: 400, wantCount: 1},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			loc := selflocate.New()
			loc.Observe(52.31, 4.77, testCase.altFt)

			if got := loc.ObservationCount(); got != testCase.wantCount {
				t.Errorf("after Observe at %.0f ft: ObservationCount = %d, want %d",
					testCase.altFt, got, testCase.wantCount)
			}
		})
	}
}

// TestAntennaHeightWidensTheConfidenceRegion covers the antenna
// term from the outside. A taller antenna sees further, so every
// horizon circle grows, the region satisfying all of them grows
// with it, and the reported confidence has to follow.
func TestAntennaHeightWidensTheConfidenceRegion(t *testing.T) {
	t.Parallel()

	const (
		trueLat = 51.5
		trueLon = 4.5
	)

	atGround := selflocate.New(selflocate.WithAntennaHeightFt(0))
	onAMast := selflocate.New(selflocate.WithAntennaHeightFt(500))

	feedSyntheticObservations(atGround, trueLat, trueLon)
	feedSyntheticObservations(onAMast, trueLat, trueLon)

	groundFix := mustEstimate(t, atGround)
	mastFix := mustEstimate(t, onAMast)

	if mastFix.ConfidenceRadiusNm <= groundFix.ConfidenceRadiusNm {
		t.Errorf("500 ft antenna gave confidence %.2f nm, want more than the ground-level %.2f nm",
			mastFix.ConfidenceRadiusNm, groundFix.ConfidenceRadiusNm)
	}
}

// TestObserveValidFixIncrementsCount covers the happy-path Observe.
func TestObserveValidFixIncrementsCount(t *testing.T) {
	t.Parallel()

	loc := selflocate.New()
	loc.Observe(52.0, 4.0, 35000)

	if got := loc.ObservationCount(); got != 1 {
		t.Errorf("after one valid Observe: ObservationCount = %d, want 1", got)
	}
}

// TestObserveRingBufferCapsAtMax exercises the max-observations
// option and the ring-buffer trim that goes with it.
func TestObserveRingBufferCapsAtMax(t *testing.T) {
	t.Parallel()

	const maxObs = 5

	loc := selflocate.New(selflocate.WithMaxObservations(maxObs))

	for index := range 20 {
		loc.Observe(52.0+0.001*float64(index), 4.0, 35000)
	}

	if got := loc.ObservationCount(); got != maxObs {
		t.Errorf("after 20 Observes with cap=%d: ObservationCount = %d, want %d",
			maxObs, got, maxObs)
	}
}

// TestEstimateDeclinesWhenBelowMinObservations pins the lower
// gate: fewer than minObservations fixes -> ok=false even if
// they include low-altitude observations.
func TestEstimateDeclinesWhenBelowMinObservations(t *testing.T) {
	t.Parallel()

	loc := selflocate.New(selflocate.WithMinObservations(10))

	for i := range 5 {
		loc.Observe(52.0+0.001*float64(i), 4.0, 5000)
	}

	if _, ok := loc.Estimate(); ok {
		t.Error("Estimate returned ok=true with only 5 fixes vs min=10")
	}
}

// TestEstimateDeclinesWhenAllHighAltitude exercises the
// readiness gate even with enough observations.
func TestEstimateDeclinesWhenAllHighAltitude(t *testing.T) {
	t.Parallel()

	loc := selflocate.New(
		selflocate.WithMinObservations(10),
		selflocate.WithMaxAltitudeForReady(20000),
	)

	for i := range 30 {
		loc.Observe(52.0+0.001*float64(i), 4.0, 35000) // all FL350
	}

	if _, ok := loc.Estimate(); ok {
		t.Error("Estimate returned ok=true despite all-high-altitude observations")
	}
}

// TestEstimateConvergesNearTrueReceiver is the end-to-end happy
// path: a busy enough sample around a known receiver should land
// the estimate within 30 nm of the truth.
func TestEstimateConvergesNearTrueReceiver(t *testing.T) {
	t.Parallel()

	const (
		trueLat     = 52.0
		trueLon     = 4.0
		toleranceNm = 30.0
	)

	loc := selflocate.New()
	feedSyntheticObservations(loc, trueLat, trueLon)

	fix, ok := loc.Estimate()
	if !ok {
		t.Fatal("Estimate returned ok=false on a fully populated synthetic dataset")
	}

	if fix.ObservationCount == 0 {
		t.Error("Fix.ObservationCount = 0, want > 0")
	}

	gotDistNm := flatDistanceNm(fix.Latitude, fix.Longitude, trueLat, trueLon)
	if gotDistNm > toleranceNm {
		t.Errorf("estimate (%.4f, %.4f) is %.1f nm from receiver, want < %.0f nm",
			fix.Latitude, fix.Longitude, gotDistNm, toleranceNm)
	}

	if fix.ConfidenceRadiusNm < gotDistNm {
		t.Errorf("Fix.ConfidenceRadiusNm = %v, want at least the %.2f nm error it covers",
			fix.ConfidenceRadiusNm, gotDistNm)
	}

	if fix.Violated != 0 {
		t.Errorf("Fix.Violated = %d on a set built to be consistent, want 0", fix.Violated)
	}
}

// TestObserveAndEstimateAreThreadSafe runs concurrent Observe
// and Estimate calls under the race detector to confirm the
// sync.RWMutex protects every access path.
func TestObserveAndEstimateAreThreadSafe(t *testing.T) {
	t.Parallel()

	loc := selflocate.New()

	var waitGroup sync.WaitGroup

	const writers = 4

	const writes = 50

	for writer := range writers {
		waitGroup.Go(func() {
			for index := range writes {
				loc.Observe(52.0+0.001*float64(writer*writes+index), 4.0, 5000)
			}
		})
	}

	waitGroup.Go(func() {
		for range 50 {
			_, _ = loc.Estimate()
		}
	})

	waitGroup.Wait()
}

// TestWithOptionRejectsInvalidValues pins the "ignore nonsense"
// contract on the three options: zero / negative values must
// leave the default in place.
func TestWithOptionRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	loc := selflocate.New(
		selflocate.WithMaxObservations(0),       // ignored
		selflocate.WithMinObservations(-1),      // ignored
		selflocate.WithMaxAltitudeForReady(-50), // ignored
		selflocate.WithAntennaHeightFt(-5),      // ignored
	)

	// The locator should still accept observations and decline
	// Estimate until the (default) gates are met.
	loc.Observe(52.0, 4.0, 35000)

	if got := loc.ObservationCount(); got != 1 {
		t.Errorf("after Observe under invalid options: count = %d, want 1", got)
	}

	if _, ok := loc.Estimate(); ok {
		t.Error("Estimate returned ok=true with only 1 fix under invalid options")
	}
}

// feedSyntheticObservations drives 60 Observe calls around a
// receiver position; mirrors the internal synthetic fixture but
// uses only the public API so external tests stay black-box.
func feedSyntheticObservations(loc *selflocate.Locator, receiverLat, receiverLon float64) {
	const obsCount = 60

	altitudes := []float64{1500, 5000, 12000, 25000, 38000}

	for i := range obsCount {
		altFt := altitudes[i%len(altitudes)]
		horizonNm := 1.23 * math.Sqrt(altFt)

		bearingRad := float64(i) * (math.Pi / 30)
		offsetNm := horizonNm / 2
		dLat := offsetNm * math.Cos(bearingRad) / 60.0
		dLon := offsetNm * math.Sin(bearingRad) / (60.0 * math.Cos(receiverLat*math.Pi/180))

		loc.Observe(receiverLat+dLat, receiverLon+dLon, altFt)
	}
}

// flatDistanceNm is a local copy of the package's flat-Earth
// distance formula so external tests don't depend on a package-
// internal export just to assert convergence.
func flatDistanceNm(lat1, lon1, lat2, lon2 float64) float64 {
	const nauticalMilePerDegree = 60.0

	dLat := lat1 - lat2
	midLat := (lat1 + lat2) / 2
	dLon := (lon1 - lon2) * math.Cos(midLat*math.Pi/180)

	return math.Sqrt(dLat*dLat+dLon*dLon) * nauticalMilePerDegree
}

// BenchmarkEstimate measures a full Estimate at the observation
// cap, which is what bounds the self-locate worker's tick. The
// confidence scan is the bulk of it.
func BenchmarkEstimate(b *testing.B) {
	loc := selflocate.New()
	for range 9 {
		feedSyntheticObservations(loc, 52.0, 4.0)
	}

	b.ResetTimer()

	for b.Loop() {
		if _, ok := loc.Estimate(); !ok {
			b.Fatal("Estimate returned ok=false")
		}
	}
}

// mustEstimate fails the test if the locator declines to produce
// a fix, so callers can compare two fixes without repeating the
// readiness check.
func mustEstimate(t *testing.T, loc *selflocate.Locator) selflocate.Fix {
	t.Helper()

	fix, ready := loc.Estimate()
	if !ready {
		t.Fatal("Estimate returned ok=false on a fully populated dataset")
	}

	return fix
}
