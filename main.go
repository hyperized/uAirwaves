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
		ctx, cancel    = context.WithCancel(context.Background())
		app            = tview.NewApplication()
		errChan        = make(chan error, 1)
		myLocation     = location.New()
		waitGroup      sync.WaitGroup
		planeList      = airplanes.New()
		batteryStatus  = battery.NewStatus()
		clock          = configureClock()
		statusBar      = configureStatusbar()
		headerPanel    = configureHeader(clock, statusBar)
		radarPanel     = radar.New(planeList, myLocation)
		planeListPanel = configurePlaneList()
		commands       = configureCommands()
		gpsStatus      = configureGpsStatus()
		footer         = configureFooter(commands, gpsStatus)
		errorLine      = configureErrorLine()
		grid           = configureGrid(headerPanel, radarPanel, planeListPanel, footer, errorLine)
	)

	waitGroup.Add(1)

	go func() {
		defer waitGroup.Done()

		if err := battery.Watch(ctx, batteryStatus); err != nil {
			errChan <- err
		}
	}()

	waitGroup.Add(1)

	go func() {
		defer waitGroup.Done()

		if err := gps.New().Watch(ctx, myLocation); err != nil {
			errChan <- err
		}
	}()

	waitGroup.Add(1)

	go func() {
		defer waitGroup.Done()

		if err := adsb.New().Stream(ctx, planeList); err != nil {
			errChan <- err
		}
	}()

	waitGroup.Add(1)

	go func() {
		defer waitGroup.Done()

		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for { // Added loop
			select {
			case appErr := <-errChan:
				errorLine.SetText(appErr.Error())
			case <-ctx.Done():
				return
			case <-ticker.C:
				app.QueueUpdateDraw(func() {
					// Header
					clock.SetText("Local: " + time.Now().Format(time.TimeOnly) + " UTC: " +
						time.Now().UTC().Format(time.TimeOnly))
					statusBar.SetText("Battery: " + batteryStatus.String())

					updatePlaneList(planeListPanel, myLocation, planeList)

					// Update footer commands
					updateFooter(commands, radarPanel)

					gpsStatus.SetText("GPS: " + myLocation.String())
				})
			}
		}
	}()

	// Input capture for global shortcuts
	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		return handleKeyInput(event, app, radarPanel)
	})

	err := app.SetRoot(grid, true).EnableMouse(true).Run()
	if err != nil {
		slog.Error("tview error", slog.Any("error", err))
	}

	cancel()
	waitGroup.Wait()

	slog.Info("Well, that was some experience...")
	slog.Info("Now just let me adjust the spacial controls...")
	slog.Info("And we'll move to another observation point.")

	os.Exit(0)
}

// configureGrid configures the main grid layout.
func configureGrid(headerPanel *tview.Flex, radarPanel *radar.View, planeListPanel *tview.List, footer *tview.Flex,
	errorLine *tview.TextView) *tview.Grid {
	grid := tview.NewGrid().SetRows(1, 0, 1, 1).SetColumns(0, 50).SetBorders(false) //nolint:mnd
	grid.AddItem(headerPanel, 0, 0, 1, 2, 0, 0, false)
	grid.AddItem(radarPanel, 1, 0, 1, 1, 0, 0, false)
	grid.AddItem(planeListPanel, 1, 1, 1, 1, 0, 0, true)
	grid.AddItem(footer, 2, 0, 1, 2, 0, 0, false)
	grid.AddItem(errorLine, 3, 0, 1, 2, 0, 0, false)

	return grid
}

// configureErrorLine configures the error line text view.
func configureErrorLine() *tview.TextView {
	errorLine := tview.NewTextView().SetTextAlign(tview.AlignLeft).SetText("no errors")
	errorLine.SetDynamicColors(true)
	errorLine.SetBackgroundColor(tcell.ColorRed)

	return errorLine
}

// configureFooter configures the footer panel.
func configureFooter(commands *tview.TextView, gpsStatus *tview.TextView) *tview.Flex {
	return tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(commands, 0, 1, false).
		AddItem(gpsStatus, 0, 1, false)
}

// configureGpsStatus configures the GPS status text view.
func configureGpsStatus() *tview.TextView {
	gpsStatus := tview.NewTextView().SetTextAlign(tview.AlignRight)
	gpsStatus.SetDynamicColors(true)
	gpsStatus.SetBackgroundColor(tcell.ColorDarkBlue)

	return gpsStatus
}

// configureCommands configures the commands text view.
func configureCommands() *tview.TextView {
	commands := tview.NewTextView().SetTextAlign(tview.AlignLeft)
	commands.SetDynamicColors(true)
	commands.SetBackgroundColor(tcell.ColorDarkBlue)

	return commands
}

// configurePlaneList configures the plane list panel.
func configurePlaneList() *tview.List {
	planeListPanel := tview.NewList().ShowSecondaryText(true)
	planeListPanel.SetBorder(true).SetTitle("Airplanes").SetTitleColor(tcell.ColorGreen)

	return planeListPanel
}

// configureHeader configures the header panel.
func configureHeader(clock *tview.TextView, statusBar *tview.TextView) *tview.Flex {
	return tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(clock, 0, 1, false).
		AddItem(statusBar, 0, 1, false)
}

// configureStatusbar configures the status bar text view.
func configureStatusbar() *tview.TextView {
	statusBar := tview.NewTextView().SetTextAlign(tview.AlignRight).SetText("loading...")
	statusBar.SetDynamicColors(true)
	statusBar.SetBackgroundColor(tcell.ColorDarkBlue)

	return statusBar
}

// configureClock configures the clock text view.
func configureClock() *tview.TextView {
	clock := tview.NewTextView().SetTextAlign(tview.AlignLeft).SetText("..:..:..")
	clock.SetDynamicColors(true)
	clock.SetBackgroundColor(tcell.ColorDarkBlue)

	return clock
}

// handleKeyInput handles global shortcuts.
func handleKeyInput(event *tcell.EventKey, app *tview.Application, radarPanel *radar.View) *tcell.EventKey {
	if event.Key() == tcell.KeyEsc {
		app.Stop()
	}

	// Catch action keys
	switch event.Rune() {
	case '+': // Increase scope range
		radarPanel.IncrementScope()
	case '-': // Decrease scope range
		radarPanel.DecrementScope()
	case 'a':
		radarPanel.ToggleAutoScope()
	case 'h': // Toggle heading indicator
		radarPanel.ToggleHeadingIndicator()
	case 'q': // Quit
		app.Stop()
	default:
		// Nothing
	}

	return event
}

// updatePlaneList updates the plane list panel with the current plane list.
func updatePlaneList(planeListPanel *tview.List, myLocation *location.Location, planeList *airplanes.Airplanes) {
	planeListPanel.Clear()

	latitude, longitude := myLocation.GetCoordinates()

	for _, value := range planeList.Sorted(latitude, longitude) {
		plane := value.GetSnapshot()

		ident := plane.ICAO
		if plane.Callsign != "" {
			ident = fmt.Sprintf("%s/%s", plane.Callsign, plane.ICAO)
		}

		sqk := ""
		if plane.Squawk != "" {
			sqk = " squawk " + plane.Squawk
		}

		// Format the main text with heading arrow and emergency highlighting
		mainText := fmt.Sprintf("%s%s", ident, sqk)
		if plane.Emergency {
			mainText = fmt.Sprintf("[red]%s%s (!)[white]", ident, sqk)
		}

		planeListPanel.AddItem(mainText, value.String(), 0, nil)
	}
}

// updateFooter updates the footer text with the current scope range and heading indicator settings.
func updateFooter(commands *tview.TextView, radarPanel *radar.View) *tview.TextView {
	return commands.SetText(fmt.Sprintf(
		"[::b]Range (+/-): %0.0f nm - [::b]Heading indicator (h): %t - [::b]Autoscope (a): %t",
		radarPanel.GetScopeRange(),
		radarPanel.GetHeadingIndicatorEnabled(),
		radarPanel.GetAutoScopeEnabled(),
	))
}
