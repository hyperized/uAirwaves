package ui_test

import (
	"strings"
	"testing"
	"time"

	"github.com/hyperized/uAirwaves/internal/ui"
	"github.com/hyperized/uAirwaves/pkg/adsb"
	"github.com/hyperized/uAirwaves/pkg/airplane"
	"github.com/hyperized/uAirwaves/pkg/airplanes"
	"github.com/hyperized/uAirwaves/pkg/location"
)

// Callsign fixtures reused across the stats-record subtests.
// Hoisted out of the per-case struct literals so goconst stops
// flagging the same six-byte literals across test files.
const (
	csFarthest       = "BAW123"
	csHighest        = "DLH456"
	csFarthestRecord = "KLM999"
	csHighestRecord  = "QFA8"
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

	framesPerSec := tracker.Sample(adsb.Stats{TotalFrames: 10, RecoveredFrames: 2})
	if framesPerSec <= 0 {
		t.Errorf("framesPerSec = %v, want > 0 after first Sample with positive elapsed", framesPerSec)
	}

	if got := tracker.FramesPerSecRecord(); got != framesPerSec {
		t.Errorf("FramesPerSecRecord after first Sample = %v, want %v (the first non-zero rate)", got, framesPerSec)
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
	framesPerSec := tracker.Sample(adsb.Stats{TotalFrames: 10, RecoveredFrames: 2})
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
		Tracked:            3,
		Positioned:         2,
		NearestDist:        1.5,
		NearestCallsign:    "KLM1023",
		FarthestDist:       42.0,
		FarthestCallsign:   csFarthest,
		HighestAlt:         38000,
		HighestCallsign:    csHighest,
		FramesPerSec:       12.3,
		FramesPerSecRecord: 28.7,
		TotalFrames:        999,
		RecoveredFrames:    17,
		CallsignsDecoded:   88,
		CallsignsApplied:   77,
	})

	for _, want := range []string{
		"1.5 nm  [gray]KLM1023[white]",
		"42.0 nm  [gray]BAW123[white]",
		"38000 ft  [gray]DLH456[white]",
		"Frames/s[::-]   12.3  [gray](peak 28.7)[white]",
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

// TestFormatStatsTextWithRecords covers the session-record
// appendix branches in FormatStatsText: when FarthestRecordDist
// strictly exceeds the current FarthestDist (and likewise for
// altitude), the line gains a dimmed "(<record> <callsign>)"
// tail; when record == current the appendix is suppressed to
// avoid showing the same value twice.
func TestFormatStatsTextWithRecords(t *testing.T) {
	t.Parallel()

	got := ui.FormatStatsText(ui.StatsRender{
		Tracked:                2,
		Positioned:             2,
		NearestDist:            5.0,
		NearestCallsign:        "NEAR1",
		FarthestDist:           42.0,
		FarthestCallsign:       csFarthest,
		HighestAlt:             38000,
		HighestCallsign:        csHighest,
		FarthestRecordDist:     186.9,
		FarthestRecordCallsign: csFarthestRecord,
		HighestRecordAlt:       45000,
		HighestRecordCallsign:  csHighestRecord,
	})

	for _, want := range []string{
		"42.0 nm  [gray]BAW123[white]  [gray](186.9 nm KLM999)[white]",
		"38000 ft  [gray]DLH456[white]  [gray](45000 ft QFA8)[white]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q; full:\n%s", want, got)
		}
	}
}

// TestFormatStatsTextRecordAlwaysShownWhenSet pins the
// "record always visible" contract: once a record is set, the
// dimmed (value callsign) appendix appears on every render —
// even when the current value equals the record. Earlier code
// suppressed the duplicate case, which produced a UX where the
// record appeared to "vanish" the moment a new plane matched
// or exceeded the prior peak; users reported this as a bug.
func TestFormatStatsTextRecordAlwaysShownWhenSet(t *testing.T) {
	t.Parallel()

	got := ui.FormatStatsText(ui.StatsRender{
		Tracked:                1,
		Positioned:             1,
		FarthestDist:           42.0,
		FarthestCallsign:       csFarthest,
		HighestAlt:             38000,
		HighestCallsign:        csHighest,
		FarthestRecordDist:     42.0,
		FarthestRecordCallsign: csFarthest,
		HighestRecordAlt:       38000,
		HighestRecordCallsign:  csHighest,
	})

	if !strings.Contains(got, "(42.0 nm BAW123)") {
		t.Errorf("farthest record appendix must show even when record == current; got:\n%s", got)
	}

	if !strings.Contains(got, "(38000 ft DLH456)") {
		t.Errorf("highest record appendix must show even when record == current; got:\n%s", got)
	}
}

// TestStatsTrackerRecordsMonotonic locks in the contract that
// UpdateRecords only promotes; a lower observation must not pull
// the record back down, and an empty callsign must not poison the
// stored record (the typical "no positioned plane this tick"
// case).
func TestStatsTrackerRecordsMonotonic(t *testing.T) {
	t.Parallel()

	tracker := ui.NewStatsTracker()

	tracker.UpdateRecords(150.0, csFarthestRecord, 40000, csHighestRecord)

	// Lower observation — must not regress.
	tracker.UpdateRecords(50.0, "BAW1", 10000, "DLH2")

	dist, callsign := tracker.FarthestRecord()
	if dist != 150.0 || callsign != csFarthestRecord {
		t.Errorf("FarthestRecord = (%v, %q), want (150.0, KLM999)", dist, callsign)
	}

	alt, hcallsign := tracker.HighestRecord()
	if alt != 40000 || hcallsign != csHighestRecord {
		t.Errorf("HighestRecord = (%v, %q), want (40000, QFA8)", alt, hcallsign)
	}

	// Higher observation but no callsign — must not poison the record.
	tracker.UpdateRecords(999.0, "", 99999, "")

	dist, callsign = tracker.FarthestRecord()
	if dist != 150.0 || callsign != csFarthestRecord {
		t.Errorf("after empty-callsign update FarthestRecord = (%v, %q), want unchanged (150.0, KLM999)",
			dist, callsign)
	}

	alt, hcallsign = tracker.HighestRecord()
	if alt != 40000 || hcallsign != csHighestRecord {
		t.Errorf("after empty-callsign update HighestRecord = (%v, %q), want unchanged (40000, QFA8)", alt, hcallsign)
	}

	// Higher observation with callsign — promotes.
	tracker.UpdateRecords(200.0, "AFR42", 42000, "UAE11")

	dist, callsign = tracker.FarthestRecord()
	if dist != 200.0 || callsign != "AFR42" {
		t.Errorf("FarthestRecord = (%v, %q), want (200.0, AFR42)", dist, callsign)
	}

	alt, hcallsign = tracker.HighestRecord()
	if alt != 42000 || hcallsign != "UAE11" {
		t.Errorf("HighestRecord = (%v, %q), want (42000, UAE11)", alt, hcallsign)
	}
}

// TestFramesPerSecRecordMonotonic confirms the session-peak
// frames/sec only ever grows: feed in a high rate, then a lower
// rate, and the peak must still report the high water mark.
func TestFramesPerSecRecordMonotonic(t *testing.T) {
	t.Parallel()

	tracker := ui.NewStatsTracker()

	// Two ticks separated by ~10 ms so elapsed is comfortably
	// positive on every host.
	time.Sleep(10 * time.Millisecond)
	tracker.Sample(adsb.Stats{TotalFrames: 1000})

	peakAfterFirst := tracker.FramesPerSecRecord()
	if peakAfterFirst <= 0 {
		t.Fatalf("first Sample should promote the peak; got %v", peakAfterFirst)
	}

	// A long-ish window with the same total → near-zero rate. The
	// peak must not regress.
	time.Sleep(20 * time.Millisecond)
	tracker.Sample(adsb.Stats{TotalFrames: 1000})

	if got := tracker.FramesPerSecRecord(); got != peakAfterFirst {
		t.Errorf("FramesPerSecRecord after lower-rate Sample = %v, want unchanged %v", got, peakAfterFirst)
	}
}

// TestFormatStatsTextHidesPeakWhenZero covers the "no peak yet"
// branch in FormatStatsText: when FramesPerSecRecord is zero the
// peak appendix must be omitted so the line doesn't render a
// confusing "peak 0.0" before any samples land.
func TestFormatStatsTextHidesPeakWhenZero(t *testing.T) {
	t.Parallel()

	got := ui.FormatStatsText(ui.StatsRender{
		Tracked:            1,
		Positioned:         0,
		FramesPerSec:       4.2,
		FramesPerSecRecord: 0,
	})

	if strings.Contains(got, "peak") {
		t.Errorf("zero peak should suppress the appendix; got:\n%s", got)
	}

	if !strings.Contains(got, "Frames/s[::-]   4.2") {
		t.Errorf("Frames/s value missing or formatted unexpectedly; got:\n%s", got)
	}
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

// TestAggregateStatsRecordPersistsAfterPrune reproduces the
// exact user-reported bug: two planes are tracked; the farther
// one sets the session record; then the record-holder is pruned
// out of the plane list. The record must keep showing in the
// next AggregateStats call.
func TestAggregateStatsRecordPersistsAfterPrune(t *testing.T) {
	t.Parallel()

	myLocation := location.New(location.WithLatitude(52.0), location.WithLongitude(4.0))

	planeList := airplanes.New()
	planeList.Ensure("KLM123")
	planeList.Ensure("BAW042")

	klm, _ := planeList.Get("KLM123")
	klm.Update(
		airplane.WithCallsign("KLM123"),
		airplane.WithLatitude(54.0),
		airplane.WithLongitude(5.0),
		airplane.WithAltitude(30000),
	)

	baw, _ := planeList.Get("BAW042")
	baw.Update(
		airplane.WithCallsign("BAW042"),
		airplane.WithLatitude(56.0),
		airplane.WithLongitude(8.0),
		airplane.WithAltitude(38000),
	)

	tracker := ui.NewStatsTracker()
	stream := adsb.New()

	// Tick 1: both planes present. BAW042 is the farthest, so the
	// record gets set to BAW042's distance.
	render1 := ui.AggregateStats(stream, tracker, myLocation, planeList)

	if render1.FarthestRecordCallsign != "BAW042" {
		t.Fatalf("tick 1: FarthestRecordCallsign = %q, want BAW042 (record should be the farthest plane)",
			render1.FarthestRecordCallsign)
	}

	recordedDist := render1.FarthestRecordDist

	// Prune BAW042 with an immediate-cutoff threshold: any plane
	// whose lastUpdate is before "now plus 1h" is removed, which
	// covers both planes. Re-add only KLM123 to mimic "BAW042 is
	// no longer in the list above".
	planeList.Prune(-time.Hour) // negative threshold => cutoff in the future => prunes everything
	planeList.Ensure("KLM123")

	klm2, _ := planeList.Get("KLM123")
	klm2.Update(
		airplane.WithCallsign("KLM123"),
		airplane.WithLatitude(54.0),
		airplane.WithLongitude(5.0),
		airplane.WithAltitude(30000),
	)

	// Tick 2: only KLM123 is tracked. The record must still point
	// at BAW042 because StatsTracker holds it independently of
	// the plane list.
	render2 := ui.AggregateStats(stream, tracker, myLocation, planeList)

	if render2.FarthestRecordDist != recordedDist {
		t.Errorf("tick 2: FarthestRecordDist = %v, want %v (record must survive prune)",
			render2.FarthestRecordDist, recordedDist)
	}

	if render2.FarthestRecordCallsign != "BAW042" {
		t.Errorf("tick 2: FarthestRecordCallsign = %q, want BAW042 (record callsign must survive prune)",
			render2.FarthestRecordCallsign)
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
