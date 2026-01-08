package main

import (
	"context"
	"errors"
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

var (
	errBatteryRecover = errors.New("recovered in battery goroutine")
	errGPSRecover     = errors.New("recovered in gps goroutine")
	errASDBRecover    = errors.New("recovered in asdb goroutine")
)

const uiUpdateInterval = 1 * time.Second

func main() {
	uic := configureUI()
	radarPanel := radar.New(uic.planeList, uic.myLocation)
	uic.radarPanel = radarPanel

	grid := configureGrid(uic)

	startBatteryWatcher(uic)
	startGPSWatcher(uic)
	startADSBStreamer(uic)
	startUIUpdater(uic)

	// Input capture for global shortcuts
	uic.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		return handleKeyInput(event, uic.app, uic.radarPanel)
	})

	if err := uic.app.SetRoot(grid, true).EnableMouse(true).Run(); err != nil {
		slog.Error("tview error", slog.Any("error", err))
	}

	uic.cancel()
	uic.waitGroup.Wait()

	slog.Info("Well, that was some experience...")
	slog.Info("Now just let me adjust the spacial controls...")
	slog.Info("And we'll move to another observation point.")

	os.Exit(0)
}

type uiComponents struct {
	ctx            context.Context //nolint:containedctx
	cancel         context.CancelFunc
	app            *tview.Application
	errChan        chan error
	myLocation     *location.Location
	waitGroup      *sync.WaitGroup
	planeList      *airplanes.Airplanes
	batteryStatus  *battery.Status
	clock          *tview.TextView
	statusBar      *tview.TextView
	headerPanel    *tview.Flex
	radarPanel     *radar.View
	planeListPanel *tview.List
	commands       *tview.TextView
	gpsStatus      *tview.TextView
	footer         *tview.Flex
	errorLine      *tview.TextView
}

func configureUI() *uiComponents {
	ctx, cancel := context.WithCancel(context.Background())
	planeList := airplanes.New()
	myLocation := location.New()
	clock := configureClock()
	statusBar := configureStatusbar()
	commands := configureCommands()
	gpsStatus := configureGpsStatus()

	return &uiComponents{
		ctx:            ctx,
		cancel:         cancel,
		app:            tview.NewApplication(),
		errChan:        make(chan error, 1),
		myLocation:     myLocation,
		waitGroup:      &sync.WaitGroup{},
		planeList:      planeList,
		batteryStatus:  battery.NewStatus(),
		clock:          clock,
		statusBar:      statusBar,
		headerPanel:    configureHeader(clock, statusBar),
		planeListPanel: configurePlaneList(),
		commands:       commands,
		gpsStatus:      gpsStatus,
		footer:         configureFooter(commands, gpsStatus),
		errorLine:      configureErrorLine(),
	}
}

func configureGrid(components *uiComponents) *tview.Grid {
	grid := tview.NewGrid().SetRows(1, 0, 1, 1).SetColumns(0, 50).SetBorders(false) //nolint:mnd
	grid.AddItem(components.headerPanel, 0, 0, 1, 2, 0, 0, false)
	grid.AddItem(components.radarPanel, 1, 0, 1, 1, 0, 0, false)
	grid.AddItem(components.planeListPanel, 1, 1, 1, 1, 0, 0, true)
	grid.AddItem(components.footer, 2, 0, 1, 2, 0, 0, false)
	grid.AddItem(components.errorLine, 3, 0, 1, 2, 0, 0, false)

	return grid
}

func startBatteryWatcher(components *uiComponents) {
	components.waitGroup.Add(1)

	go func() {
		defer components.waitGroup.Done()
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok {
					components.errChan <- errors.Join(err, errBatteryRecover)
				}
			}
		}()

		if err := battery.Watch(components.ctx, components.batteryStatus); err != nil {
			components.errChan <- err
		}
	}()
}

func startGPSWatcher(components *uiComponents) {
	components.waitGroup.Add(1)

	go func() {
		defer components.waitGroup.Done()
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok {
					components.errChan <- errors.Join(err, errGPSRecover)
				}
			}
		}()

		if err := gps.New().Watch(components.ctx, components.myLocation); err != nil {
			components.errChan <- err
		}
	}()
}

func startADSBStreamer(components *uiComponents) {
	components.waitGroup.Add(1)

	go func() {
		defer components.waitGroup.Done()
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok {
					components.errChan <- errors.Join(err, errASDBRecover)
				}
			}
		}()

		if err := adsb.New().Stream(components.ctx, components.planeList); err != nil {
			components.errChan <- err
		}
	}()
}

func startUIUpdater(components *uiComponents) {
	components.waitGroup.Add(1)

	go func() {
		defer components.waitGroup.Done()
		defer func() {
			if r := recover(); r != nil {
				components.cancel()
				components.app.Stop()
				slog.Error("Recovered in main goroutine:", slog.Any("error", r))
			}
		}()

		ticker := time.NewTicker(uiUpdateInterval)
		defer ticker.Stop()

		for {
			select {
			case appErr := <-components.errChan:
				components.cancel()
				components.app.Stop()
				slog.Error("Caught error in errChan:", slog.Any("error", appErr))

				return
			case <-components.ctx.Done():
				return
			case <-ticker.C:
				components.app.QueueUpdateDraw(func() {
					components.clock.SetText("Local: " + time.Now().Format(time.TimeOnly) + " UTC: " +
						time.Now().UTC().Format(time.TimeOnly))
					components.statusBar.SetText("Battery: " + components.batteryStatus.String())
					updatePlaneList(components.planeListPanel, components.myLocation, components.planeList)
					updateFooter(components.commands, components.radarPanel)
					components.gpsStatus.SetText("GPS: " + components.myLocation.String())
				})
			}
		}
	}()
}

// configureErrorLine configures the error line text view.
func configureErrorLine() *tview.TextView {
	errorLine := tview.NewTextView().SetTextAlign(tview.AlignLeft).SetText("no errors")
	errorLine.SetDynamicColors(true)
	errorLine.SetBackgroundColor(tcell.ColorRed)
	errorLine.SetMaxLines(1)
	errorLine.SetWrap(false)

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
