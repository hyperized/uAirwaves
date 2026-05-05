package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
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

const (
	uiUpdateInterval     = 1 * time.Second
	batteryWarningOrange = 40
	batteryWarningRed    = 20
)

func main() {
	uic := configureUI()
	radarPanel := radar.New(uic.planeList, uic.myLocation)
	uic.radarPanel = radarPanel

	grid := configureGrid(uic)

	startBatteryWatcher(uic, envOr("BATTERY_PATH", ""))
	startGPSWatcher(uic, envOr("GPSD_ADDRESS", ""))
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
		errChan:        make(chan error, 3),
		myLocation:     myLocation,
		waitGroup:      &sync.WaitGroup{},
		planeList:      planeList,
		batteryStatus:  battery.NewStatus(),
		clock:          clock,
		statusBar:      statusBar,
		headerPanel:    configureHeader(clock, gpsStatus, statusBar),
		planeListPanel: configurePlaneList(),
		commands:       commands,
		gpsStatus:      gpsStatus,
		footer:         configureFooter(commands),
	}
}

func configureGrid(components *uiComponents) *tview.Grid {
	grid := tview.NewGrid().SetRows(1, 0, 1).SetColumns(0, 50).SetBorders(false) //nolint:mnd
	grid.AddItem(components.headerPanel, 0, 0, 1, 2, 0, 0, false)
	grid.AddItem(components.radarPanel, 1, 0, 1, 1, 0, 0, false)
	grid.AddItem(components.planeListPanel, 1, 1, 1, 1, 0, 0, true)
	grid.AddItem(components.footer, 2, 0, 1, 2, 0, 0, false)

	return grid
}

// launchWorker runs fn in a goroutine, recovering panics and forwarding errors to errChan.
func launchWorker(wg *sync.WaitGroup, errChan chan<- error, panicSentinel error, fn func() error) {
	wg.Add(1)

	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				if err, ok := r.(error); ok {
					errChan <- errors.Join(err, panicSentinel)
				}
			}
		}()

		if err := fn(); err != nil {
			errChan <- err
		}
	}()
}

// envOr returns the value of an environment variable, or fallback if unset/empty.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return fallback
}

func startBatteryWatcher(components *uiComponents, filePath string) {
	launchWorker(components.waitGroup, components.errChan, errBatteryRecover, func() error {
		if filePath != "" {
			return battery.WatchWithInterval(components.ctx, components.batteryStatus, 30*time.Second, filePath)
		}

		return battery.Watch(components.ctx, components.batteryStatus)
	})
}

func startGPSWatcher(components *uiComponents, address string) {
	launchWorker(components.waitGroup, components.errChan, errGPSRecover, func() error {
		opts := []gps.Option{gps.WithReconnect(true)}
		if address != "" {
			opts = append(opts, gps.WithGpsAddress(address))
		}

		return gps.New(opts...).Watch(components.ctx, components.myLocation)
	})
}

func startADSBStreamer(components *uiComponents) {
	launchWorker(components.waitGroup, components.errChan, errASDBRecover, func() error {
		return adsb.New(adsb.WithLocation(components.myLocation)).
			Stream(components.ctx, components.planeList)
	})
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

					updateHeaderColor(components.batteryStatus.GetPercentage(), components.clock, components.statusBar)

					updatePlaneList(components.planeListPanel, components.myLocation, components.planeList)
					updateFooter(components.commands, components.radarPanel)
					components.gpsStatus.SetText("GPS: " + components.myLocation.String())
				})
			}
		}
	}()
}

// updateHeaderColor updates the header color based on the battery percentage.
func updateHeaderColor(percentage int8, clock *tview.TextView, statusBar *tview.TextView) {
	headerColor := tcell.ColorDarkGreen
	textColor := tcell.ColorBlack

	if percentage <= batteryWarningRed {
		headerColor = tcell.ColorRed
		textColor = tcell.ColorWhite
	} else if percentage <= batteryWarningOrange {
		headerColor = tcell.ColorOrange
		textColor = tcell.ColorBlack
	}

	clock.SetBackgroundColor(headerColor)
	clock.SetTextColor(textColor)
	statusBar.SetBackgroundColor(headerColor)
	statusBar.SetTextColor(textColor)
}

// configureFooter configures the footer panel.
func configureFooter(commands *tview.TextView) *tview.Flex {
	return tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(commands, 0, 1, false)
}

// configureGpsStatus configures the GPS status text view.
func configureGpsStatus() *tview.TextView {
	gpsStatus := tview.NewTextView().SetTextAlign(tview.AlignCenter)
	gpsStatus.SetDynamicColors(true)
	gpsStatus.SetBackgroundColor(tcell.ColorDarkGreen)
	gpsStatus.SetTextColor(tcell.ColorBlack)

	return gpsStatus
}

// configureCommands configures the commands text view.
func configureCommands() *tview.TextView {
	commands := tview.NewTextView().SetTextAlign(tview.AlignLeft).SetWrap(false)
	commands.SetDynamicColors(true)
	commands.SetBackgroundColor(tcell.ColorDarkBlue)

	return commands
}

// configurePlaneList configures the plane list panel.
func configurePlaneList() *tview.List {
	planeListPanel := tview.NewList().ShowSecondaryText(true)
	planeListPanel.SetBorder(false).SetTitle("Airplanes").
		SetTitleColor(tcell.ColorGreen).
		SetBorderPadding(1, 1, 1, 1)

	return planeListPanel
}

// configureHeader configures the header panel.
func configureHeader(clock *tview.TextView, gpsStatus *tview.TextView, statusBar *tview.TextView) *tview.Flex {
	return tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(clock, 0, 1, false).
		AddItem(gpsStatus, 0, 1, false).
		AddItem(statusBar, 0, 1, false)
}

// configureStatusbar configures the status bar text view.
func configureStatusbar() *tview.TextView {
	statusBar := tview.NewTextView().SetTextAlign(tview.AlignRight).SetText("loading...")
	statusBar.SetDynamicColors(true)
	statusBar.SetBackgroundColor(tcell.ColorDarkGreen)
	statusBar.SetTextColor(tcell.ColorBlack)

	return statusBar
}

// configureClock configures the clock text view.
func configureClock() *tview.TextView {
	clock := tview.NewTextView().SetTextAlign(tview.AlignLeft).SetText("..:..:..")
	clock.SetDynamicColors(true)
	clock.SetBackgroundColor(tcell.ColorDarkGreen)
	clock.SetTextColor(tcell.ColorBlack)

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
	case 't': // Toggle trail indicator
		radarPanel.ToggleTrailIndicator()
	case 'm': // Toggle heat map
		radarPanel.ToggleHeatIndicator()
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

		distance := airplanes.HaversineDistance(latitude, longitude, plane.Latitude, plane.Longitude)

		secondaryText := fmt.Sprintf("%.1fnm %s", distance, value.String())
		if distance == math.MaxFloat64 {
			secondaryText = value.String()
		}

		planeListPanel.AddItem(mainText, secondaryText, 0, nil)
	}
}

// updateFooter updates the footer text with the current scope range and heading indicator settings.
func updateFooter(commands *tview.TextView, radarPanel *radar.View) *tview.TextView {
	return commands.SetText(fmt.Sprintf(
		"[::b]Tracking: %d - [::b]Range (+/-): %0.0f nm - [::b]Heading (h): %t - [::b]Trail (t): %t - [::b]Heat (m): %t - [::b]Autoscope (a): %t",
		radarPanel.GetAircraftCount(),
		radarPanel.GetScopeRange(),
		radarPanel.GetHeadingIndicatorEnabled(),
		radarPanel.GetTrailIndicatorEnabled(),
		radarPanel.GetHeatIndicatorEnabled(),
		radarPanel.GetAutoScopeEnabled(),
	))
}
