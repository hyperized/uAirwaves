package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/hyperized/rtl2832u"
	"github.com/rivo/tview"
	"lab.hyperized.net/hyperized/uAirwaves/internal/check"
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
	uiUpdateInterval = 1 * time.Second
	// sweepUpdateInterval is the faster UI cadence used while the
	// SDR gain auto-sweep is in progress. It matches the radar
	// spinner's per-frame step so the rotating dot advances one
	// position per tick (a full rotation in ~2 s) instead of
	// jumping multiple steps per redraw.
	sweepUpdateInterval = 250 * time.Millisecond
	batteryPollInterval = 30 * time.Second
)

func main() {
	cfg, err := parseFlags(os.Args[1:], os.Stderr)
	if err != nil {
		// flag.ContinueOnError already printed the usage / error
		// to stderr; just translate to a usage-style exit code.
		os.Exit(2) //nolint:mnd // 2 = bad usage, conventional shell exit code.
	}

	if cfg.checkDuration > 0 {
		os.Exit(runCheckMode(cfg))
	}

	uic := configureUI(cfg)
	radarPanel := radar.New(uic.planeList, uic.myLocation, uic.adsbStream)
	uic.radarPanel = radarPanel

	// Replace the default slog handler before any worker
	// goroutine can emit. From this point on every slog write
	// lands in the in-app notification queue instead of stderr,
	// which is what tview owns once Run starts. Without this
	// swap, a single Info/Warn from a worker corrupts the
	// terminal layout (the very bug the notification bar was
	// asked to fix).
	originalSlog := slog.Default()

	slog.SetDefault(slog.New(ui.NewSlogHandler(uic.notifications, slog.LevelInfo)))

	uic.grid = configureGrid(uic)

	startBatteryWatcher(uic, cfg.batteryPath)
	startGPSWatcher(uic, cfg.gpsdAddress)
	startADSBStreamer(uic)
	startUIUpdater(uic)

	// Input capture for global shortcuts.
	biasTee := &biasTeeAdapter{stream: uic.adsbStream}
	ctrls := ui.NewKeyControllers(
		uic.app, uic.radarPanel, uic.notifications, biasTee,
		uic.selection, uic.planeListPanel,
	)

	uic.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		return ui.HandleKeyInput(event, ctrls)
	})

	if err := uic.app.SetRoot(uic.grid, true).EnableMouse(true).Run(); err != nil {
		slog.Error("tview error", slog.Any("error", err))
	}

	uic.cancel()
	uic.waitGroup.Wait()

	// Restore the original (stderr-backed) handler so the
	// shutdown messages below land on the terminal again.
	slog.SetDefault(originalSlog)

	slog.Info("Well, that was some experience...")
	slog.Info("Now just let me adjust the spacial controls...")
	slog.Info("And we'll move to another observation point.")

	os.Exit(0)
}

// Left-column page names — used by the Pages widget that swaps
// the radar and the flight-details view when the operator opens
// a plane from the right-column list.
const (
	leftPageRadar   = "radar"
	leftPageDetails = "details"
)

type uiComponents struct {
	ctx                context.Context //nolint:containedctx
	cancel             context.CancelFunc
	app                *tview.Application
	errChan            chan error
	myLocation         *location.Location
	waitGroup          *sync.WaitGroup
	planeList          *airplanes.Airplanes
	adsbStream         *adsb.ADSB
	statsTracker       *ui.StatsTracker
	batteryStatus      *battery.Status
	clock              *tview.TextView
	statusBar          *tview.TextView
	headerPanel        *tview.Flex
	notifications      *ui.Notifications
	notificationBar    *tview.TextView
	bottomSection      *tview.Flex
	grid               *tview.Grid
	radarPanel         *radar.View
	planeListPanel     *tview.List
	statsPanel         *tview.TextView
	rightColumn        *tview.Flex
	commands           *tview.TextView
	gpsStatus          *tview.TextView
	sourceStatus       *tview.TextView
	footer             *tview.Flex
	selection          *ui.Selection
	flightDetailsText  *tview.TextView
	flightDetailsMini  *radar.MiniView
	flightDetailsPanel *tview.Flex
	leftPages          *tview.Pages
}

func configureUI(cfg cliConfig) *uiComponents {
	ctx, cancel := context.WithCancel(context.Background())
	planeList := airplanes.New()
	myLocation := location.New()
	clock := configureClock()
	statusBar := configureStatusbar()
	commands := configureCommands()
	gpsStatus := configureGpsStatus()
	sourceStatus := configureSourceStatus()
	planeListPanel := configurePlaneList()
	statsPanel := configureStatsPanel()
	headerPanel := configureHeader(clock, gpsStatus, sourceStatus, statusBar)
	notifications := ui.NewNotifications()
	notificationBar := configureNotificationBar()
	footer := configureFooter(commands)
	bottomSection := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(footer, 1, 0, false).
		AddItem(notificationBar, 0, 0, false)
	detailsText, detailsMini, detailsPanel := configureFlightDetailsPanel(myLocation)

	return &uiComponents{
		ctx:                ctx,
		cancel:             cancel,
		app:                tview.NewApplication(),
		errChan:            make(chan error, 3), //nolint:mnd // buffered for the three background workers.
		myLocation:         myLocation,
		waitGroup:          &sync.WaitGroup{},
		planeList:          planeList,
		adsbStream:         adsb.New(buildADSBOptions(cfg, myLocation)...),
		statsTracker:       ui.NewStatsTracker(),
		batteryStatus:      battery.NewStatus(),
		clock:              clock,
		statusBar:          statusBar,
		headerPanel:        headerPanel,
		notifications:      notifications,
		notificationBar:    notificationBar,
		bottomSection:      bottomSection,
		planeListPanel:     planeListPanel,
		statsPanel:         statsPanel,
		rightColumn:        configureRightColumn(planeListPanel, statsPanel),
		commands:           commands,
		gpsStatus:          gpsStatus,
		sourceStatus:       sourceStatus,
		footer:             footer,
		selection:          ui.NewSelection(),
		flightDetailsText:  detailsText,
		flightDetailsMini:  detailsMini,
		flightDetailsPanel: detailsPanel,
	}
}

// buildADSBOptions assembles the adsb.New option slice from
// the parsed CLI config. Sources, in precedence order:
//
//  1. --replay-iq PATH → file-backed replay (testing).
//  2. --beast HOST:PORT → consume BEAST frames from a remote
//     demodulator over TCP (no local SDR).
//  3. Default         → drive the rtl2832u + demod stack
//     directly off the on-board SDR. --auto-sweep applies
//     here.
//
// Replay wins over BEAST so a developer can always replay a
// captured IQ even on a host that also sets --beast.
func buildADSBOptions(cfg cliConfig, myLocation *location.Location) []adsb.Option {
	opts := []adsb.Option{adsb.WithLocation(myLocation)}

	if cfg.replayIQPath != "" {
		path := cfg.replayIQPath
		opts = append(opts, adsb.WithReceiverFactory(func() (adsb.Receiver, error) {
			rcv, err := adsb.NewFileReceiver(path)
			if err != nil {
				slog.Error("adsb: open replay failed", slog.String("path", path), slog.Any("error", err))

				return nil, fmt.Errorf("open replay %s: %w", path, err)
			}

			return rcv, nil
		}), adsb.WithSourceLabel("Replay "+filepath.Base(path)))

		slog.Info("adsb: replaying from file", slog.String("path", path))

		return opts
	}

	if cfg.beastAddress != "" {
		opts = append(opts, adsb.WithBeastAddress(cfg.beastAddress), adsb.WithSourceLabel("BEAST "+cfg.beastAddress))
		slog.Info("adsb: consuming BEAST", slog.String("address", cfg.beastAddress))

		return opts
	}

	opts = append(opts, adsb.WithSourceLabel("SDR"))

	if cfg.biasTee {
		opts = append(opts, adsb.WithReceiverFactory(biasTeeReceiverFactory))

		slog.Info("adsb: bias-tee enabled at boot")
	}

	if cfg.autoSweep {
		opts = append(opts, adsb.WithAutoSweep())

		slog.Info("adsb: auto-sweep enabled (will run before first frame)")
	}

	return opts
}

// biasTeeReceiverFactory opens the RTL-SDR with the bias-tee
// pulled high at chip-config time. Identical to pkg/adsb's
// defaultReceiverFactory except it threads rtl2832u.WithBiasTee
// through Open so the external LNA / SAW filter is powered
// before --auto-sweep starts measuring (a sweep without LNA
// power picks the wrong gain cell).
//
//nolint:ireturn // factory: returning the interface is the seam pkg/adsb relies on.
func biasTeeReceiverFactory() (adsb.Receiver, error) {
	rcv, err := rtl2832u.Open(rtl2832u.WithBiasTee(true))
	if err != nil {
		return nil, fmt.Errorf("adsb: open RTL-SDR receiver with bias-tee: %w", err)
	}

	return rcv, nil
}

func configureGrid(components *uiComponents) *tview.Grid {
	components.leftPages = tview.NewPages().
		AddPage(leftPageRadar, components.radarPanel, true, true).
		AddPage(leftPageDetails, components.flightDetailsPanel, true, false)

	grid := tview.NewGrid().SetRows(1, 0, 1).SetColumns(0, 50).SetBorders(false) //nolint:mnd
	grid.AddItem(components.headerPanel, 0, 0, 1, 2, 0, 0, false)
	grid.AddItem(components.leftPages, 1, 0, 1, 1, 0, 0, false)
	grid.AddItem(components.rightColumn, 1, 1, 1, 1, 0, 0, true)
	grid.AddItem(components.bottomSection, 2, 0, 1, 2, 0, 0, false)

	return grid
}

// Flight-details Flex weights — text takes the top half (read at
// a glance), the mini-scope inset the bottom half (read for
// spatial context). Equal-ish weights keep both useful on small
// terminals without one starving the other.
const (
	flightDetailsTextWeight = 3
	flightDetailsMiniWeight = 2
)

// configureFlightDetailsPanel builds the bordered Flex that hosts
// the textual details (top) and the single-flight scope inset
// (bottom). Returns the parts separately so the UI ticker can
// push snapshots into each without re-walking the Flex tree.
//
//nolint:nonamedreturns // (text, mini, panel) reads clearer named at this signature.
func configureFlightDetailsPanel(
	loc *location.Location,
) (text *tview.TextView, mini *radar.MiniView, panel *tview.Flex) {
	text = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	mini = radar.NewMiniView(loc)

	panel = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(text, 0, flightDetailsTextWeight, false).
		AddItem(mini, 0, flightDetailsMiniWeight, false)
	panel.SetBorder(true).SetTitle("Flight details").
		SetTitleColor(tcell.ColorGreen).
		SetBorderPadding(1, 1, 2, 2) //nolint:mnd // padding for header chrome inside the panel.

	return text, mini, panel
}

// configureNotificationBar builds the 1-line bar that surfaces
// captured slog notifications. Background colour is applied
// per-record by renderNotification (red for ERROR, yellow for
// WARN, blue for INFO); the bar's default is transparent so the
// hidden state (height=0) doesn't leak a colour stripe.
func configureNotificationBar() *tview.TextView {
	bar := tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	bar.SetTextColor(tcell.ColorWhite)

	return bar
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
		return runGPSWatch(components.ctx, components.myLocation, address)
	})
}

func runGPSWatch(ctx context.Context, loc *location.Location, address string) error {
	opts := []gps.Option{gps.WithReconnect(true)}
	if address != "" {
		opts = append(opts, gps.WithGpsAddress(address))
	}

	if err := gps.New(opts...).Watch(ctx, loc); err != nil {
		return fmt.Errorf("gps watch: %w", err)
	}

	return nil
}

// runCheckMode runs the JSON-emitting non-TUI check window. Exit
// codes:
//
//	0 — thresholds met, JSON written to stdout
//	1 — thresholds not met (still emits the JSON; consumer can
//	    diff which gate failed)
//	2 — bad input (out-of-range duration) or fatal I/O error
func runCheckMode(cfg cliConfig) int {
	const (
		exitOK       = 0
		exitFail     = 1
		exitBadInput = 2
	)

	duration, err := check.ValidateDuration(cfg.checkDuration)
	if err != nil {
		slog.Error("check: bad --check duration", slog.Any("error", err))

		return exitBadInput
	}

	check.LogStart(duration)

	myLocation := location.New()
	planes := airplanes.New()
	stream := adsb.New(buildADSBOptions(cfg, myLocation)...)

	report, passed, runErr := check.Run(context.Background(), check.Options{
		Duration:   duration,
		Thresholds: check.DefaultThresholds(),
		Output:     os.Stdout,
		Stream:     stream,
		Planes:     planes,
		Location:   myLocation,
		StartGPS: func(ctx context.Context) error {
			return runGPSWatch(ctx, myLocation, cfg.gpsdAddress)
		},
		StartADSB: func(ctx context.Context) error {
			return stream.Stream(ctx, planes)
		},
	})
	if runErr != nil {
		slog.Error("check: worker error", slog.Any("error", runErr))

		return exitBadInput
	}

	if !passed {
		slog.Warn("check: thresholds not met", slog.Any("failures", report.Thresholds.Failures))

		return exitFail
	}

	return exitOK
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

		currentInterval := uiUpdateInterval

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
				// Adapt the redraw cadence to the SDR state: a
				// running gain sweep needs ~4 Hz to animate the
				// radar's loading spinner one cell per tick, but
				// the idle UI is plenty at 1 Hz. Reset only fires
				// when the desired cadence changes so a Reset call
				// every tick is avoided.
				desired := uiUpdateInterval
				if components.adsbStream.Sweeping() {
					desired = sweepUpdateInterval
				}

				if desired != currentInterval {
					ticker.Reset(desired)

					currentInterval = desired
				}

				components.app.QueueUpdateDraw(func() {
					components.clock.SetText("Local: " + time.Now().Format(time.TimeOnly) + " UTC: " +
						time.Now().UTC().Format(time.TimeOnly))
					components.statusBar.SetText("Battery: " + components.batteryStatus.String())

					ui.UpdateHeaderColor(
						components.batteryStatus.GetPercentage(),
						components.clock,
						components.statusBar,
					)

					ui.UpdatePlaneList(components.planeListPanel, components.myLocation, components.planeList,
						components.selection)
					ui.UpdateStatsPanel(components.statsPanel, components.adsbStream, components.statsTracker,
						components.myLocation, components.planeList)
					ui.UpdateFooter(components.commands, components.radarPanel, components.adsbStream)
					ui.UpdateSourceStatus(components.sourceStatus, components.adsbStream)
					components.gpsStatus.SetText("GPS: " + components.myLocation.String())
					renderFlightDetails(components)
					ui.RenderNotificationBar(
						components.grid, components.bottomSection, components.notificationBar, components.notifications,
					)
				})
			}
		}
	})
}

// renderFlightDetails reconciles the left-column page (radar vs.
// flight-details) with the selection state and feeds the details
// panel a fresh snapshot of the picked plane. Pulled out of the
// UI ticker so the ticker body stays under the line-length gate
// and so the swap logic reads in one place.
//
// Resolution flow:
//  1. Selection closed → switch to radar page, leave details
//     content alone so reopening the same plane shows the prior
//     state for one tick (negligible UX, simpler code).
//  2. Selection open with a known ICAO → look it up in the live
//     plane list; if found, snapshot and render; if pruned, show
//     a "no longer tracked" placeholder so the operator sees the
//     plane went away rather than a stale freeze.
//  3. Selection open but no plane has been picked yet → fall
//     through to the renderer's nil-snapshot hint.
func renderFlightDetails(components *uiComponents) {
	if !components.selection.IsOpen() {
		components.leftPages.SwitchToPage(leftPageRadar)
		components.flightDetailsMini.Clear()

		return
	}

	components.leftPages.SwitchToPage(leftPageDetails)

	icao := components.selection.ICAO()
	if icao == "" {
		ui.UpdateFlightDetails(components.flightDetailsText, nil, 0, 0)
		components.flightDetailsMini.Clear()
		fitFlightDetailsText(components)

		return
	}

	plane, ok := components.planeList.Get(icao)
	if !ok {
		components.flightDetailsText.SetText(
			"[gray]Plane " + icao + " is no longer tracked (pruned). Press Esc to return to the radar.[white]",
		)
		components.flightDetailsMini.Clear()
		fitFlightDetailsText(components)

		return
	}

	snap := plane.GetSnapshot()
	rxLat, rxLon := components.myLocation.GetCoordinates()
	ui.UpdateFlightDetails(components.flightDetailsText, &snap, rxLat, rxLon)
	components.flightDetailsMini.SetSnapshot(snap)
	fitFlightDetailsText(components)
}

// fitFlightDetailsText resizes the text Flex item to exactly its
// current line count so the mini-scope below it gets the rest of
// the panel's vertical space — no dead gap between the last text
// row and the mini-scope. The Flex's mini-scope item carries the
// only proportional weight, so it absorbs the remainder.
func fitFlightDetailsText(components *uiComponents) {
	body := components.flightDetailsText.GetText(false)
	lines := strings.Count(body, "\n") + 1
	components.flightDetailsPanel.ResizeItem(components.flightDetailsText, lines, 0)
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
// Wrap-around is disabled: pressing Down at the bottom row (or
// Up at the top) should be inert, not jump to the opposite end —
// the operator's eye loses the cursor when the list "teleports".
func configurePlaneList() *tview.List {
	planeListPanel := tview.NewList().ShowSecondaryText(true)
	planeListPanel.SetWrapAround(false)
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

// Header flex weights. The source pill carries the widest content
// ("Source: BEAST 192.168.1.159:30005 ● 1.2 KiB") and was truncating
// the byte-unit suffix at the default 1/4-of-width slot. Doubling
// the source slot lets the units render without making the rest of
// the header crowded.
const (
	headerWeightClock  = 1
	headerWeightGPS    = 1
	headerWeightSource = 2
	headerWeightStatus = 1
)

// configureHeader configures the header panel.
func configureHeader(
	clock *tview.TextView, gpsStatus *tview.TextView, sourceStatus *tview.TextView, statusBar *tview.TextView,
) *tview.Flex {
	return tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(clock, 0, headerWeightClock, false).
		AddItem(gpsStatus, 0, headerWeightGPS, false).
		AddItem(sourceStatus, 0, headerWeightSource, false).
		AddItem(statusBar, 0, headerWeightStatus, false)
}

// configureSourceStatus configures the data-source status text
// view in the header. Shares the GPS pill's colour scheme so the
// header reads as one band.
func configureSourceStatus() *tview.TextView {
	sourceStatus := tview.NewTextView().SetTextAlign(tview.AlignCenter)
	sourceStatus.SetDynamicColors(true)
	sourceStatus.SetBackgroundColor(tcell.ColorDarkGreen)
	sourceStatus.SetTextColor(tcell.ColorBlack)

	return sourceStatus
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

// biasTeeStream is the slice of *adsb.ADSB the bias-tee adapter
// drives. Hoisted to an interface so newBiasTeeAdapter is testable
// against a fake without spinning up a real Stream.
type biasTeeStream interface {
	BiasTeeSupported() bool
	BiasTeeEnabled() (bool, error)
	SetBiasTee(enable bool) error
}

// biasTeeAdapter bridges the 'b' keybind dispatch to the ADSB
// stream. Read-then-toggle: poll the chip, flip the bit, log the
// outcome through slog so the notification bar surfaces it. The
// UI footer's own poll renders the new state on the next tick —
// no in-adapter cache is kept so a third-party flipping the bit
// (rtl_biast, another process) stays visible.
type biasTeeAdapter struct {
	stream biasTeeStream
}

// ToggleBiasTee implements ui.BiasTeeController. Reads the chip,
// flips the bit, and slog-logs the outcome. Unsupported sources
// surface a one-line warn; the key press is otherwise inert.
func (a *biasTeeAdapter) ToggleBiasTee() {
	if !a.stream.BiasTeeSupported() {
		slog.Warn("bias-tee: not available on the active source (BEAST / replay mode)")

		return
	}

	enabled, err := a.stream.BiasTeeEnabled()
	if err != nil {
		slog.Warn("bias-tee: read failed", slog.Any("error", err))

		return
	}

	if err := a.stream.SetBiasTee(!enabled); err != nil {
		slog.Warn("bias-tee: set failed", slog.Any("error", err))

		return
	}

	slog.Info("bias-tee toggled", slog.Bool("enabled", !enabled))
}
