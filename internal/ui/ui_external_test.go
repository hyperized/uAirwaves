package ui_test

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/hyperized/uAirwaves/internal/ui"
	"github.com/hyperized/uAirwaves/pkg/adsb"
	"github.com/hyperized/uAirwaves/pkg/airplane"
	"github.com/hyperized/uAirwaves/pkg/airplanes"
	"github.com/hyperized/uAirwaves/pkg/location"
	"github.com/hyperized/uAirwaves/pkg/radar"
	"github.com/rivo/tview"
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
	icaoFixtureA = "AAA001"
	icaoFixtureB = "BBB002"
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
		wantText   tcell.Color
	}{
		{name: "red at exact threshold", percentage: ui.BatteryWarningRed, wantBG: ui.ActiveTheme().HeaderCriticalBackground, wantText: ui.ActiveTheme().HeaderCriticalText},
		{name: "red below threshold", percentage: 5, wantBG: ui.ActiveTheme().HeaderCriticalBackground, wantText: ui.ActiveTheme().HeaderCriticalText},
		{name: "orange at exact threshold", percentage: ui.BatteryWarningOrange, wantBG: ui.ActiveTheme().HeaderWarningBackground, wantText: ui.ActiveTheme().HeaderWarningText},
		{name: "orange between red and orange", percentage: 30, wantBG: ui.ActiveTheme().HeaderWarningBackground, wantText: ui.ActiveTheme().HeaderWarningText},
		{name: "green above orange threshold", percentage: 80, wantBG: ui.ActiveTheme().HeaderOKBackground, wantText: ui.ActiveTheme().HeaderOKText},
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

			// tview.TextView exposes no GetTextColor, so the text half of the
			// pair was never covered — and it was the half that was wrong.
			gotBG, gotText := ui.HeaderColors(testCase.percentage)
			if gotBG != testCase.wantBG {
				t.Errorf("HeaderColors(%d) background = %v, want %v",
					testCase.percentage, gotBG, testCase.wantBG)
			}

			if gotText != testCase.wantText {
				t.Errorf("HeaderColors(%d) text = %v, want %v",
					testCase.percentage, gotText, testCase.wantText)
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
	incrementScope, decrementScope, autoScope, heading, trail, heat, airport int
}

func (r *fakeRadar) IncrementScope()         { r.incrementScope++ }
func (r *fakeRadar) DecrementScope()         { r.decrementScope++ }
func (r *fakeRadar) ToggleAutoScope()        { r.autoScope++ }
func (r *fakeRadar) ToggleHeadingIndicator() { r.heading++ }
func (r *fakeRadar) CycleTrailMode()         { r.trail++ }
func (r *fakeRadar) ToggleHeatIndicator()    { r.heat++ }
func (r *fakeRadar) ToggleAirportIndicator() { r.airport++ }

// fakeNotifs implements NotificationController and counts the
// dismiss-front and dismiss-all calls the dispatcher fires.
type fakeNotifs struct {
	dismissFronts int
	dismissAll    int
}

func (f *fakeNotifs) DismissFront() { f.dismissFronts++ }
func (f *fakeNotifs) DismissAll()   { f.dismissAll++ }

// fakeBias implements BiasTeeController and counts ToggleBiasTee
// invocations so the dispatcher table can assert the right key
// fired.
type fakeBias struct {
	toggles int
}

func (f *fakeBias) ToggleBiasTee() { f.toggles++ }

// fakeCoverage implements CoverageController and counts CycleCoverage
// invocations so the dispatch table can assert the 'c' key fired.
type fakeCoverage struct {
	cycles int
}

func (f *fakeCoverage) CycleCoverage() { f.cycles++ }

// fakeBiasReader implements ui.BiasTeeReader for footer tests.
// supportedVal toggles whether the Bias-T segment renders at all;
// enabledVal drives the on/off text when it does.
type fakeBiasReader struct {
	supportedVal bool
	enabledVal   bool
}

func (f fakeBiasReader) BiasTeeState() (bool, bool) { return f.supportedVal, f.enabledVal }

// keyDispatchCase pins one row of the HandleKeyInput dispatch
// table. Each int is the expected per-method call count for the
// matching fakeRadar field; wantAppStops counts fakeApp.Stop.
type keyDispatchCase struct {
	name             string
	event            *tcell.EventKey
	wantAppStops     int
	wantInc          int
	wantDec          int
	wantAutoScope    int
	wantHeading      int
	wantTrail        int
	wantHeat         int
	wantAirport      int
	wantDismissFront int
	wantDismissAll   int
	wantBiasToggles  int
	wantCoverage     int
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
		name: "l toggles airport overlay", event: tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModNone),
		wantAirport: 1,
	},
	{name: "q stops app", event: tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone), wantAppStops: 1},
	{
		name: "x dismisses front notification", event: tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone),
		wantDismissFront: 1,
	},
	{
		name: "X dismisses all notifications", event: tcell.NewEventKey(tcell.KeyRune, 'X', tcell.ModNone),
		wantDismissAll: 1,
	},
	{
		name: "b toggles bias-tee", event: tcell.NewEventKey(tcell.KeyRune, 'b', tcell.ModNone),
		wantBiasToggles: 1,
	},
	{
		name: "c cycles coverage", event: tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModNone),
		wantCoverage: 1,
	},
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
	nts := &fakeNotifs{}
	bia := &fakeBias{}
	cov := &fakeCoverage{}

	returned := ui.HandleKeyInput(
		testCase.event,
		ui.NewKeyControllers(app, rdr, nil, nts, bia, nil, nil, cov),
	)
	if returned != testCase.event {
		t.Errorf("HandleKeyInput should return event unchanged; got %v want %v", returned, testCase.event)
	}

	got := keyDispatchCase{
		wantAppStops:     app.stops,
		wantInc:          rdr.incrementScope,
		wantDec:          rdr.decrementScope,
		wantAutoScope:    rdr.autoScope,
		wantHeading:      rdr.heading,
		wantTrail:        rdr.trail,
		wantHeat:         rdr.heat,
		wantAirport:      rdr.airport,
		wantDismissFront: nts.dismissFronts,
		wantDismissAll:   nts.dismissAll,
		wantBiasToggles:  bia.toggles,
		wantCoverage:     cov.cycles,
	}

	if got != (keyDispatchCase{
		wantAppStops:     testCase.wantAppStops,
		wantInc:          testCase.wantInc,
		wantDec:          testCase.wantDec,
		wantAutoScope:    testCase.wantAutoScope,
		wantHeading:      testCase.wantHeading,
		wantTrail:        testCase.wantTrail,
		wantHeat:         testCase.wantHeat,
		wantAirport:      testCase.wantAirport,
		wantDismissFront: testCase.wantDismissFront,
		wantDismissAll:   testCase.wantDismissAll,
		wantBiasToggles:  testCase.wantBiasToggles,
		wantCoverage:     testCase.wantCoverage,
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
	planeList.Ensure(icaoFixtureA)
	planeList.Ensure(icaoFixtureB)

	for _, icao := range []string{icaoFixtureA, icaoFixtureB} {
		plane, _ := planeList.Get(icao)
		plane.Update(
			airplane.WithLatitude(52.1),
			airplane.WithLongitude(4.1),
		)
	}

	panel := tview.NewList()
	panel.AddItem("stale", "remains until Clear", 0, nil)

	selection := ui.NewSelection()
	ui.UpdatePlaneList(panel, myLocation, planeList, selection)

	if got := panel.GetItemCount(); got != 2 {
		t.Errorf("panel.GetItemCount = %d, want 2 (stale entry should be cleared)", got)
	}

	// Selection should carry an index→ICAO mapping with both planes
	// after the rebuild, so a global Enter dispatch can resolve the
	// current row to a plane.
	selection.OpenAt(0)

	if selection.ICAO() == "" {
		t.Error("OpenAt(0) after rebuild produced empty ICAO; index map not fed into selection")
	}
}

// TestUpdatePlaneListDropsPositionless pins the always-positioned
// contract: position-less contacts (lat/lon == 0) are never shown
// in the sidebar list. The previous "positioned-only filter" was
// removed; this test guards the behaviour that replaced it.
func TestUpdatePlaneListDropsPositionless(t *testing.T) {
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

	panel := tview.NewList()
	ui.UpdatePlaneList(panel, myLocation, planeList, nil)

	if got := panel.GetItemCount(); got != 1 {
		t.Errorf("panel.GetItemCount = %d, want 1 (position-less must always be filtered)", got)
	}
}

// TestUpdatePlaneListPreservesCursorIndex pins the index-stable
// cursor contract: the tview.List current item stays at the same
// row index across rebuilds, even when planes re-sort. Following
// the plane (the prior shape) caused the cursor to bounce up and
// down the list as distances changed, so the operator preference
// is "cursor doesn't move on its own".
func TestUpdatePlaneListPreservesCursorIndex(t *testing.T) {
	t.Parallel()

	myLocation := location.New(location.WithLatitude(rxLat), location.WithLongitude(rxLon))
	planeList := airplanes.New()

	planeList.Ensure(icaoFixtureA)
	planeList.Ensure(icaoFixtureB)

	planeA, _ := planeList.Get(icaoFixtureA)
	planeA.Update(airplane.WithLatitude(rxLat+0.01), airplane.WithLongitude(rxLon))

	planeB, _ := planeList.Get(icaoFixtureB)
	planeB.Update(airplane.WithLatitude(rxLat+1.0), airplane.WithLongitude(rxLon))

	panel := tview.NewList()
	selection := ui.NewSelection()

	ui.UpdatePlaneList(panel, myLocation, planeList, selection)
	panel.SetCurrentItem(1)

	// Re-sort: A moves further than B, so the order flips. The
	// cursor should remain on index 1, NOT follow plane B to its
	// new position.
	planeA.Update(airplane.WithLatitude(rxLat+5.0), airplane.WithLongitude(rxLon))
	ui.UpdatePlaneList(panel, myLocation, planeList, selection)

	if got := panel.GetCurrentItem(); got != 1 {
		t.Errorf("cursor index after re-sort = %d, want 1 (index should be stable, not follow ICAO)", got)
	}
}

// TestUpdatePlaneListClampsCursorWhenListShrinks pins the clamp:
// if a list shrinks, the cursor at the old (now-past-end) index
// should snap to the new last row, not stay parked off-list.
func TestUpdatePlaneListClampsCursorWhenListShrinks(t *testing.T) {
	t.Parallel()

	myLocation := location.New(location.WithLatitude(rxLat), location.WithLongitude(rxLon))
	planeList := airplanes.New()

	planeList.Ensure(icaoFixtureA)
	planeList.Ensure(icaoFixtureB)

	planeA, _ := planeList.Get(icaoFixtureA)
	planeA.Update(airplane.WithLatitude(rxLat+0.01), airplane.WithLongitude(rxLon))

	planeB, _ := planeList.Get(icaoFixtureB)
	planeB.Update(airplane.WithLatitude(rxLat+1.0), airplane.WithLongitude(rxLon))

	panel := tview.NewList()
	selection := ui.NewSelection()

	ui.UpdatePlaneList(panel, myLocation, planeList, selection)
	panel.SetCurrentItem(1)

	// Plane B loses its position (lat/lon → 0); the always-positioned
	// filter drops it on the next rebuild and the list shrinks to 1.
	planeB.Update(airplane.WithLatitude(0), airplane.WithLongitude(0))

	ui.UpdatePlaneList(panel, myLocation, planeList, selection)

	if got := panel.GetItemCount(); got != 1 {
		t.Fatalf("expected filtered list to shrink to 1, got %d items", got)
	}

	if got := panel.GetCurrentItem(); got != 0 {
		t.Errorf("cursor index after shrink = %d, want 0 (clamped to new last row)", got)
	}
}

// TestUpdateFooterReadsRadarState wires a real radar.View into
// the side-effecting UpdateFooter and asserts the resulting
// command-text contains the expected scope range. Exists to keep
// footerStateFromRadar in coverage.
func TestUpdateFooterReadsRadarState(t *testing.T) {
	t.Parallel()

	planeList := airplanes.New()
	myLocation := location.New()
	radarPanel := radar.New(planeList, myLocation, nil)
	radarPanel.SetScopeRange(25)

	commands := tview.NewTextView()

	ui.UpdateFooter(commands, radarPanel, fakeBiasReader{}, ui.CoverageShadows)

	got := commands.GetText(true)
	if !strings.Contains(got, "Range (+/-): 25 nm") {
		t.Errorf("UpdateFooter text = %q, want substring 'Range (+/-): 25 nm'", got)
	}

	if !strings.Contains(got, "Airports (l): true") {
		t.Errorf("UpdateFooter text = %q, want substring 'Airports (l): true'", got)
	}

	if !strings.Contains(got, "Coverage (c): shadows") {
		t.Errorf("UpdateFooter text = %q, want substring 'Coverage (c): shadows'", got)
	}

	if strings.Contains(got, "Bias-T") {
		t.Errorf("UpdateFooter text = %q, want no 'Bias-T' segment for an unsupported reader", got)
	}
}

// TestUpdateFooterReadsBiasTeeState exercises the supported
// branches of footerStateFromRadar — both on and off — through the
// cached BiasTeeState read.
func TestUpdateFooterReadsBiasTeeState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		reader fakeBiasReader
		want   string
	}{
		{name: "supported on", reader: fakeBiasReader{supportedVal: true, enabledVal: true}, want: "Bias-T (b): on"},
		{name: "supported off", reader: fakeBiasReader{supportedVal: true, enabledVal: false}, want: "Bias-T (b): off"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			planeList := airplanes.New()
			myLocation := location.New()
			radarPanel := radar.New(planeList, myLocation, nil)

			commands := tview.NewTextView()
			ui.UpdateFooter(commands, radarPanel, testCase.reader, ui.CoverageCone)

			if got := commands.GetText(true); !strings.Contains(got, testCase.want) {
				t.Errorf("UpdateFooter text = %q, want substring %q", got, testCase.want)
			}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input uint64
		want  string
	}{
		{"zero", 0, "0 B"},
		{"bytes", 512, "512 B"},
		{"kib boundary", 1024, "1.0 KiB"},
		{"kib", 2_560, "2.5 KiB"},
		{"mib boundary", 1024 * 1024, "1.0 MiB"},
		{"mib", 1024*1024*3 + 1024*512, "3.5 MiB"},
		{"gib boundary", 1024 * 1024 * 1024, "1.0 GiB"},
		{"gib", 1024*1024*1024*4 + 1024*1024*256, "4.2 GiB"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := ui.FormatBytes(testCase.input); got != testCase.want {
				t.Errorf("FormatBytes(%d) = %q, want %q", testCase.input, got, testCase.want)
			}
		})
	}
}

func TestFormatSourceText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		info adsb.SourceInfo
		want string
	}{
		{
			name: "empty label connected with bytes",
			info: adsb.SourceInfo{Label: "", Connected: true, BytesIn: 1024},
			want: "Source: unknown " + ui.ConnectedTag() + "●[-] 1.0 KiB",
		},
		{
			name: "sdr connected no bytes",
			info: adsb.SourceInfo{Label: "SDR", Connected: true, BytesIn: 0},
			want: "Source: SDR " + ui.ConnectedTag() + "●[-]",
		},
		{
			name: "beast disconnected",
			info: adsb.SourceInfo{Label: "BEAST host:30005", Connected: false, BytesIn: 0},
			want: "Source: BEAST host:30005 " + ui.DisconnectedTag() + "●[-]",
		},
		{
			name: "beast connected with bytes",
			info: adsb.SourceInfo{Label: "BEAST host:30005", Connected: true, BytesIn: 5_242_880},
			want: "Source: BEAST host:30005 " + ui.ConnectedTag() + "●[-] 5.0 MiB",
		},
		{
			name: "replay connected",
			info: adsb.SourceInfo{Label: "Replay capture.iq", Connected: true, BytesIn: 0},
			want: "Source: Replay capture.iq " + ui.ConnectedTag() + "●[-]",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := ui.FormatSourceText(testCase.info); got != testCase.want {
				t.Errorf("FormatSourceText(%+v) = %q, want %q", testCase.info, got, testCase.want)
			}
		})
	}
}

func TestUpdateSourceStatusWritesText(t *testing.T) {
	t.Parallel()

	stream := adsb.New(adsb.WithSourceLabel("SDR"))
	panel := tview.NewTextView()
	ui.UpdateSourceStatus(panel, stream)

	if got := panel.GetText(true); !strings.Contains(got, "Source: SDR") {
		t.Errorf("UpdateSourceStatus text missing 'Source: SDR'; got %q", got)
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
// Three rows pin every bias-tee outcome: segment absent when
// unsupported, on, and off.
func TestFormatFooter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state ui.FooterState
		want  string
	}{
		{
			name: "bias-tee unsupported omits the segment, coverage cone",
			state: ui.FooterState{
				ScopeRange:       50,
				HeadingEnabled:   true,
				TrailMode:        radar.TrailOff,
				HeatEnabled:      true,
				AutoScopeEnabled: false,
				AirportsEnabled:  true,
				Coverage:         ui.CoverageCone,
			},
			want: "[::b]Range (+/-): 50 nm - [::b]Heading (h): true - " +
				"[::b]Trail (t): off - [::b]Heat (m): true - [::b]Autoscope (a): false - " +
				"[::b]Airports (l): true - [::b]Coverage (c): cone",
		},
		{
			name: "bias-tee on, coverage shadows",
			state: ui.FooterState{
				TrailMode:        radar.TrailShort,
				Coverage:         ui.CoverageShadows,
				BiasTeeSupported: true,
				BiasTeeEnabled:   true,
			},
			want: "[::b]Range (+/-): 0 nm - [::b]Heading (h): false - " +
				"[::b]Trail (t): short - [::b]Heat (m): false - [::b]Autoscope (a): false - " +
				"[::b]Airports (l): false - [::b]Coverage (c): shadows - [::b]Bias-T (b): on",
		},
		{
			name: "bias-tee off, coverage off",
			state: ui.FooterState{
				TrailMode:        radar.TrailLong,
				Coverage:         ui.CoverageOff,
				BiasTeeSupported: true,
				BiasTeeEnabled:   false,
			},
			want: "[::b]Range (+/-): 0 nm - [::b]Heading (h): false - " +
				"[::b]Trail (t): long - [::b]Heat (m): false - [::b]Autoscope (a): false - " +
				"[::b]Airports (l): false - [::b]Coverage (c): off - [::b]Bias-T (b): off",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := ui.FormatFooter(testCase.state); got != testCase.want {
				t.Errorf("FormatFooter mismatch\ngot:  %q\nwant: %q", got, testCase.want)
			}
		})
	}
}

// fakeSelection records the SelectionController calls
// HandleKeyInput fires on Enter and Esc.
type fakeSelection struct {
	openVal    bool
	openIndex  int
	openCalls  int
	closeCalls int
}

func (f *fakeSelection) IsOpen() bool { return f.openVal }
func (f *fakeSelection) Close()       { f.closeCalls++ }
func (f *fakeSelection) OpenAt(index int) {
	f.openIndex = index
	f.openCalls++
}

// fakePlaneList implements PlaneListController; HandleKeyInput
// calls GetCurrentItem to resolve the highlighted row for Enter.
type fakePlaneList struct {
	current int
}

func (f *fakePlaneList) GetCurrentItem() int { return f.current }

// TestHandleKeyInputEscClosesDetailsWhenOpen pins the contract
// that Esc dismisses the flight-details panel without quitting
// the app when the panel is currently shown.
func TestHandleKeyInputEscClosesDetailsWhenOpen(t *testing.T) {
	t.Parallel()

	app := &fakeApp{}
	rdr := &fakeRadar{}
	nts := &fakeNotifs{}
	bia := &fakeBias{}
	sel := &fakeSelection{openVal: true}
	lst := &fakePlaneList{}

	event := tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone)

	ui.HandleKeyInput(event, ui.NewKeyControllers(app, rdr, nil, nts, bia, sel, lst, nil))

	if app.stops != 0 {
		t.Errorf("Esc with details open should NOT stop the app, stops = %d", app.stops)
	}

	if sel.closeCalls != 1 {
		t.Errorf("Esc with details open should call Selection.Close once, got %d", sel.closeCalls)
	}
}

// TestHandleKeyInputEscQuitsWhenDetailsClosed pins the converse:
// Esc with the details panel hidden still quits the app, so the
// legacy quit shortcut keeps working.
func TestHandleKeyInputEscQuitsWhenDetailsClosed(t *testing.T) {
	t.Parallel()

	app := &fakeApp{}
	rdr := &fakeRadar{}
	nts := &fakeNotifs{}
	bia := &fakeBias{}
	sel := &fakeSelection{openVal: false}
	lst := &fakePlaneList{}

	event := tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone)

	ui.HandleKeyInput(event, ui.NewKeyControllers(app, rdr, nil, nts, bia, sel, lst, nil))

	if app.stops != 1 {
		t.Errorf("Esc with details closed should stop the app, stops = %d", app.stops)
	}

	if sel.closeCalls != 0 {
		t.Errorf("Esc with details closed should not call Close, got %d", sel.closeCalls)
	}
}

// fakeMiniRadar implements MiniRadarController for the scope-key
// redirect tests. Counts the three method calls the dispatcher
// fires so a single assertion confirms which target served the
// keystroke.
type fakeMiniRadar struct {
	increment, decrement, autoScope int
}

func (f *fakeMiniRadar) IncrementScope()  { f.increment++ }
func (f *fakeMiniRadar) DecrementScope()  { f.decrement++ }
func (f *fakeMiniRadar) ToggleAutoScope() { f.autoScope++ }

// TestHandleKeyInputScopeKeysRoute pins the conditional redirect
// of +/-/a between the main radar and the mini view. When the
// flight-details panel is open the mini radar wins; otherwise
// the main radar still handles the keys. The table covers both
// branches so a future logic flip is caught immediately.
func TestHandleKeyInputScopeKeysRoute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		selectionOpen    bool
		wantMainScope    int
		wantMiniScope    int
		failOnMainOpen   string
		failOnMiniClosed string
	}{
		{
			name:           "details open routes scope keys to mini",
			selectionOpen:  true,
			wantMiniScope:  1,
			failOnMainOpen: "main radar should NOT receive scope keys when details open",
		},
		{
			name:             "details closed routes scope keys to main",
			selectionOpen:    false,
			wantMainScope:    1,
			failOnMiniClosed: "mini radar should NOT receive scope keys when details closed",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			rdr, mini := assertScopeRouting(t, testCase.selectionOpen)

			if testCase.wantMainScope == 0 && (rdr.incrementScope+rdr.decrementScope+rdr.autoScope) != 0 {
				t.Errorf("%s; got inc=%d dec=%d auto=%d", testCase.failOnMainOpen,
					rdr.incrementScope, rdr.decrementScope, rdr.autoScope)
			}

			if testCase.wantMiniScope == 0 && (mini.increment+mini.decrement+mini.autoScope) != 0 {
				t.Errorf("%s; got inc=%d dec=%d auto=%d", testCase.failOnMiniClosed,
					mini.increment, mini.decrement, mini.autoScope)
			}
		})
	}
}

// assertScopeRouting fires +/-/a through HandleKeyInput against a
// freshly-constructed controller set with the supplied selection
// state and returns the recorders for further assertion. Pulled
// out so the table case body stays under the wsl_v5 statement
// limit and the duplication linter doesn't flag two near-
// identical drivers.
func assertScopeRouting(t *testing.T, selectionOpen bool) (*fakeRadar, *fakeMiniRadar) {
	t.Helper()

	app := &fakeApp{}
	rdr := &fakeRadar{}
	mini := &fakeMiniRadar{}
	nts := &fakeNotifs{}
	bia := &fakeBias{}
	sel := &fakeSelection{openVal: selectionOpen}
	lst := &fakePlaneList{}

	ctrls := ui.NewKeyControllers(app, rdr, mini, nts, bia, sel, lst, nil)

	for _, pressed := range []rune{'+', '-', 'a'} {
		ui.HandleKeyInput(tcell.NewEventKey(tcell.KeyRune, pressed, tcell.ModNone), ctrls)
	}

	return rdr, mini
}

// TestHandleKeyInputEnterOpensSelection pins the Enter →
// Selection.OpenAt(currentItem) wiring.
func TestHandleKeyInputEnterOpensSelection(t *testing.T) {
	t.Parallel()

	app := &fakeApp{}
	rdr := &fakeRadar{}
	nts := &fakeNotifs{}
	bia := &fakeBias{}
	sel := &fakeSelection{}
	lst := &fakePlaneList{current: 3}

	event := tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)

	ui.HandleKeyInput(event, ui.NewKeyControllers(app, rdr, nil, nts, bia, sel, lst, nil))

	if sel.openCalls != 1 {
		t.Errorf("Enter should call Selection.OpenAt once, got %d", sel.openCalls)
	}

	if sel.openIndex != 3 {
		t.Errorf("Enter should open at current list index 3, got %d", sel.openIndex)
	}
}
