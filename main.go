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
	"lab.hyperized.net/hyperized/uAirwaves/internal/ui"
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
	errADSBRecover    = errors.New("recovered in adsb goroutine")
)

const (
	uiUpdateInterval    = 1 * time.Second
	batteryPollInterval = 30 * time.Second
)

func main() {
	uic := configureUI()
	radarPanel := radar.New(uic.planeList, uic.myLocation)
	uic.radarPanel = radarPanel

	grid := configureGrid(uic)

	startBatteryWatcher(uic, ui.EnvOr("BATTERY_PATH", ""))
	startGPSWatcher(uic, ui.EnvOr("GPSD_ADDRESS", ""))
	startADSBStreamer(uic)
	startUIUpdater(uic)

	// Input capture for global shortcuts.
	uic.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		return ui.HandleKeyInput(event, uic.app, uic.radarPanel, uic.planeFilter)
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
	planeFilter    *ui.PlaneFilter
	adsbStream     *adsb.ADSB
	statsTracker   *ui.StatsTracker
	batteryStatus  *battery.Status
	clock          *tview.TextView
	statusBar      *tview.TextView
	headerPanel    *tview.Flex
	radarPanel     *radar.View
	planeListPanel *tview.List
	statsPanel     *tview.TextView
	rightColumn    *tview.Flex
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
	planeListPanel := configurePlaneList()
	statsPanel := configureStatsPanel()

	return &uiComponents{
		ctx:            ctx,
		cancel:         cancel,
		app:            tview.NewApplication(),
		errChan:        make(chan error, 3), //nolint:mnd // buffered for the three background workers.
		myLocation:     myLocation,
		waitGroup:      &sync.WaitGroup{},
		planeList:      planeList,
		planeFilter:    ui.NewPlaneFilter(),
		adsbStream:     adsb.New(buildADSBOptions(myLocation)...),
		statsTracker:   ui.NewStatsTracker(),
		batteryStatus:  battery.NewStatus(),
		clock:          clock,
		statusBar:      statusBar,
		headerPanel:    configureHeader(clock, gpsStatus, statusBar),
		planeListPanel: planeListPanel,
		statsPanel:     statsPanel,
		rightColumn:    configureRightColumn(planeListPanel, statsPanel),
		commands:       commands,
		gpsStatus:      gpsStatus,
		footer:         configureFooter(commands),
	}
}

// buildADSBOptions assembles the adsb.New option slice from the
// runtime environment. Sources, in precedence order:
//
//  1. UAIRWAVES_REPLAY_IQ → file-backed replay (testing).
//  2. BEAST_ADDRESS       → consume BEAST frames from a remote
//     demodulator over TCP (no local SDR).
//  3. Default             → drive the rtl2832u + demod stack
//     directly off the on-board SDR.
//
// Replay wins over BEAST so a developer can always replay a
// captured IQ even on a host that also has BEAST_ADDRESS set in
// its environment.
func buildADSBOptions(myLocation *location.Location) []adsb.Option {
	opts := []adsb.Option{adsb.WithLocation(myLocation)}

	if path := ui.EnvOr("UAIRWAVES_REPLAY_IQ", ""); path != "" {
		opts = append(opts, adsb.WithReceiverFactory(func() (adsb.Receiver, error) {
			rcv, err := adsb.NewFileReceiver(path)
			if err != nil {
				slog.Error("adsb: open replay failed", slog.String("path", path), slog.Any("error", err))

				return nil, fmt.Errorf("open replay %s: %w", path, err)
			}

			return rcv, nil
		}))

		slog.Info("adsb: replaying from file", slog.String("path", path))

		return opts
	}

	if addr := ui.EnvOr("BEAST_ADDRESS", ""); addr != "" {
		opts = append(opts, adsb.WithBeastAddress(addr))
		slog.Info("adsb: consuming BEAST", slog.String("address", addr))
	}

	return opts
}

func configureGrid(components *uiComponents) *tview.Grid {
	grid := tview.NewGrid().SetRows(1, 0, 1).SetColumns(0, 50).SetBorders(false) //nolint:mnd
	grid.AddItem(components.headerPanel, 0, 0, 1, 2, 0, 0, false)
	grid.AddItem(components.radarPanel, 1, 0, 1, 1, 0, 0, false)
	grid.AddItem(components.rightColumn, 1, 1, 1, 1, 0, 0, true)
	grid.AddItem(components.footer, 2, 0, 1, 2, 0, 0, false)

	return grid
}

func startBatteryWatcher(components *uiComponents, filePath string) {
	ui.LaunchWorker(components.waitGroup, components.errChan, errBatteryRecover, func() error {
		if filePath != "" {
			return battery.WatchWithInterval(components.ctx, components.batteryStatus, batteryPollInterval, filePath)
		}

		return battery.Watch(components.ctx, components.batteryStatus)
	})
}

func startGPSWatcher(components *uiComponents, address string) {
	ui.LaunchWorker(components.waitGroup, components.errChan, errGPSRecover, func() error {
		opts := []gps.Option{gps.WithReconnect(true)}
		if address != "" {
			opts = append(opts, gps.WithGpsAddress(address))
		}

		return gps.New(opts...).Watch(components.ctx, components.myLocation)
	})
}

func startADSBStreamer(components *uiComponents) {
	ui.LaunchWorker(components.waitGroup, components.errChan, errADSBRecover, func() error {
		return components.adsbStream.Stream(components.ctx, components.planeList)
	})
}

func startUIUpdater(components *uiComponents) {
	components.waitGroup.Go(func() {
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

					ui.UpdateHeaderColor(
						components.batteryStatus.GetPercentage(),
						components.clock,
						components.statusBar,
					)

					positionedOnly := components.planeFilter.PositionedOnly()
					ui.UpdatePlaneList(components.planeListPanel, components.myLocation, components.planeList,
						positionedOnly)
					ui.UpdateStatsPanel(components.statsPanel, components.adsbStream, components.statsTracker,
						components.myLocation, components.planeList)
					ui.UpdateFooter(components.commands, components.radarPanel, positionedOnly)
					components.gpsStatus.SetText("GPS: " + components.myLocation.String())
				})
			}
		}
	})
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

// configureStatsPanel configures the stats panel that lives
// below the plane list in the right column. tview-rendered text
// with dynamic colours so the renderer can dim secondary fields.
func configureStatsPanel() *tview.TextView {
	statsPanel := tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	statsPanel.SetBorder(true).SetTitle("Stats").
		SetTitleColor(tcell.ColorGreen).
		SetBorderPadding(0, 0, 1, 1)

	return statsPanel
}

// configureRightColumn stacks the plane list (top ~5/6) and the
// stats panel (bottom ~1/6) into a single column so the existing
// grid slot can host both.
func configureRightColumn(planeListPanel *tview.List, statsPanel *tview.TextView) *tview.Flex {
	const (
		planeListWeight = 5
		statsWeight     = 1
	)

	return tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(planeListPanel, 0, planeListWeight, true).
		AddItem(statsPanel, 0, statsWeight, false)
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
