package ui_test

import (
	"strings"
	"testing"
	"time"

	"lab.hyperized.net/hyperized/uAirwaves/internal/ui"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/adsb"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplane"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplanes"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/location"
)

// TestNewStatsTrackerSeedsSampleTime locks in the contract that
// NewStatsTracker initialises the last-sample timestamp so the
// first Sample call has a positive elapsed window.
func TestNewStatsTrackerSeedsSampleTime(t *testing.T) {
	t.Parallel()

	tracker := ui.NewStatsTracker()
	if tracker == nil {
		t.Fatal("NewStatsTracker returned nil")
	}

	// Sleep one tick to guarantee elapsed > 0 even on the fastest CI.
	time.Sleep(time.Millisecond)

	framesPerSec, recoveredPerSec := tracker.Sample(adsb.Stats{TotalFrames: 10, RecoveredFrames: 2})
	if framesPerSec <= 0 {
		t.Errorf("framesPerSec = %v, want > 0 after first Sample with positive elapsed", framesPerSec)
	}

	if recoveredPerSec <= 0 {
		t.Errorf("recoveredPerSec = %v, want > 0", recoveredPerSec)
	}
}

// TestStatsTrackerZeroElapsedReturnsZeroRates exercises the
// `elapsed > 0` guard: two Sample calls back-to-back, with the
// second call's tick the same as the first by manipulating the
// reading order, must not divide by zero. We cannot easily make
// elapsed exactly 0 here, but we can confirm that two Sample
// calls in quick succession update the cumulative state.
func TestStatsTrackerSampleAdvancesState(t *testing.T) {
	t.Parallel()

	tracker := ui.NewStatsTracker()

	time.Sleep(time.Millisecond)

	tracker.Sample(adsb.Stats{TotalFrames: 5, RecoveredFrames: 1})

	time.Sleep(time.Millisecond)

	// Second sample: delta should reflect the increment.
	framesPerSec, _ := tracker.Sample(adsb.Stats{TotalFrames: 10, RecoveredFrames: 2})
	if framesPerSec <= 0 {
		t.Errorf("framesPerSec = %v, want > 0 (5 new frames / non-zero elapsed)", framesPerSec)
	}
}

// TestDisplayIdent covers both branches of the callsign-fallback
// helper.
func TestDisplayIdent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input airplane.Snapshot
		want  string
	}{
		{
			name:  "callsign preferred when set",
			input: airplane.Snapshot{ICAO: testICAO, Callsign: testCallsign},
			want:  testCallsign,
		},
		{
			name:  "ICAO fallback when empty callsign",
			input: airplane.Snapshot{ICAO: testICAO},
			want:  testICAO,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := ui.DisplayIdent(testCase.input); got != testCase.want {
				t.Errorf("DisplayIdent(%+v) = %q, want %q", testCase.input, got, testCase.want)
			}
		})
	}
}

// TestFormatStatsTextPositionedZero exercises the "—" branches:
// when Positioned == 0 nearest/farthest fall back to the dash;
// HighestAlt == 0 likewise hides the highest line.
func TestFormatStatsTextPositionedZero(t *testing.T) {
	t.Parallel()

	got := ui.FormatStatsText(ui.StatsRender{
		Tracked:          5,
		Positioned:       0,
		TotalFrames:      1000,
		RecoveredFrames:  10,
		CallsignsDecoded: 50,
		CallsignsApplied: 40,
	})

	if !strings.Contains(got, "Tracked[::-]    5  ([gray]0 positioned[white])") {
		t.Errorf("missing Tracked line; got:\n%s", got)
	}

	if !strings.Contains(got, "Nearest[::-]    —") {
		t.Errorf("missing nearest dash branch; got:\n%s", got)
	}

	if !strings.Contains(got, "Farthest[::-]   —") {
		t.Errorf("missing farthest dash branch; got:\n%s", got)
	}

	if !strings.Contains(got, "Highest[::-]    —") {
		t.Errorf("missing highest dash branch; got:\n%s", got)
	}
}

// TestFormatStatsTextWithAggregates exercises every populated
// branch: nearest, farthest, highest all render with their
// dimmed callsign tail.
func TestFormatStatsTextWithAggregates(t *testing.T) {
	t.Parallel()

	got := ui.FormatStatsText(ui.StatsRender{
		Tracked:          3,
		Positioned:       2,
		NearestDist:      1.5,
		NearestCallsign:  "KLM1023",
		FarthestDist:     42.0,
		FarthestCallsign: "BAW123",
		HighestAlt:       38000,
		HighestCallsign:  "DLH456",
		FramesPerSec:     12.3,
		RecoveredPerSec:  0.45,
		TotalFrames:      999,
		RecoveredFrames:  17,
		CallsignsDecoded: 88,
		CallsignsApplied: 77,
	})

	for _, want := range []string{
		"1.5 nm  [gray]KLM1023[white]",
		"42.0 nm  [gray]BAW123[white]",
		"38000 ft  [gray]DLH456[white]",
		"12.3  ([gray]rec 0.45[white])",
		"999  ([gray]IDs 77/88[white])",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q; full:\n%s", want, got)
		}
	}
}

// newAirplaneAt is a small constructor for snapshot-bearing
// planes the AggregateStats tests need. The Update call burns the
// "first lastPositionTime is zero" gate so PositionHistory
// reflects the supplied coordinates.
func newAirplaneAt(t *testing.T, icao, callsign string, lat, lon, alt float64) *airplane.Airplane {
	t.Helper()

	plane := airplane.New(icao)
	plane.Update(
		airplane.WithCallsign(callsign),
		airplane.WithLatitude(lat),
		airplane.WithLongitude(lon),
		airplane.WithAltitude(alt),
	)

	return plane
}

// TestAggregateStatsReducesPlanes drives the walking helper
// through several distinct planes so nearest/farthest/highest
// each land on a different aircraft. The receiver sits at (52,
// 4); planes are placed on a roughly north-south line so the
// distances are easy to reason about by hand.
func TestAggregateStatsReducesPlanes(t *testing.T) {
	t.Parallel()

	myLocation := location.New(location.WithLatitude(52.0), location.WithLongitude(4.0))

	planeList := airplanes.New()
	planeList.Ensure("AAA001")
	planeList.Ensure("BBB002")
	planeList.Ensure("CCC003")

	near, _ := planeList.Get("AAA001")
	nearPlane := newAirplaneAt(t, "AAA001", "NEAR1", 52.05, 4.05, 5000)

	far, _ := planeList.Get("BBB002")
	farPlane := newAirplaneAt(t, "BBB002", "FAR2", 53.5, 5.5, 8000)

	high, _ := planeList.Get("CCC003")
	highPlane := newAirplaneAt(t, "CCC003", "HIGH3", 52.3, 4.3, 38000)

	// Copy the prepared state into the planes the list returned.
	nearSnap := nearPlane.GetSnapshot()
	farSnap := farPlane.GetSnapshot()
	highSnap := highPlane.GetSnapshot()

	near.Update(
		airplane.WithCallsign(nearSnap.Callsign),
		airplane.WithLatitude(nearSnap.Latitude),
		airplane.WithLongitude(nearSnap.Longitude),
		airplane.WithAltitude(nearSnap.Altitude),
	)
	far.Update(
		airplane.WithCallsign(farSnap.Callsign),
		airplane.WithLatitude(farSnap.Latitude),
		airplane.WithLongitude(farSnap.Longitude),
		airplane.WithAltitude(farSnap.Altitude),
	)
	high.Update(
		airplane.WithCallsign(highSnap.Callsign),
		airplane.WithLatitude(highSnap.Latitude),
		airplane.WithLongitude(highSnap.Longitude),
		airplane.WithAltitude(highSnap.Altitude),
	)

	tracker := ui.NewStatsTracker()
	stream := adsb.New()

	render := ui.AggregateStats(stream, tracker, myLocation, planeList)

	if render.Tracked != 3 {
		t.Errorf("Tracked = %d, want 3", render.Tracked)
	}

	if render.Positioned != 3 {
		t.Errorf("Positioned = %d, want 3", render.Positioned)
	}

	if render.NearestCallsign != "NEAR1" {
		t.Errorf("NearestCallsign = %q, want NEAR1", render.NearestCallsign)
	}

	if render.FarthestCallsign != "FAR2" {
		t.Errorf("FarthestCallsign = %q, want FAR2", render.FarthestCallsign)
	}

	if render.HighestCallsign != "HIGH3" {
		t.Errorf("HighestCallsign = %q, want HIGH3", render.HighestCallsign)
	}
}

// TestAggregateStatsSkipsPositionlessPlanes covers the
// `if snap.Latitude == 0 && snap.Longitude == 0 { continue }`
// branch: a freshly-Ensured plane has no fix and must not move
// nearest/farthest/highest off their defaults.
func TestAggregateStatsSkipsPositionlessPlanes(t *testing.T) {
	t.Parallel()

	myLocation := location.New(location.WithLatitude(52.0), location.WithLongitude(4.0))

	planeList := airplanes.New()
	planeList.Ensure("ZZZ999") // no position update

	tracker := ui.NewStatsTracker()
	stream := adsb.New()

	render := ui.AggregateStats(stream, tracker, myLocation, planeList)

	if render.Tracked != 1 {
		t.Errorf("Tracked = %d, want 1", render.Tracked)
	}

	if render.Positioned != 0 {
		t.Errorf("Positioned = %d, want 0", render.Positioned)
	}
}

// TestAggregateStatsSkipsHaversineInfinity covers the
// `if distance == math.MaxFloat64 { continue }` branch: when the
// receiver itself has no fix (lat=0, lon=0) HaversineDistance
// returns MaxFloat64 for every plane and AggregateStats must
// report zero positioned despite the planes having coordinates.
func TestAggregateStatsSkipsHaversineInfinity(t *testing.T) {
	t.Parallel()

	// receiver at origin sentinel
	myLocation := location.New()

	planeList := airplanes.New()
	planeList.Ensure("ABC001")

	plane, _ := planeList.Get("ABC001")
	plane.Update(
		airplane.WithLatitude(52.05),
		airplane.WithLongitude(4.05),
		airplane.WithAltitude(5000),
	)

	tracker := ui.NewStatsTracker()
	stream := adsb.New()

	render := ui.AggregateStats(stream, tracker, myLocation, planeList)

	if render.Tracked != 1 {
		t.Errorf("Tracked = %d, want 1", render.Tracked)
	}

	if render.Positioned != 0 {
		t.Errorf("Positioned = %d, want 0 (receiver at origin → infinity sentinel)", render.Positioned)
	}
}
