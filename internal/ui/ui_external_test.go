package ui_test

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"lab.hyperized.net/hyperized/uAirwaves/internal/ui"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/adsb"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplane"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplanes"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/location"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/radar"
)

// Repeated test fixtures: kept package-private so the goconst
// linter doesn't keep flagging the same literals across cases.
const (
	testICAO     = "ABCDEF"
	testCallsign = "KLM1023"
	testLat      = 52.1
	testLon      = 4.1
	rxLat        = 52.0
	rxLon        = 4.0
)

// TestUpdateHeaderColorBatteryThresholds drives every branch of
// headerColors via the side-effecting UpdateHeaderColor. The
// background colour is the only piece tview surfaces publicly;
// the text colour lives in an unexported textStyle. Background
// alone is sufficient: the three threshold bands each use a
// distinct background, so wrong text colour would still imply
// wrong band selection.
func TestUpdateHeaderColorBatteryThresholds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		percentage int8
		wantBG     tcell.Color
	}{
		{name: "red at exact threshold", percentage: ui.BatteryWarningRed, wantBG: tcell.ColorRed},
		{name: "red below threshold", percentage: 5, wantBG: tcell.ColorRed},
		{name: "orange at exact threshold", percentage: ui.BatteryWarningOrange, wantBG: tcell.ColorOrange},
		{name: "orange between red and orange", percentage: 30, wantBG: tcell.ColorOrange},
		{name: "green above orange threshold", percentage: 80, wantBG: tcell.ColorDarkGreen},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			clock := tview.NewTextView()
			statusBar := tview.NewTextView()

			ui.UpdateHeaderColor(testCase.percentage, clock, statusBar)

			if got := clock.GetBackgroundColor(); got != testCase.wantBG {
				t.Errorf("clock BG = %v, want %v", got, testCase.wantBG)
			}

			if got := statusBar.GetBackgroundColor(); got != testCase.wantBG {
				t.Errorf("statusBar BG = %v, want %v", got, testCase.wantBG)
			}
		})
	}
}

// fakeApp implements AppController to count Stop calls.
type fakeApp struct {
	stops int
}

func (a *fakeApp) Stop() { a.stops++ }

// fakeRadar implements RadarController and records every method
// call. The boolean flags let tests assert that the right
// dispatch path fired for each key.
type fakeRadar struct {
	incrementScope, decrementScope, autoScope, heading, trail, heat int
}

func (r *fakeRadar) IncrementScope()         { r.incrementScope++ }
func (r *fakeRadar) DecrementScope()         { r.decrementScope++ }
func (r *fakeRadar) ToggleAutoScope()        { r.autoScope++ }
func (r *fakeRadar) ToggleHeadingIndicator() { r.heading++ }
func (r *fakeRadar) ToggleTrailIndicator()   { r.trail++ }
func (r *fakeRadar) ToggleHeatIndicator()    { r.heat++ }

// fakeFilter implements PlaneFilterController and records every
// toggle so the dispatch table can assert the right key fired.
type fakeFilter struct {
	positionedToggles int
}

func (f *fakeFilter) TogglePositionedOnly() { f.positionedToggles++ }

// keyDispatchCase pins one row of the HandleKeyInput dispatch
// table. Each int is the expected per-method call count for the
// matching fakeRadar field; wantAppStops counts fakeApp.Stop.
type keyDispatchCase struct {
	name              string
	event             *tcell.EventKey
	wantAppStops      int
	wantInc           int
	wantDec           int
	wantAutoScope     int
	wantHeading       int
	wantTrail         int
	wantHeat          int
	wantPositionedTog int
}

// keyDispatchCases is the HandleKeyInput dispatch table. Hoisted
// out so the test function stays small enough for revive's
// cognitive-complexity threshold.
//
//nolint:gochecknoglobals // table fixture, read-only for the test.
var keyDispatchCases = []keyDispatchCase{
	{
		name:         "escape stops app",
		event:        tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone),
		wantAppStops: 1,
	},
	{name: "plus increments scope", event: tcell.NewEventKey(tcell.KeyRune, '+', tcell.ModNone), wantInc: 1},
	{name: "minus decrements scope", event: tcell.NewEventKey(tcell.KeyRune, '-', tcell.ModNone), wantDec: 1},
	{name: "a toggles autoScope", event: tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone), wantAutoScope: 1},
	{name: "h toggles heading", event: tcell.NewEventKey(tcell.KeyRune, 'h', tcell.ModNone), wantHeading: 1},
	{name: "t toggles trail", event: tcell.NewEventKey(tcell.KeyRune, 't', tcell.ModNone), wantTrail: 1},
	{name: "m toggles heat", event: tcell.NewEventKey(tcell.KeyRune, 'm', tcell.ModNone), wantHeat: 1},
	{
		name: "p toggles positioned-only filter", event: tcell.NewEventKey(tcell.KeyRune, 'p', tcell.ModNone),
		wantPositionedTog: 1,
	},
	{name: "q stops app", event: tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone), wantAppStops: 1},
	{name: "unrecognised rune is no-op", event: tcell.NewEventKey(tcell.KeyRune, 'z', tcell.ModNone)},
}

// TestHandleKeyInputDispatch covers every documented rune and
// the Esc key path. Each subtest exercises one case so a future
// rebinding shows up as a single-case failure.
func TestHandleKeyInputDispatch(t *testing.T) {
	t.Parallel()

	for _, testCase := range keyDispatchCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			assertKeyDispatch(t, testCase)
		})
	}
}

// assertKeyDispatch runs the per-case assertions for
// HandleKeyInput. Extracted from the test loop so the per-test
// body's branching stays inside revive's cognitive-complexity
// gate.
func assertKeyDispatch(t *testing.T, testCase keyDispatchCase) {
	t.Helper()

	app := &fakeApp{}
	rdr := &fakeRadar{}
	flt := &fakeFilter{}

	returned := ui.HandleKeyInput(testCase.event, app, rdr, flt)
	if returned != testCase.event {
		t.Errorf("HandleKeyInput should return event unchanged; got %v want %v", returned, testCase.event)
	}

	got := keyDispatchCase{
		wantAppStops:      app.stops,
		wantInc:           rdr.incrementScope,
		wantDec:           rdr.decrementScope,
		wantAutoScope:     rdr.autoScope,
		wantHeading:       rdr.heading,
		wantTrail:         rdr.trail,
		wantHeat:          rdr.heat,
		wantPositionedTog: flt.positionedToggles,
	}

	if got != (keyDispatchCase{
		wantAppStops:      testCase.wantAppStops,
		wantInc:           testCase.wantInc,
		wantDec:           testCase.wantDec,
		wantAutoScope:     testCase.wantAutoScope,
		wantHeading:       testCase.wantHeading,
		wantTrail:         testCase.wantTrail,
		wantHeat:          testCase.wantHeat,
		wantPositionedTog: testCase.wantPositionedTog,
	}) {
		t.Errorf("dispatch counts mismatch\n got: %+v\nwant: %+v", got, testCase)
	}
}

// formatPlaneListCase pins one row of the FormatPlaneListEntry
// table. wantSecondaryEq pins the exact secondary text;
// wantSecondaryHas a substring — exactly one of the two is set
// per case.
type formatPlaneListCase struct {
	name             string
	snap             airplane.Snapshot
	summary          string
	wantMain         string
	wantSecondaryEq  string
	wantSecondaryHas string
}

// formatPlaneListCases is the FormatPlaneListEntry table. Hoisted
// out of the test function so the function body stays under
// revive's function-length limit.
//
//nolint:gochecknoglobals // table fixture, read-only for the test.
var formatPlaneListCases = []formatPlaneListCase{
	{
		name: "callsign present, no squawk, regular plane",
		snap: airplane.Snapshot{
			ICAO:      testICAO,
			Callsign:  testCallsign,
			Latitude:  testLat,
			Longitude: testLon,
		},
		summary:          "FL050 90o 250kts",
		wantMain:         testCallsign + "/" + testICAO,
		wantSecondaryHas: "nm FL050 90o 250kts",
	},
	{
		name: "ICAO fallback when callsign empty",
		snap: airplane.Snapshot{
			ICAO:      "BCDEF1",
			Latitude:  testLat,
			Longitude: testLon,
		},
		summary:          "FL100",
		wantMain:         "BCDEF1",
		wantSecondaryHas: "nm FL100",
	},
	{
		name: "squawk appended to main",
		snap: airplane.Snapshot{
			ICAO:      testICAO,
			Callsign:  testCallsign,
			Squawk:    "1234",
			Latitude:  testLat,
			Longitude: testLon,
		},
		summary:          "FL050",
		wantMain:         testCallsign + "/" + testICAO + " squawk 1234",
		wantSecondaryHas: "nm FL050",
	},
	{
		name: "emergency wraps main in red markup",
		snap: airplane.Snapshot{
			ICAO:      testICAO,
			Callsign:  testCallsign,
			Squawk:    "7700",
			Emergency: true,
			Latitude:  testLat,
			Longitude: testLon,
		},
		summary:          "FL050",
		wantMain:         "[red]" + testCallsign + "/" + testICAO + " squawk 7700 (!)[white]",
		wantSecondaryHas: "nm FL050",
	},
	{
		name:            "no position drops distance prefix",
		snap:            airplane.Snapshot{ICAO: testICAO},
		summary:         "FL000 0o 0kts",
		wantMain:        testICAO,
		wantSecondaryEq: "FL000 0o 0kts",
	},
}

// TestFormatPlaneListEntry covers every documented branch of the
// formatter: callsign-vs-ICAO ident, squawk-vs-no-squawk,
// emergency highlight, and the no-position fallback (distance
// == MaxFloat64 hides the "X.Xnm " prefix on the secondary).
func TestFormatPlaneListEntry(t *testing.T) {
	t.Parallel()

	for _, testCase := range formatPlaneListCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			assertFormatPlaneListEntry(t, testCase)
		})
	}
}

// assertFormatPlaneListEntry runs the FormatPlaneListEntry
// assertions for one case. Extracted to keep the table loop body
// (and its cognitive complexity) within revive's threshold.
func assertFormatPlaneListEntry(t *testing.T, testCase formatPlaneListCase) {
	t.Helper()

	main, secondary := ui.FormatPlaneListEntry(testCase.snap, testCase.summary, rxLat, rxLon)

	if main != testCase.wantMain {
		t.Errorf("main = %q, want %q", main, testCase.wantMain)
	}

	if testCase.wantSecondaryEq != "" && secondary != testCase.wantSecondaryEq {
		t.Errorf("secondary = %q, want %q", secondary, testCase.wantSecondaryEq)
	}

	if testCase.wantSecondaryHas != "" && !strings.Contains(secondary, testCase.wantSecondaryHas) {
		t.Errorf("secondary = %q, want substring %q", secondary, testCase.wantSecondaryHas)
	}
}

// TestUpdatePlaneListWritesEntries drives the side-effecting
// wrapper through a small list and asserts the resulting tview
// list has the expected number of rows. The formatting is
// covered by TestFormatPlaneListEntry; this test exists to keep
// the tview.List interaction in coverage.
func TestUpdatePlaneListWritesEntries(t *testing.T) {
	t.Parallel()

	myLocation := location.New(location.WithLatitude(52.0), location.WithLongitude(4.0))

	planeList := airplanes.New()
	planeList.Ensure("AAA001")
	planeList.Ensure("BBB002")

	for _, icao := range []string{"AAA001", "BBB002"} {
		plane, _ := planeList.Get(icao)
		plane.Update(
			airplane.WithLatitude(52.1),
			airplane.WithLongitude(4.1),
		)
	}

	panel := tview.NewList()
	panel.AddItem("stale", "remains until Clear", 0, nil)

	ui.UpdatePlaneList(panel, myLocation, planeList, true)

	if got := panel.GetItemCount(); got != 2 {
		t.Errorf("panel.GetItemCount = %d, want 2 (stale entry should be cleared)", got)
	}
}

// TestUpdatePlaneListFilter exercises the positioned-only filter:
// the position-less contact is dropped when on, included when off.
func TestUpdatePlaneListFilter(t *testing.T) {
	t.Parallel()

	myLocation := location.New(location.WithLatitude(rxLat), location.WithLongitude(rxLon))

	planeList := airplanes.New()
	planeList.Ensure("POSITN")
	planeList.Ensure("NOPOSN")

	positioned, _ := planeList.Get("POSITN")
	positioned.Update(
		airplane.WithLatitude(testLat),
		airplane.WithLongitude(testLon),
	)

	t.Run("positioned-only drops position-less", func(t *testing.T) {
		t.Parallel()

		panel := tview.NewList()
		ui.UpdatePlaneList(panel, myLocation, planeList, true)

		if got := panel.GetItemCount(); got != 1 {
			t.Errorf("panel.GetItemCount = %d, want 1 (position-less should be filtered)", got)
		}
	})

	t.Run("all-mode keeps position-less", func(t *testing.T) {
		t.Parallel()

		panel := tview.NewList()
		ui.UpdatePlaneList(panel, myLocation, planeList, false)

		if got := panel.GetItemCount(); got != 2 {
			t.Errorf("panel.GetItemCount = %d, want 2 (all contacts visible)", got)
		}
	})
}

// TestUpdateFooterReadsRadarState wires a real radar.View into
// the side-effecting UpdateFooter and asserts the resulting
// command-text contains the expected scope range. Exists to keep
// footerStateFromRadar in coverage.
func TestUpdateFooterReadsRadarState(t *testing.T) {
	t.Parallel()

	planeList := airplanes.New()
	myLocation := location.New()
	radarPanel := radar.New(planeList, myLocation)
	radarPanel.SetScopeRange(25)

	commands := tview.NewTextView()

	ui.UpdateFooter(commands, radarPanel, true)

	got := commands.GetText(true)
	if !strings.Contains(got, "Range (+/-): 25 nm") {
		t.Errorf("UpdateFooter text = %q, want substring 'Range (+/-): 25 nm'", got)
	}

	if !strings.Contains(got, "Positioned (p): true") {
		t.Errorf("UpdateFooter text = %q, want substring 'Positioned (p): true'", got)
	}
}

// TestUpdateStatsPanelWritesText drives the side-effecting
// wrapper through one tick. The pure halves (AggregateStats /
// FormatStatsText) are independently tested; this exists to
// cover the SetText edge.
func TestUpdateStatsPanelWritesText(t *testing.T) {
	t.Parallel()

	planeList := airplanes.New()
	planeList.Ensure("ABC001")

	plane, _ := planeList.Get("ABC001")
	plane.Update(
		airplane.WithLatitude(52.1),
		airplane.WithLongitude(4.1),
		airplane.WithAltitude(30000),
	)

	myLocation := location.New(location.WithLatitude(52.0), location.WithLongitude(4.0))
	tracker := ui.NewStatsTracker()
	stream := adsb.New()

	panel := tview.NewTextView()
	ui.UpdateStatsPanel(panel, stream, tracker, myLocation, planeList)

	got := panel.GetText(true)
	if !strings.Contains(got, "Tracked") {
		t.Errorf("UpdateStatsPanel text missing 'Tracked'; got %q", got)
	}
}

// TestFormatFooter pins the exact rendered footer string for a
// fully-populated FooterState — this is the single line shown
// across the bottom of the UI, so any format drift is visible.
func TestFormatFooter(t *testing.T) {
	t.Parallel()

	got := ui.FormatFooter(ui.FooterState{
		AircraftCount:    7,
		ScopeRange:       50,
		HeadingEnabled:   true,
		TrailEnabled:     false,
		HeatEnabled:      true,
		AutoScopeEnabled: false,
		PositionedOnly:   true,
	})

	want := "[::b]Tracking: 7 - [::b]Range (+/-): 50 nm - [::b]Heading (h): true - " +
		"[::b]Trail (t): false - [::b]Heat (m): true - [::b]Autoscope (a): false - [::b]Positioned (p): true"

	if got != want {
		t.Errorf("FormatFooter mismatch\ngot:  %q\nwant: %q", got, want)
	}
}
