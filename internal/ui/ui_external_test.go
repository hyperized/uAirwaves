package ui_test

import (
	"errors"
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

// errSyntheticBiasRead is the static sentinel for the
// "BiasTeeReader.BiasTeeEnabled errored" branch in
// footerStateFromRadar; err113 forbids ad-hoc errors.New in test
// bodies because identity-based assertions are fragile.
var errSyntheticBiasRead = errors.New("synthetic bias-tee read failure")

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

// fakeBiasReader implements ui.BiasTeeReader for footer tests.
// supportedVal toggles the n/a branch; enabledVal drives the
// on/off render; readErr exercises the "supported but read
// failed" fallback (footer should treat it as off).
type fakeBiasReader struct {
	supportedVal bool
	enabledVal   bool
	readErr      error
}

func (f fakeBiasReader) BiasTeeSupported() bool { return f.supportedVal }

func (f fakeBiasReader) BiasTeeEnabled() (bool, error) {
	return f.enabledVal, f.readErr
}

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
	wantDismissFront  int
	wantDismissAll    int
	wantBiasToggles   int
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
	nts := &fakeNotifs{}
	bia := &fakeBias{}

	returned := ui.HandleKeyInput(testCase.event, ui.NewKeyControllers(app, rdr, flt, nts, bia))
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
		wantDismissFront:  nts.dismissFronts,
		wantDismissAll:    nts.dismissAll,
		wantBiasToggles:   bia.toggles,
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
		wantDismissFront:  testCase.wantDismissFront,
		wantDismissAll:    testCase.wantDismissAll,
		wantBiasToggles:   testCase.wantBiasToggles,
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
	radarPanel := radar.New(planeList, myLocation, nil)
	radarPanel.SetScopeRange(25)

	commands := tview.NewTextView()

	ui.UpdateFooter(commands, radarPanel, fakeBiasReader{}, true)

	got := commands.GetText(true)
	if !strings.Contains(got, "Range (+/-): 25 nm") {
		t.Errorf("UpdateFooter text = %q, want substring 'Range (+/-): 25 nm'", got)
	}

	if !strings.Contains(got, "Positioned (p): true") {
		t.Errorf("UpdateFooter text = %q, want substring 'Positioned (p): true'", got)
	}

	if !strings.Contains(got, "Bias-T (b): n/a") {
		t.Errorf("UpdateFooter text = %q, want substring 'Bias-T (b): n/a' (unsupported reader)", got)
	}
}

// TestUpdateFooterReadsBiasTeeState exercises the supported
// branches of footerStateFromRadar — both on and off. The reader's
// readErr path drives the "supported but read failed" fallback,
// which footerStateFromRadar treats as off.
func TestUpdateFooterReadsBiasTeeState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		reader fakeBiasReader
		want   string
	}{
		{name: "supported on", reader: fakeBiasReader{supportedVal: true, enabledVal: true}, want: "Bias-T (b): on"},
		{name: "supported off", reader: fakeBiasReader{supportedVal: true, enabledVal: false}, want: "Bias-T (b): off"},
		{
			name: "supported but read failed",
			reader: fakeBiasReader{
				supportedVal: true,
				enabledVal:   true, // ignored when readErr != nil
				readErr:      errSyntheticBiasRead,
			},
			want: "Bias-T (b): off",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			planeList := airplanes.New()
			myLocation := location.New()
			radarPanel := radar.New(planeList, myLocation, nil)

			commands := tview.NewTextView()
			ui.UpdateFooter(commands, radarPanel, testCase.reader, false)

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
			want: "Source: unknown [green]●[white] 1.0 KiB",
		},
		{
			name: "sdr connected no bytes",
			info: adsb.SourceInfo{Label: "SDR", Connected: true, BytesIn: 0},
			want: "Source: SDR [green]●[white]",
		},
		{
			name: "beast disconnected",
			info: adsb.SourceInfo{Label: "BEAST host:30005", Connected: false, BytesIn: 0},
			want: "Source: BEAST host:30005 [red]●[white]",
		},
		{
			name: "beast connected with bytes",
			info: adsb.SourceInfo{Label: "BEAST host:30005", Connected: true, BytesIn: 5_242_880},
			want: "Source: BEAST host:30005 [green]●[white] 5.0 MiB",
		},
		{
			name: "replay connected",
			info: adsb.SourceInfo{Label: "Replay capture.iq", Connected: true, BytesIn: 0},
			want: "Source: Replay capture.iq [green]●[white]",
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
// Three rows so every bias-tee branch (on / off / n/a) is pinned.
func TestFormatFooter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state ui.FooterState
		want  string
	}{
		{
			name: "bias-tee unsupported",
			state: ui.FooterState{
				AircraftCount:    7,
				ScopeRange:       50,
				HeadingEnabled:   true,
				TrailEnabled:     false,
				HeatEnabled:      true,
				AutoScopeEnabled: false,
				PositionedOnly:   true,
			},
			want: "[::b]Tracking: 7 - [::b]Range (+/-): 50 nm - [::b]Heading (h): true - " +
				"[::b]Trail (t): false - [::b]Heat (m): true - [::b]Autoscope (a): false - " +
				"[::b]Positioned (p): true - [::b]Bias-T (b): n/a",
		},
		{
			name: "bias-tee on",
			state: ui.FooterState{
				BiasTeeSupported: true,
				BiasTeeEnabled:   true,
			},
			want: "[::b]Tracking: 0 - [::b]Range (+/-): 0 nm - [::b]Heading (h): false - " +
				"[::b]Trail (t): false - [::b]Heat (m): false - [::b]Autoscope (a): false - " +
				"[::b]Positioned (p): false - [::b]Bias-T (b): on",
		},
		{
			name: "bias-tee off",
			state: ui.FooterState{
				BiasTeeSupported: true,
				BiasTeeEnabled:   false,
			},
			want: "[::b]Tracking: 0 - [::b]Range (+/-): 0 nm - [::b]Heading (h): false - " +
				"[::b]Trail (t): false - [::b]Heat (m): false - [::b]Autoscope (a): false - " +
				"[::b]Positioned (p): false - [::b]Bias-T (b): off",
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
