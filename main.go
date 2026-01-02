package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/data/adsb"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/data/battery"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/data/gps"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/radar"
)

func main() {
	var (
		ctx      = context.Background()
		app      = tview.NewApplication()
		errChan  = make(chan error, 1)
		location = gps.NewLocation()
	)

	clock := tview.NewTextView().
		SetTextAlign(tview.AlignLeft).
		SetText("..:..:..")
	clock.SetDynamicColors(true)
	clock.SetBackgroundColor(tcell.ColorDarkBlue)

	statusBar := tview.NewTextView().
		SetTextAlign(tview.AlignRight).
		SetText("loading...")
	statusBar.SetDynamicColors(true)
	statusBar.SetBackgroundColor(tcell.ColorDarkBlue)

	headerPanel := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(clock, 0, 1, false).
		AddItem(statusBar, 0, 1, false)

	radarPanel := radar.NewView()

	planeListPanel := tview.NewList().
		ShowSecondaryText(true)
	planeListPanel.SetBorder(true).
		SetTitle("Airplanes").SetTitleColor(tcell.ColorGreen)

	commands := tview.NewTextView().SetTextAlign(tview.AlignLeft)
	commands.SetDynamicColors(true)
	commands.SetBackgroundColor(tcell.ColorDarkBlue)

	gpsStatus := tview.NewTextView().SetTextAlign(tview.AlignRight)
	gpsStatus.SetDynamicColors(true)
	gpsStatus.SetBackgroundColor(tcell.ColorDarkBlue)

	footer := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(commands, 0, 1, false).
		AddItem(gpsStatus, 0, 1, false)

	grid := tview.NewGrid().
		SetRows(1, 0, 1).
		SetColumns(0, 50).
		SetBorders(false)

	// Top Row spans both columns
	grid.AddItem(headerPanel, 0, 0, 1, 2, 0, 0, false)

	// Middle Row
	grid.AddItem(radarPanel, 1, 0, 1, 1, 0, 0, false)
	grid.AddItem(planeListPanel, 1, 1, 1, 1, 0, 0, true) // ADSB on the right

	// The bottom Row spans both columns
	grid.AddItem(footer, 2, 0, 1, 2, 0, 0, false)

	// Battery service
	slog.Info("Monitoring battery")
	b := battery.New()
	go b.Watch(ctx, errChan)

	// GPS service
	slog.Info("GPS service")
	g := gps.New()
	defer g.Stop(ctx)
	go g.Watch(ctx, location, errChan)

	// ADSB service
	slog.Info("ADSB service")
	a := adsb.New()
	conErr := a.Connect(ctx)
	if conErr != nil {
		log.Fatal(conErr) // Cannot proceed without this.
		return
	}
	defer a.Stop(ctx)
	go a.Stream(ctx, errChan)

	slog.Info("Start renderer")
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for { // Added loop
			select {
			case appErr := <-errChan:
				app.Stop()
				slog.Error("Loop caught error: ", slog.Any("error", appErr))
			case <-ctx.Done():
				return
			case <-ticker.C:
				app.QueueUpdateDraw(func() {
					// Header
					clock.SetText("Local: " + time.Now().Format(time.TimeOnly) + " UTC: " + time.Now().UTC().Format(time.TimeOnly))
					statusBar.SetText("Battery: " + b.Display())

					planeListPanel.Clear()
					latitude, longitude := location.GetCoordinates()
					for _, p := range a.Planes().Sorted(latitude, longitude) {
						plane := p.GetSnapshot()

						ident := fmt.Sprintf("%s", plane.ICAO)
						if plane.Callsign != "" {
							ident = fmt.Sprintf("%s/%s", plane.Callsign, plane.ICAO)
						}

						sqk := ""
						if plane.Squawk != nil {
							sqk = fmt.Sprintf(" squawk %s", plane.Squawk)
						}

						// Format the main text with heading arrow and emergency highlighting
						mainText := fmt.Sprintf("%s%s", ident, sqk)
						if isEmergencySquawk(plane.Squawk) {
							mainText = fmt.Sprintf("[red]%s%s (!)[white]", ident, sqk)
						}

						planeListPanel.AddItem(mainText, p.String(), 0, nil)
					}

					radarPanel.SetCenter(latitude, longitude)
					radarPanel.SetPlanes(a.Planes().Sorted(latitude, longitude))

					// Update footer commands
					commands.SetText(fmt.Sprintf("[::b]Range (+/-): %0.0f nm - [::b]Trails (t): %t - [::b]Autoscope (a): %t",
						radarPanel.GetScopeRange(),
						radarPanel.GetTrailsEnabled(),
						radarPanel.GetAutoScopeEnabled(),
					))

					gpsStatus.SetText(fmt.Sprintf("GPS: %s", location.String()))
				})
			}
		}
	}()

	// Input capture for global shortcuts
	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc {
			app.Stop()
		}
		switch event.Rune() {
		case '+': // Increase scope range
			currentRange := radarPanel.GetScopeRange()
			if currentRange < 200 { // Max 200 nautical miles
				radarPanel.SetScopeRange(currentRange + 20)
			}
		case '-': // Decrease scope range
			currentRange := radarPanel.GetScopeRange()
			if currentRange > 20 { // Min 20 nautical miles
				radarPanel.SetScopeRange(currentRange - 20)
			}
		case 'a':
			radarPanel.ToggleAutoScope()
		case 't': // Toggle trails
			radarPanel.ToggleTrails()
		case 'q': // Quit
			app.Stop()
		}
		return event
	})

	if err := app.SetRoot(grid, true).EnableMouse(true).Run(); err != nil {
		app.Stop()
		log.Fatal(err)
	}
}

func isEmergencySquawk(squawk []byte) bool {
	squawkStr := string(squawk)
	return squawkStr == "7500" || squawkStr == "7600" || squawkStr == "7700"
}
