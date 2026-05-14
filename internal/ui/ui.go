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

// HandleKeyInput is the global key dispatcher: Esc/q stop the
// app, +/- adjust scope, a/h/t/m toggle indicators. The event
// is returned unchanged so tview's input chain can pass it on
// to the focused widget.
//
// Lifted out of main.go behind the AppController /
// RadarController interfaces so the dispatch table is testable
// without a tview event loop.
func HandleKeyInput(event *tcell.EventKey, app AppController, radarPanel RadarController) *tcell.EventKey {
	if event.Key() == tcell.KeyEsc {
		app.Stop()
	}

	dispatchRune(event.Rune(), app, radarPanel)

	return event
}

// dispatchRune is a strategy-table dispatcher keyed on the
// pressed rune. Split out of HandleKeyInput so the switch stays
// small enough for revive's cyclomatic-complexity gate.
func dispatchRune(pressed rune, app AppController, radarPanel RadarController) {
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
// line.
func UpdatePlaneList(planeListPanel *tview.List, myLocation *location.Location, planeList *airplanes.Airplanes) {
	planeListPanel.Clear()

	latitude, longitude := myLocation.GetCoordinates()

	for _, snap := range planeList.Sorted(latitude, longitude) {
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
func UpdateFooter(commands *tview.TextView, radarPanel *radar.View) {
	commands.SetText(FormatFooter(footerStateFromRadar(radarPanel)))
}

// FooterState is the snapshot of radar settings the footer line
// summarises. The pure formatter takes this struct so test code
// can render every combination without driving a real radar.
type FooterState struct {
	AircraftCount    int
	ScopeRange       float64
	HeadingEnabled   bool
	TrailEnabled     bool
	HeatEnabled      bool
	AutoScopeEnabled bool
}

// FormatFooter renders a FooterState into the tview-coloured
// command line shown across the bottom of the UI.
func FormatFooter(state FooterState) string {
	return fmt.Sprintf(
		"[::b]Tracking: %d - [::b]Range (+/-): %0.0f nm - [::b]Heading (h): %t - "+
			"[::b]Trail (t): %t - [::b]Heat (m): %t - [::b]Autoscope (a): %t",
		state.AircraftCount,
		state.ScopeRange,
		state.HeadingEnabled,
		state.TrailEnabled,
		state.HeatEnabled,
		state.AutoScopeEnabled,
	)
}

func footerStateFromRadar(radarPanel *radar.View) FooterState {
	return FooterState{
		AircraftCount:    radarPanel.GetAircraftCount(),
		ScopeRange:       radarPanel.GetScopeRange(),
		HeadingEnabled:   radarPanel.GetHeadingIndicatorEnabled(),
		TrailEnabled:     radarPanel.GetTrailIndicatorEnabled(),
		HeatEnabled:      radarPanel.GetHeatIndicatorEnabled(),
		AutoScopeEnabled: radarPanel.GetAutoScopeEnabled(),
	}
}
