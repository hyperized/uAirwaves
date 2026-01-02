package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/adsb"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplanes"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/battery"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/gps"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/location"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/radar"
)

func main() {
	var (
		ctx, cancel   = context.WithCancel(context.Background())
		app           = tview.NewApplication()
		errChan       = make(chan error, 10)
		myLocation    = location.New()
		wg            sync.WaitGroup
		planeList     = airplanes.New()
		batteryStatus = battery.NewStatus()
	)

	slog.Info("Setting up TUI")
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

	radarPanel := radar.New(planeList, myLocation)

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

	//// Middle Row
	grid.AddItem(radarPanel, 1, 0, 1, 1, 0, 0, false)
	grid.AddItem(planeListPanel, 1, 1, 1, 1, 0, 0, true) // ADSB on the right

	// The bottom Row spans both columns
	grid.AddItem(footer, 2, 0, 1, 2, 0, 0, false)

	slog.Debug("Battery monitoring")
	wg.Add(1)
	go func() {
		defer wg.Done()

		if err := battery.Watch(ctx, batteryStatus); err != nil {
			errChan <- err
		}
	}()

	slog.Debug("GPS service")
	wg.Add(1)
	go func() {
		defer wg.Done()

		if err := gps.New().Watch(ctx, myLocation); err != nil {
			errChan <- err
		}
	}()

	slog.Debug("ADSB service")
	wg.Add(1)
	go func() {
		defer wg.Done()

		if err := adsb.New().Stream(ctx, planeList); err != nil {
			errChan <- err
		}

	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for { // Added loop
			select {
			case appErr := <-errChan:
				slog.Error("Loop caught error: ", slog.Any("error", appErr))
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				app.QueueUpdateDraw(func() {
					// Header
					clock.SetText("Local: " + time.Now().Format(time.TimeOnly) + " UTC: " + time.Now().UTC().Format(time.TimeOnly))
					statusBar.SetText("Battery: " + batteryStatus.String())

					planeListPanel.Clear()
					latitude, longitude := myLocation.GetCoordinates()
					for _, p := range planeList.Sorted(latitude, longitude) {
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

					// Update footer commands
					commands.SetText(fmt.Sprintf("[::b]Range (+/-): %0.0f nm - [::b]Trails (t): %t - [::b]Autoscope (a): %t",
						radarPanel.GetScopeRange(),
						radarPanel.GetTrailsEnabled(),
						radarPanel.GetAutoScopeEnabled(),
					))

					gpsStatus.SetText(fmt.Sprintf("GPS: %s", myLocation.String()))
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
			if currentRange < 200 { // max 200 nautical miles
				radarPanel.SetScopeRange(currentRange + 20)
			}
		case '-': // Decrease scope range
			currentRange := radarPanel.GetScopeRange()
			if currentRange > 20 { // min 20 nautical miles
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

	err := app.SetRoot(grid, true).EnableMouse(true).Run()
	if err != nil {
		slog.Error("tview error", slog.Any("error", err))
	}

	slog.Debug("Issuing context cancellation")
	cancel()
	slog.Debug("Waiting for goroutines to finish")
	wg.Wait()

	slog.Info("Well, that was some experience...")
	slog.Info("Now just let me adjust the spacial controls...")
	slog.Info("And we'll move to another observation point.")

	os.Exit(0)
}

func isEmergencySquawk(squawk []byte) bool {
	squawkStr := string(squawk)
	return squawkStr == "7500" || squawkStr == "7600" || squawkStr == "7700"
}
