package ui

import (
	"fmt"
	"math"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/adsb"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplane"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplanes"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/location"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/radar"
)

// Battery threshold percentages used by UpdateHeaderColor.
// Anything ≤ BatteryWarningRed switches the header to red on
// white; ≤ BatteryWarningOrange goes to orange on black; above
// stays at dark-green on black.
const (
	BatteryWarningRed    = 20
	BatteryWarningOrange = 40
)

// UpdateHeaderColor switches the clock and statusBar background
// to the colour scheme matching the current battery percentage.
// Pulled out of main.go so it can be table-driven tested with
// fake tview TextViews — no harness required.
func UpdateHeaderColor(percentage int8, clock, statusBar *tview.TextView) {
	headerColor, textColor := headerColors(percentage)

	clock.SetBackgroundColor(headerColor)
	clock.SetTextColor(textColor)
	statusBar.SetBackgroundColor(headerColor)
	statusBar.SetTextColor(textColor)
}

// headerColors picks the (background, text) pair for a given
// battery percentage. Pure function, exported through
// UpdateHeaderColor — keeps the colour-decision logic isolated
// for table-driven tests.
//
//nolint:nonamedreturns // (header, text) reads clearer named at this signature.
func headerColors(percentage int8) (header, text tcell.Color) {
	switch {
	case percentage <= BatteryWarningRed:
		return tcell.ColorRed, tcell.ColorWhite
	case percentage <= BatteryWarningOrange:
		return tcell.ColorOrange, tcell.ColorBlack
	default:
		return tcell.ColorDarkGreen, tcell.ColorBlack
	}
}

// AppController abstracts the tview Application methods
// HandleKeyInput depends on, so tests can exercise dispatch
// without spinning up a real Application.
type AppController interface {
	Stop()
}

// RadarController abstracts the radar.View methods
// HandleKeyInput dispatches to. Tests inject a fake recorder to
// observe which dispatch path each key took.
type RadarController interface {
	IncrementScope()
	DecrementScope()
	ToggleAutoScope()
	ToggleHeadingIndicator()
	CycleTrailMode()
	ToggleHeatIndicator()
	ToggleAirportIndicator()
}

// NotificationController abstracts the notification queue's
// dismiss surface. Used by the key dispatcher to pop the front
// message on 'x' / clear all on 'X'. The full *Notifications
// type satisfies this interface; tests can pass a fake recorder.
type NotificationController interface {
	DismissFront()
	DismissAll()
}

// BiasTeeController abstracts the bias-tee toggle the key
// dispatcher fires on 'b'. The implementation in main.go reads
// the live chip state via adsb.BiasTeeEnabled, flips it via
// adsb.SetBiasTee, and surfaces failures as slog.Warn so the
// notification bar renders them. Unsupported sources (BEAST,
// replay) should log a one-line warn and otherwise no-op.
type BiasTeeController interface {
	ToggleBiasTee()
}

// SelectionController abstracts the flight-details selection
// state HandleKeyInput consults on Esc (to dismiss the details
// panel instead of quitting the app) and on Enter (to open the
// currently-highlighted plane). The full *Selection satisfies
// this interface; tests can pass a fake recorder.
type SelectionController interface {
	IsOpen() bool
	Close()
	OpenAt(index int)
}

// PlaneListController abstracts the read of the plane list's
// "currently highlighted row" so HandleKeyInput can resolve
// Enter to a Selection.OpenAt call without depending on the full
// tview.List.
type PlaneListController interface {
	GetCurrentItem() int
}

// MiniRadarController is the minimal scope-control surface the
// flight-details mini-radar exposes. When the details panel is
// open the dispatcher rebinds the scope-mutating keys (+, -, a)
// from the main radar to the mini view so the operator can zoom
// the per-plane scope without leaving the details panel.
type MiniRadarController interface {
	IncrementScope()
	DecrementScope()
	ToggleAutoScope()
}

// KeyControllers bundles every dependency HandleKeyInput needs
// so the function signature stays inside revive's argument-count
// limit. Cheap to construct at the call site via
// NewKeyControllers.
type KeyControllers struct {
	App       AppController
	Radar     RadarController
	MiniRadar MiniRadarController
	Notifs    NotificationController
	BiasTee   BiasTeeController
	Selection SelectionController
	PlaneList PlaneListController
}

// HandleKeyInput is the global key dispatcher: Esc/q stop the
// app, +/- adjust scope, a/h/t/m toggle indicators, p toggles
// the sidebar positioned-only filter, x/X dismiss the current /
// all queued notifications. Enter opens the flight-details panel
// for the highlighted plane; Esc closes it without quitting the
// app when it is open. The event is returned unchanged so
// tview's input chain can pass it on to the focused widget.
//
// Lifted out of main.go behind the AppController /
// RadarController / PlaneFilterController /
// NotificationController interfaces so the dispatch table is
// testable without a tview event loop.
func HandleKeyInput(event *tcell.EventKey, ctrls KeyControllers) *tcell.EventKey {
	if event.Key() == tcell.KeyEsc {
		if ctrls.Selection != nil && ctrls.Selection.IsOpen() {
			ctrls.Selection.Close()

			return event
		}

		ctrls.App.Stop()
	}

	if event.Key() == tcell.KeyEnter && ctrls.Selection != nil && ctrls.PlaneList != nil {
		ctrls.Selection.OpenAt(ctrls.PlaneList.GetCurrentItem())
	}

	dispatchRune(event.Rune(), ctrls)

	return event
}

// NewKeyControllers bundles the controllers HandleKeyInput needs
// into one value. Exists so main.go's SetInputCapture closure
// reads cleanly and so tests can construct a controller set with
// their own fakes.
func NewKeyControllers(
	app AppController,
	radarPanel RadarController,
	miniRadar MiniRadarController,
	notifs NotificationController,
	biasTee BiasTeeController,
	selection SelectionController,
	planeList PlaneListController,
) KeyControllers {
	return KeyControllers{
		App:       app,
		Radar:     radarPanel,
		MiniRadar: miniRadar,
		Notifs:    notifs,
		BiasTee:   biasTee,
		Selection: selection,
		PlaneList: planeList,
	}
}

// dispatchRune is a strategy-table dispatcher keyed on the
// pressed rune. Split out of HandleKeyInput so the switch stays
// small enough for revive's cyclomatic-complexity gate.
//
// When the flight-details panel is open the scope-mutating keys
// (+/-, a) route to the mini view instead of the main radar so
// the operator can zoom the per-plane scope without backing out
// of the panel.
func dispatchRune(pressed rune, ctrls KeyControllers) {
	if dispatchScopeRune(pressed, ctrls) {
		return
	}

	switch pressed {
	case 'h':
		ctrls.Radar.ToggleHeadingIndicator()
	case 't':
		ctrls.Radar.CycleTrailMode()
	case 'm':
		ctrls.Radar.ToggleHeatIndicator()
	case 'l':
		ctrls.Radar.ToggleAirportIndicator()
	case 'q':
		ctrls.App.Stop()
	case 'x':
		ctrls.Notifs.DismissFront()
	case 'X':
		ctrls.Notifs.DismissAll()
	case 'b':
		ctrls.BiasTee.ToggleBiasTee()
	default:
		// No-op for unrecognised keys; event still bubbles up.
	}
}

// dispatchScopeRune handles the three keys whose target switches
// between the main radar and the mini view depending on whether
// the details panel is open. Returns true when the rune was
// recognised so dispatchRune can short-circuit and skip the
// generic switch.
func dispatchScopeRune(pressed rune, ctrls KeyControllers) bool {
	target := scopeTargetFor(ctrls)

	switch pressed {
	case '+':
		target.IncrementScope()
	case '-':
		target.DecrementScope()
	case 'a':
		target.ToggleAutoScope()
	default:
		return false
	}

	return true
}

// scopeTargetFor picks which scope controller the +/-/a keys
// should drive. When a selection is open and a MiniRadar
// controller is wired up, the mini view wins; otherwise the
// main radar handles the key. The main radar's full
// RadarController already satisfies MiniRadarController via the
// shared three-method subset, so the return type unifies cleanly.
//
//nolint:ireturn // interface return is intentional: callers dispatch on the unified scope-control surface.
func scopeTargetFor(ctrls KeyControllers) MiniRadarController {
	if ctrls.MiniRadar != nil && ctrls.Selection != nil && ctrls.Selection.IsOpen() {
		return ctrls.MiniRadar
	}

	return ctrls.Radar
}

// UpdatePlaneList rewrites the right-column plane list panel
// from the current airplanes list, sorted by distance from
// myLocation. Emergency squawks are red-highlighted; planes
// without a resolved position are always skipped — the
// position-less shadow contacts are noise the operator never
// wants to see in this view.
//
// selection (may be nil) is fed the parallel index→ICAO slice so
// the global key dispatcher (Enter) can resolve the currently
// highlighted row back to a plane.
//
// Cursor stability: the tview.List.Clear() call resets the
// current-item index to 0, which loses the operator's arrow-key
// progress every tick. We snapshot the cursor index *before*
// Clear() and restore it after rebuild (clamped to the new
// length). This keeps the cursor at the same visual position
// even when the underlying plane order churns — the alternative
// (track the plane's ICAO and follow it) caused the cursor to
// jump up and down the list as planes re-sorted by distance.
func UpdatePlaneList(
	planeListPanel *tview.List, myLocation *location.Location, planeList *airplanes.Airplanes,
	selection *Selection,
) {
	prevCursor := planeListPanel.GetCurrentItem()

	planeListPanel.Clear()

	latitude, longitude := myLocation.GetCoordinates()

	sorted := planeList.Sorted(latitude, longitude)
	icaos := make([]string, 0, len(sorted))

	for _, snap := range sorted {
		if snap.Latitude == 0 || snap.Longitude == 0 {
			continue
		}

		icaos = append(icaos, snap.ICAO)

		mainText, secondaryText := FormatPlaneListEntry(snap, snap.Summary(), latitude, longitude)
		planeListPanel.AddItem(mainText, secondaryText, 0, nil)
	}

	if selection != nil {
		selection.SetICAOs(icaos)
	}

	restorePlaneListCursor(planeListPanel, prevCursor)
}

// restorePlaneListCursor re-anchors the tview.List's current
// item to the same index it was on before the rebuild, clamped
// to the new length so a shrinking list doesn't park the cursor
// past the last row. An empty list is a no-op.
func restorePlaneListCursor(planeListPanel *tview.List, prevCursor int) {
	count := planeListPanel.GetItemCount()
	if count == 0 {
		return
	}

	target := prevCursor
	if target >= count {
		target = count - 1
	}

	if target < 0 {
		target = 0
	}

	planeListPanel.SetCurrentItem(target)
}

// FormatPlaneListEntry formats a single plane row. Exposed so
// the formatting (and the emergency / no-position branches) is
// testable without a tview.List.
//
// summary is the plane's String() result (altitude / heading /
// velocity / age) — passed in rather than recomputed so tests
// can drive both inputs from pure data.
//
//nolint:nonamedreturns // (mainText, secondaryText) reads clearer named at this signature.
func FormatPlaneListEntry(
	snap airplane.Snapshot, summary string, latitude, longitude float64,
) (mainText, secondaryText string) {
	ident := snap.ICAO
	if snap.Callsign != "" {
		ident = fmt.Sprintf("%s/%s", snap.Callsign, snap.ICAO)
	}

	sqk := ""
	if snap.Squawk != "" {
		sqk = " squawk " + snap.Squawk
	}

	mainText = ident + sqk
	if snap.Emergency {
		mainText = fmt.Sprintf("[red]%s%s (!)[white]", ident, sqk)
	}

	distance := airplanes.HaversineDistance(latitude, longitude, snap.Latitude, snap.Longitude)

	secondaryText = fmt.Sprintf("%.1fnm %s", distance, summary)
	if distance == math.MaxFloat64 {
		secondaryText = summary
	}

	return mainText, secondaryText
}

// UpdateSourceStatus writes the formatted source-line into
// the header's source TextView. Thin SetText wrapper so
// FormatSourceText is independently testable.
func UpdateSourceStatus(sourceStatus *tview.TextView, stream *adsb.ADSB) {
	sourceStatus.SetText(FormatSourceText(stream.Source()))
}

// FormatSourceText renders the data-source line shown in the
// header. Label is the source identifier ("SDR", "BEAST host:port",
// "Replay file"); a coloured dot signals connection state; the
// running byte count is shown only when bytes have actually been
// pulled (so SDR and replay stay terse).
//
// Colour spans close with [-] (tview's reset-to-default sentinel)
// rather than [white] so the byte suffix inherits the TextView's
// configured text colour — the source pill sits on a dark-green
// background where white text is unreadable.
func FormatSourceText(info adsb.SourceInfo) string {
	label := info.Label
	if label == "" {
		label = "unknown"
	}

	state := "[red]●[-]"
	if info.Connected {
		state = "[green]●[-]"
	}

	if info.BytesIn > 0 {
		return fmt.Sprintf("Source: %s %s %s", label, state, FormatBytes(info.BytesIn))
	}

	return fmt.Sprintf("Source: %s %s", label, state)
}

// FormatBytes renders a byte count using base-1024 units (B / KiB
// / MiB / GiB). One decimal place above the unit boundary, exact
// otherwise. Sized for header use, not log lines.
func FormatBytes(bytes uint64) string {
	const (
		kib = 1024
		mib = kib * 1024
		gib = mib * 1024
	)

	switch {
	case bytes >= gib:
		return fmt.Sprintf("%.1f GiB", float64(bytes)/gib)
	case bytes >= mib:
		return fmt.Sprintf("%.1f MiB", float64(bytes)/mib)
	case bytes >= kib:
		return fmt.Sprintf("%.1f KiB", float64(bytes)/kib)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// UpdateStatsPanel refreshes the stats panel with the latest
// ingest counters and plane-list-derived aggregates. Thin
// wrapper around AggregateStats + FormatStatsText so the side
// effect (SetText) is the only thing main.go cares about; the
// pure halves are independently tested.
func UpdateStatsPanel(
	statsPanel *tview.TextView,
	stream *adsb.ADSB,
	tracker *StatsTracker,
	myLocation *location.Location,
	planeList *airplanes.Airplanes,
) {
	statsPanel.SetText(FormatStatsText(AggregateStats(stream, tracker, myLocation, planeList)))
}

// BiasTeeReader is the read-only slice of *adsb.ADSB the footer
// needs: whether the active source supports bias-tee, and the
// live chip state when it does. Hoisted so UpdateFooter is
// testable with a stub.
type BiasTeeReader interface {
	BiasTeeSupported() bool
	BiasTeeEnabled() (bool, error)
}

// UpdateFooter rewrites the footer command/status line. Pulled
// out to internal/ui so the footer string format is testable
// against a fake radar source.
func UpdateFooter(commands *tview.TextView, radarPanel *radar.View, biasTee BiasTeeReader) {
	commands.SetText(FormatFooter(footerStateFromRadar(radarPanel, biasTee)))
}

// FooterState is the snapshot of UI settings the footer line
// summarises. The pure formatter takes this struct so test code
// can render every combination without driving a real radar.
// AircraftCount is intentionally absent — the Stats panel
// already shows a richer "Tracked / Positioned" pair, so a
// duplicate count in the footer was just noise.
type FooterState struct {
	ScopeRange       float64
	HeadingEnabled   bool
	TrailMode        radar.TrailMode
	HeatEnabled      bool
	AutoScopeEnabled bool
	AirportsEnabled  bool
	BiasTeeSupported bool
	BiasTeeEnabled   bool
}

// FormatFooter renders a FooterState into the tview-coloured
// command line shown across the bottom of the UI.
func FormatFooter(state FooterState) string {
	return fmt.Sprintf(
		"[::b]Range (+/-): %0.0f nm - [::b]Heading (h): %t - "+
			"[::b]Trail (t): %s - [::b]Heat (m): %t - [::b]Autoscope (a): %t - "+
			"[::b]Airports (l): %t - [::b]Bias-T (b): %s",
		state.ScopeRange,
		state.HeadingEnabled,
		state.TrailMode,
		state.HeatEnabled,
		state.AutoScopeEnabled,
		state.AirportsEnabled,
		formatBiasTee(state),
	)
}

// formatBiasTee renders the bias-tee footer cell. Sources without
// a controllable bias-tee (BEAST, replay) show n/a so the operator
// knows the toggle is inert; the local-SDR path shows the live
// on/off bit read from the chip.
func formatBiasTee(state FooterState) string {
	switch {
	case !state.BiasTeeSupported:
		return "n/a"
	case state.BiasTeeEnabled:
		return "on"
	default:
		return "off"
	}
}

// readBiasTeeState polls the BiasTeeReader for the live chip state.
// A read error while supported=true is suppressed (footer falls
// back to "off") — the next tick will either recover or the stream
// will exit and flip supported=false on its own.
//
//nolint:nonamedreturns // (supported, enabled) reads clearer named at this signature.
func readBiasTeeState(biasTee BiasTeeReader) (supported, enabled bool) {
	supported = biasTee.BiasTeeSupported()
	if !supported {
		return false, false
	}

	got, err := biasTee.BiasTeeEnabled()
	if err != nil {
		return true, false
	}

	return true, got
}

func footerStateFromRadar(radarPanel *radar.View, biasTee BiasTeeReader) FooterState {
	supported, enabled := readBiasTeeState(biasTee)

	return FooterState{
		ScopeRange:       radarPanel.GetScopeRange(),
		HeadingEnabled:   radarPanel.GetHeadingIndicatorEnabled(),
		TrailMode:        radarPanel.GetTrailMode(),
		HeatEnabled:      radarPanel.GetHeatIndicatorEnabled(),
		AutoScopeEnabled: radarPanel.GetAutoScopeEnabled(),
		AirportsEnabled:  radarPanel.GetAirportIndicatorEnabled(),
		BiasTeeSupported: supported,
		BiasTeeEnabled:   enabled,
	}
}
