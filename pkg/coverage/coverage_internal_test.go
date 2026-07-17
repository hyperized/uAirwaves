package coverage

import (
	"math"
	"testing"
)

func TestDistanceBin(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name       string
		distanceNm float64
		want       int
	}{
		{name: "zero distance", distanceNm: 0, want: 0},
		{name: "just under first boundary", distanceNm: 9.999, want: 0},
		{name: "exact boundary rolls up", distanceNm: 10.0, want: 1},
		{name: "just under last bin", distanceNm: 249.9, want: 24},
		{name: "clamp at max", distanceNm: 250.0, want: 24},
		{name: "clamp far beyond max", distanceNm: 1000, want: 24},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := distanceBin(testCase.distanceNm); got != testCase.want {
				t.Errorf("distanceBin(%v) = %d, want %d", testCase.distanceNm, got, testCase.want)
			}
		})
	}
}

func TestAltitudeBand(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name  string
		altFt float64
		want  int
	}{
		{name: "zero altitude", altFt: 0, want: 0},
		{name: "exact boundary rolls up", altFt: 5000, want: 1},
		{name: "just under top band", altFt: 49999, want: 9},
		{name: "clamp at max", altFt: 50000, want: 9},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := altitudeBand(testCase.altFt); got != testCase.want {
				t.Errorf("altitudeBand(%v) = %d, want %d", testCase.altFt, got, testCase.want)
			}
		})
	}
}

func TestBearingSector(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name       string
		bearingDeg float64
		want       int
	}{
		{name: "zero bearing", bearingDeg: 0, want: 0},
		{name: "one sector width", bearingDeg: 22.5, want: 1},
		{name: "just under full circle", bearingDeg: 359.9, want: 15},
		{name: "clamp guard at exact 360", bearingDeg: 360, want: 15},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := bearingSector(testCase.bearingDeg); got != testCase.want {
				t.Errorf("bearingSector(%v) = %d, want %d", testCase.bearingDeg, got, testCase.want)
			}
		})
	}
}

func TestNormalizeBearing(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		deg  float64
		want float64
	}{
		{name: "negative wraps up", deg: -10, want: 350},
		{name: "beyond full circle wraps down", deg: 370, want: 10},
		{name: "exact full circle wraps to zero", deg: 360, want: 0},
		{name: "zero degrees stays zero", deg: 0, want: 0},
		{name: "mid-range unchanged", deg: 22.5, want: 22.5},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := normalizeBearing(testCase.deg); got != testCase.want {
				t.Errorf("normalizeBearing(%v) = %v, want %v", testCase.deg, got, testCase.want)
			}
		})
	}
}

func TestSaturatingInc(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		initial uint32
		want    uint32
	}{
		{name: "increments below max", initial: math.MaxUint32 - 1, want: math.MaxUint32},
		{name: "stays saturated at max", initial: math.MaxUint32, want: math.MaxUint32},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			counter := testCase.initial
			saturatingInc(&counter)

			if counter != testCase.want {
				t.Errorf("saturatingInc from %d = %d, want %d", testCase.initial, counter, testCase.want)
			}
		})
	}
}

// assertTrackerIsZero fails the test if tracker holds any non-zero state.
// Extracted to keep TestTrackerObserveInvalidInputsAreIgnored's cognitive
// complexity under the linter's limit.
func assertTrackerIsZero(t *testing.T, tracker *Tracker) {
	t.Helper()

	for band := range tracker.cells {
		for bin := range tracker.cells[band] {
			if tracker.cells[band][bin] != 0 {
				t.Errorf("cells[%d][%d] = %d, want 0", band, bin, tracker.cells[band][bin])
			}
		}
	}

	for sector := range tracker.sectors {
		if tracker.sectors[sector] != 0 {
			t.Errorf("sectors[%d] = %v, want 0", sector, tracker.sectors[sector])
		}
	}

	if tracker.maxRangeNm != 0 {
		t.Errorf("maxRangeNm = %v, want 0", tracker.maxRangeNm)
	}

	if tracker.maxRangeAltFt != 0 {
		t.Errorf("maxRangeAltFt = %v, want 0", tracker.maxRangeAltFt)
	}
}

func TestTrackerObserveInvalidInputsAreIgnored(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name       string
		distanceNm float64
		bearingDeg float64
		altFt      float64
	}{
		{name: "negative distance", distanceNm: -1, bearingDeg: 90, altFt: 1000},
		{name: "negative altitude", distanceNm: 5, bearingDeg: 90, altFt: -1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			tracker := New()
			tracker.Observe(testCase.distanceNm, testCase.bearingDeg, testCase.altFt)
			assertTrackerIsZero(t, tracker)
		})
	}
}
