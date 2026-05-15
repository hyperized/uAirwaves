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
	ToggleTrailIndicator()
	ToggleHeatIndicator()
}

// PlaneFilterController abstracts the sidebar-filter toggle.
// Separate from RadarController because the sidebar list is not
// radar state — the filter only affects the right-column plane
// list, not the radar render.
type PlaneFilterController interface {
	TogglePositionedOnly()
}

// HandleKeyInput is the global key dispatcher: Esc/q stop the
// app, +/- adjust scope, a/h/t/m toggle indicators, p toggles
// the sidebar positioned-only filter. The event is returned
// unchanged so tview's input chain can pass it on to the focused
// widget.
//
// Lifted out of main.go behind the AppController /
// RadarController / PlaneFilterController interfaces so the
// dispatch table is testable without a tview event loop.
func HandleKeyInput(
	event *tcell.EventKey, app AppController, radarPanel RadarController, filter PlaneFilterController,
) *tcell.EventKey {
	if event.Key() == tcell.KeyEsc {
		app.Stop()
	}

	dispatchRune(event.Rune(), app, radarPanel, filter)

	return event
}

// dispatchRune is a strategy-table dispatcher keyed on the
// pressed rune. Split out of HandleKeyInput so the switch stays
// small enough for revive's cyclomatic-complexity gate.
func dispatchRune(pressed rune, app AppController, radarPanel RadarController, filter PlaneFilterController) {
	switch pressed {
	case '+':
		radarPanel.IncrementScope()
	case '-':
		radarPanel.DecrementScope()
	case 'a':
		radarPanel.ToggleAutoScope()
	case 'h':
		radarPanel.ToggleHeadingIndicator()
	case 't':
		radarPanel.ToggleTrailIndicator()
	case 'm':
		radarPanel.ToggleHeatIndicator()
	case 'p':
		filter.TogglePositionedOnly()
	case 'q':
		app.Stop()
	default:
		// No-op for unrecognised keys; event still bubbles up.
	}
}

// UpdatePlaneList rewrites the right-column plane list panel
// from the current airplanes list, sorted by distance from
// myLocation. Emergency squawks are red-highlighted; planes
// without a resolved position get a distance-less secondary
// line. When positionedOnly is true, position-less contacts are
// skipped entirely — the default sidebar mode.
//
//nolint:revive // flag-parameter: positionedOnly selects the sidebar's filter mode, not a behaviour switch.
func UpdatePlaneList(
	planeListPanel *tview.List, myLocation *location.Location, planeList *airplanes.Airplanes, positionedOnly bool,
) {
	planeListPanel.Clear()

	latitude, longitude := myLocation.GetCoordinates()

	for _, snap := range planeList.Sorted(latitude, longitude) {
		if positionedOnly && (snap.Latitude == 0 || snap.Longitude == 0) {
			continue
		}

		mainText, secondaryText := FormatPlaneListEntry(snap, snap.Summary(), latitude, longitude)

		planeListPanel.AddItem(mainText, secondaryText, 0, nil)
	}
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
func FormatSourceText(info adsb.SourceInfo) string {
	label := info.Label
	if label == "" {
		label = "unknown"
	}

	state := "[red]●[white]"
	if info.Connected {
		state = "[green]●[white]"
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

// UpdateFooter rewrites the footer command/status line. Pulled
// out to internal/ui so the footer string format is testable
// against a fake radar source.
func UpdateFooter(commands *tview.TextView, radarPanel *radar.View, positionedOnly bool) {
	commands.SetText(FormatFooter(footerStateFromRadar(radarPanel, positionedOnly)))
}

// FooterState is the snapshot of UI settings the footer line
// summarises. The pure formatter takes this struct so test code
// can render every combination without driving a real radar.
type FooterState struct {
	AircraftCount    int
	ScopeRange       float64
	HeadingEnabled   bool
	TrailEnabled     bool
	HeatEnabled      bool
	AutoScopeEnabled bool
	PositionedOnly   bool
}

// FormatFooter renders a FooterState into the tview-coloured
// command line shown across the bottom of the UI.
func FormatFooter(state FooterState) string {
	return fmt.Sprintf(
		"[::b]Tracking: %d - [::b]Range (+/-): %0.0f nm - [::b]Heading (h): %t - "+
			"[::b]Trail (t): %t - [::b]Heat (m): %t - [::b]Autoscope (a): %t - [::b]Positioned (p): %t",
		state.AircraftCount,
		state.ScopeRange,
		state.HeadingEnabled,
		state.TrailEnabled,
		state.HeatEnabled,
		state.AutoScopeEnabled,
		state.PositionedOnly,
	)
}

func footerStateFromRadar(radarPanel *radar.View, positionedOnly bool) FooterState {
	return FooterState{
		AircraftCount:    radarPanel.GetAircraftCount(),
		ScopeRange:       radarPanel.GetScopeRange(),
		HeadingEnabled:   radarPanel.GetHeadingIndicatorEnabled(),
		TrailEnabled:     radarPanel.GetTrailIndicatorEnabled(),
		HeatEnabled:      radarPanel.GetHeatIndicatorEnabled(),
		AutoScopeEnabled: radarPanel.GetAutoScopeEnabled(),
		PositionedOnly:   positionedOnly,
	}
}
