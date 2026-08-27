package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/hyperized/rtl2832u"
	"github.com/hyperized/uAirwaves/internal/check"
	"github.com/hyperized/uAirwaves/internal/ui"
	"github.com/hyperized/uAirwaves/pkg/adsb"
	"github.com/hyperized/uAirwaves/pkg/airplanes"
	"github.com/hyperized/uAirwaves/pkg/battery"
	"github.com/hyperized/uAirwaves/pkg/coverage"
	"github.com/hyperized/uAirwaves/pkg/gps"
	"github.com/hyperized/uAirwaves/pkg/location"
	"github.com/hyperized/uAirwaves/pkg/radar"
	"github.com/hyperized/uAirwaves/pkg/selflocate"
	"github.com/rivo/tview"
)

var (
	errBatteryRecover    = errors.New("recovered in battery goroutine")
	errGPSRecover        = errors.New("recovered in gps goroutine")
	errADSBRecover       = errors.New("recovered in adsb goroutine")
	errADSBWorkerRecover = errors.New("recovered in adsb helper goroutine")
	errSelfLocateRecover = errors.New("recovered in self-locate goroutine")
	errBiasTeeRecover    = errors.New("recovered in bias-tee goroutine")
)

// workerErrChanDepth sizes the shared worker error channel. The UI
// updater consumes exactly one error before it tears the app down, so
// the buffer must absorb a panic-send from every other background
// goroutine without blocking wg.Wait() at shutdown: the four named
// workers (battery, GPS, ADSB, self-locate), the three Stream helper
// goroutines routed through adsbWorkerSpawner, and one in-flight
// bias-tee toggle.
const workerErrChanDepth = 8

const (
	uiUpdateInterval = 1 * time.Second
	// sweepUpdateInterval is the faster UI cadence used while the
	// SDR gain auto-sweep is in progress. It matches the radar
	// spinner's per-frame step so the rotating dot advances one
	// position per tick (a full rotation in ~2 s) instead of
	// jumping multiple steps per redraw.
	sweepUpdateInterval = 250 * time.Millisecond
	batteryPollInterval = 30 * time.Second

	// selfLocateTickInterval is how often the self-locate worker
	// considers pushing an ADSB-derived position into myLocation.
	// 15 s is well above the ADSB stream's per-frame cadence
	// (each Estimate call walks the full observation buffer, so
	// over-ticking just burns CPU) and below the gpsd watchdog
	// window so a freshly stalled GPS hands control to self-locate
	// inside one tick.
	selfLocateTickInterval = 15 * time.Second

	// selfLocateGPSFreshWindow is how recent a gpsd-supplied fix
	// must be for the self-locate worker to defer. 30 s matches
	// gps.tpvTimeout — once gpsd has been silent past that window,
	// the GPS watchdog has already declared the session stalled,
	// and self-locate becomes the authority.
	selfLocateGPSFreshWindow = 30 * time.Second
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
	startSelfLocateWorker(uic)
	startADSBStreamer(uic)
	startUIUpdater(uic)

	// Input capture for global shortcuts.
	biasTee := &biasTeeAdapter{
		stream:    uic.adsbStream,
		waitGroup: uic.waitGroup,
		errChan:   uic.errChan,
	}
	coverageCtrl := &coverageController{
		view:        uic.coverageView,
		rightColumn: uic.rightColumn,
		panel:       uic.coveragePanel,
	}
	ctrls := ui.NewKeyControllers(
		uic.app, uic.radarPanel, uic.flightDetailsMini,
		uic.notifications, biasTee,
		uic.selection, uic.planeListPanel, coverageCtrl,
	)

	uic.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		return ui.HandleKeyInput(event, ctrls)
	})

	restoreLog := func() { slog.SetDefault(originalSlog) }

	code := runWithRecover(
		func() error { return uic.app.SetRoot(uic.grid, true).EnableMouse(true).Run() },
		restoreLog,
	)

	uic.cancel()
	uic.waitGroup.Wait()

	// Restore the original (stderr-backed) handler so the
	// shutdown messages below land on the terminal again.
	// Idempotent: the panic path inside runWithRecover already
	// restored it, so a second call is a harmless no-op.
	restoreLog()

	slog.Info("Well, that was some experience...")
	slog.Info("Now just let me adjust the spacial controls...")
	slog.Info("And we'll move to another observation point.")

	os.Exit(code)
}

// runWithRecover runs the tview event loop and guards the graceful
// shutdown tail: a panic in any render closure is recovered, the log
// handler restored, and the panic logged with its stack, so main's
// cancel/Wait/farewell still run instead of the process aborting.
// Returns the exit code — 1 on a recovered panic, 0 otherwise.
//
//nolint:nonamedreturns // exitCode is set from the deferred recover.
func runWithRecover(run func() error, restoreLog func()) (exitCode int) {
	defer func() {
		if r := recover(); r != nil {
			restoreLog()
			slog.Error("Recovered from panic in tview event loop",
				slog.Any("panic", r),
				slog.String("stack", string(debug.Stack())),
			)

			exitCode = 1
		}
	}()

	if err := run(); err != nil {
		slog.Error("tview error", slog.Any("error", err))

		return 0
	}

	return 0
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
	coveragePanel      *tview.TextView
	coverageTracker    *coverage.Tracker
	coverageView       *ui.CoverageView
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

	// selfLocator buffers CPR-decoded plane positions and
	// produces a receiver-location estimate on demand. Wired
	// into the ADSB stream as a PositionObserver.
	selfLocator *selflocate.Locator

	// gpsLastFix records the wall-clock instant of the most
	// recent GPS fix that carried a real lat/lon. nil means
	// "GPS has never produced a real fix this session". The
	// self-locate worker reads this to decide whether to defer
	// to GPS or push its own estimate.
	gpsLastFix *atomic.Pointer[time.Time]
}

func configureUI(cfg cliConfig) *uiComponents {
	ctx, cancel := context.WithCancel(context.Background())
	planeList := airplanes.New()
	myLocation := location.New()
	selfLocator := selflocate.New()
	coverageTracker := coverage.New()
	coverageView := ui.NewCoverageView()
	gpsLastFix := &atomic.Pointer[time.Time]{}
	// waitGroup and errChan are built here (rather than inline in the
	// struct literal) so buildADSBOptions can hand Stream's helper
	// goroutines the same supervision every other worker gets.
	waitGroup := &sync.WaitGroup{}
	errChan := make(chan error, workerErrChanDepth)
	clock := configureClock()
	statusBar := configureStatusbar()
	commands := configureCommands()
	gpsStatus := configureGpsStatus()
	sourceStatus := configureSourceStatus()
	planeListPanel := configurePlaneList()
	statsPanel := configureStatsPanel()
	coveragePanel := configureCoveragePanel()
	headerPanel := configureHeader(clock, gpsStatus, sourceStatus, statusBar)
	notifications := ui.NewNotifications()
	notificationBar := configureNotificationBar()
	footer := configureFooter(commands)
	bottomSection := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(footer, 1, 0, false).
		AddItem(notificationBar, 0, 0, false)
	detailsText, detailsMini, detailsPanel := configureFlightDetailsPanel(myLocation)
	// One observer fans each CPR fix out to the self-locate estimator and
	// the antenna-coverage tracker; buildADSBOptions installs it as the
	// single adsb.PositionObserver.
	observer := positionObserver(myLocation, selfLocator, coverageTracker)

	return &uiComponents{
		ctx:                ctx,
		cancel:             cancel,
		app:                tview.NewApplication(),
		errChan:            errChan,
		myLocation:         myLocation,
		waitGroup:          waitGroup,
		planeList:          planeList,
		adsbStream:         adsb.New(buildADSBOptions(cfg, myLocation, observer, waitGroup, errChan)...),
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
		coveragePanel:      coveragePanel,
		coverageTracker:    coverageTracker,
		coverageView:       coverageView,
		rightColumn:        configureRightColumn(planeListPanel, statsPanel, coveragePanel),
		commands:           commands,
		gpsStatus:          gpsStatus,
		sourceStatus:       sourceStatus,
		footer:             footer,
		selection:          ui.NewSelection(),
		flightDetailsText:  detailsText,
		flightDetailsMini:  detailsMini,
		flightDetailsPanel: detailsPanel,
		selfLocator:        selfLocator,
		gpsLastFix:         gpsLastFix,
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
//
// waitGroup and errChan supervise Stream's three helper goroutines
// via adsbWorkerSpawner; the spawner is threaded onto every source
// branch because Stream launches those helpers before it forks to
// the SDR / BEAST / replay path. Both nil (the check mode, whose
// WaitGroup lives inside check.Run) leaves Stream on its default
// recover-and-log spawner.
func buildADSBOptions(
	cfg cliConfig,
	myLocation *location.Location,
	observer adsb.PositionObserver,
	waitGroup *sync.WaitGroup,
	errChan chan<- error,
) []adsb.Option {
	opts := []adsb.Option{adsb.WithLocation(myLocation)}

	if waitGroup != nil && errChan != nil {
		opts = append(opts, adsb.WithWorkerSpawner(adsbWorkerSpawner(waitGroup, errChan)))
	}

	if observer != nil {
		opts = append(opts, adsb.WithPositionObserver(observer))
	}

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

	// Reaching here means neither --replay-iq nor --beast was set, so
	// this is the local-SDR source: enable reconnect so an unplugged
	// dongle backs off and retries instead of killing the TUI. Replay
	// and BEAST return above and never carry this option.
	opts = append(opts, adsb.WithSourceLabel("SDR"), adsb.WithSDRReconnect())

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

// adsbWorkerSpawner adapts adsb.Stream's helper-goroutine launch to
// the process WaitGroup and shared error channel. Stream starts three
// long-lived helpers (stale-plane prune, CPR-cache cleanup,
// ICAO-filter cleanup); routing each through ui.LaunchWorker gives
// them the same guarantees every other worker has — a recovered panic
// is joined with errADSBWorkerRecover and forwarded to errChan, and
// wg.Wait() at shutdown blocks until they return. All three exit on
// ctx.Done, so they honour WithWorkerSpawner's ctx-aware contract.
func adsbWorkerSpawner(waitGroup *sync.WaitGroup, errChan chan<- error) func(task func()) {
	return func(task func()) {
		ui.LaunchWorker(waitGroup, errChan, errADSBWorkerRecover, func() error {
			task()

			return nil
		})
	}
}

// positionObserver builds the adsb.PositionObserver that fans one
// CPR-decoded fix out to the self-locate estimator and the antenna-
// coverage tracker. It runs on the ADSB ingest goroutine, so it stays
// allocation-free and non-blocking: GetCoordinates takes a read lock, the
// distance/bearing helpers are pure, and Tracker.Observe is a single
// mutex. Coverage fixes are dropped until a receiver position is known
// (both coordinates zero) because distance and bearing are meaningless
// without it, and the HaversineDistance sentinel is guarded so a
// null-island plane never lands in the last bin. Returns nil when neither
// consumer is wired (check mode), so no hook is installed at all.
func positionObserver(
	myLocation *location.Location,
	selfLocator *selflocate.Locator,
	tracker *coverage.Tracker,
) adsb.PositionObserver {
	if selfLocator == nil && tracker == nil {
		return nil
	}

	return func(lat, lon, altFt float64) {
		if selfLocator != nil {
			selfLocator.Observe(lat, lon, altFt)
		}

		if tracker == nil {
			return
		}

		receiverLat, receiverLon := myLocation.GetCoordinates()
		if receiverLat == 0 && receiverLon == 0 {
			return
		}

		distance := airplanes.HaversineDistance(receiverLat, receiverLon, lat, lon)
		if distance == math.MaxFloat64 {
			return
		}

		tracker.Observe(distance, ui.FlightBearing(receiverLat, receiverLon, lat, lon), altFt)
	}
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
		SetTitleColor(ui.ColorPanelTitle).
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
	// Starting value only; RenderNotificationBar sets the pair that matches
	// whichever severity is currently showing.
	bar.SetTextColor(ui.ColorNotifyInfoText)

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
	onFix := stampLastFix(components.gpsLastFix)

	ui.LaunchWorker(components.waitGroup, components.errChan, errGPSRecover, func() error {
		return runGPSWatch(components.ctx, components.myLocation, address, onFix)
	})
}

// stampLastFix returns a gps.FixCallback that records the
// supplied instant into the atomic pointer the self-locate
// worker reads. Pulled out as a named helper so the wiring is
// testable without spinning up gpsd.
func stampLastFix(slot *atomic.Pointer[time.Time]) gps.FixCallback {
	if slot == nil {
		return nil
	}

	return func(at time.Time) {
		slot.Store(&at)
	}
}

func runGPSWatch(
	ctx context.Context, loc *location.Location, address string, onFix gps.FixCallback,
) error {
	opts := []gps.Option{gps.WithReconnect(true)}
	if address != "" {
		opts = append(opts, gps.WithGpsAddress(address))
	}

	if onFix != nil {
		opts = append(opts, gps.WithFixCallback(onFix))
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
	// Check mode skips the self-locate fallback: a ≤ 5 minute
	// diagnostic window is well below the time it takes the
	// horizon-circle intersection to converge, and the JSON
	// output already reports gpsd state explicitly.
	//
	// nil WaitGroup + errChan leave Stream's helper goroutines on the
	// default recover-and-log spawner: check.Run owns its own
	// WaitGroup and a 1:1-sized errChan, so wiring the helpers through
	// it would change that channel's sizing contract.
	stream := adsb.New(buildADSBOptions(cfg, myLocation, nil, nil, nil)...)

	report, passed, runErr := check.Run(context.Background(), check.Options{
		Duration:   duration,
		Thresholds: check.DefaultThresholds(),
		Output:     os.Stdout,
		Stream:     stream,
		Planes:     planes,
		Location:   myLocation,
		StartGPS: func(ctx context.Context) error {
			return runGPSWatch(ctx, myLocation, cfg.gpsdAddress, nil)
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

// startSelfLocateWorker spins up the goroutine that periodically
// considers pushing an ADSB-derived position into myLocation
// when GPS has not produced a fix recently. Always runs alongside
// the GPS watcher; the per-tick gpsFreshness check decides which
// source wins on any given tick.
func startSelfLocateWorker(components *uiComponents) {
	ui.LaunchWorker(components.waitGroup, components.errChan, errSelfLocateRecover, func() error {
		runSelfLocate(
			components.ctx, components.myLocation, components.selfLocator, components.gpsLastFix,
			selfLocateTickInterval, selfLocateGPSFreshWindow,
		)

		return nil
	})
}

// runSelfLocate loops on a ticker, asking the locator for an
// estimate each tick and pushing it into myLocation when GPS has
// been silent long enough that the fallback should fill in.
//
// Parameters are explicit (rather than read from package-level
// constants) so internal tests can drive a fast tick + freshness
// window without touching the production values.
func runSelfLocate(
	ctx context.Context,
	loc *location.Location,
	locator *selflocate.Locator,
	gpsLastFix *atomic.Pointer[time.Time],
	tickInterval, gpsFreshWindow time.Duration,
) {
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	var (
		lastAppliedLat, lastAppliedLon float64
		hasApplied                     bool
	)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if gpsIsFresh(gpsLastFix, gpsFreshWindow) {
				continue
			}

			fix, ok := locator.Estimate()
			if !ok {
				continue
			}

			if hasApplied && fix.Latitude == lastAppliedLat && fix.Longitude == lastAppliedLon {
				continue
			}

			loc.Update(
				location.WithSource(location.SourceInferred),
				location.WithConfidenceRadiusNm(fix.ConfidenceRadiusNm),
				location.WithLatitude(fix.Latitude),
				location.WithLongitude(fix.Longitude),
			)

			if !hasApplied {
				slog.Info("Self-locate fix applied",
					slog.Float64("lat", fix.Latitude),
					slog.Float64("lon", fix.Longitude),
					slog.Float64("confidence_nm", fix.ConfidenceRadiusNm),
					slog.Int("observations", fix.ObservationCount),
				)
			}

			lastAppliedLat = fix.Latitude
			lastAppliedLon = fix.Longitude
			hasApplied = true
		}
	}
}

// gpsIsFresh reports whether the most recent GPS fix is younger
// than the supplied window. nil slot or never-set pointer counts
// as "stale" so self-locate is free to fill in.
func gpsIsFresh(slot *atomic.Pointer[time.Time], window time.Duration) bool {
	if slot == nil {
		return false
	}

	last := slot.Load()
	if last == nil {
		return false
	}

	return time.Since(*last) < window
}

func startUIUpdater(components *uiComponents) {
	components.waitGroup.Go(func() {
		runUIUpdateLoop(components, uiUpdateInterval)
	})
}

// runUIUpdateLoop is the redraw pump the UI-updater goroutine runs.
// It watches the shared error channel and context alongside the
// redraw ticker: an error tears the app down, a cancelled context
// returns cleanly, and every tick hands off to tickUI. A panic in
// any render path is recovered here so a single bad frame stops the
// app gracefully instead of aborting the process.
//
// The interval is a parameter (production always passes
// uiUpdateInterval) so internal tests can drive the tick and recover
// paths on a millisecond cadence without a wall-clock second.
func runUIUpdateLoop(components *uiComponents, interval time.Duration) {
	defer func() {
		if r := recover(); r != nil {
			components.cancel()
			components.app.Stop()
			slog.Error("Recovered in main goroutine:", slog.Any("error", r))
		}
	}()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	currentInterval := interval

	// drawInFlight coalesces redraws and keeps the enqueue off
	// this WaitGroup-tracked goroutine: TryQueueUpdateDraw skips
	// a tick whose predecessor's draw has not yet run, so Stop()
	// stranding an update can never wedge wg.Wait() on exit.
	var drawInFlight atomic.Bool

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
			currentInterval = tickUI(components, ticker, currentInterval, &drawInFlight)
		}
	}
}

// tickUI adapts the redraw cadence to the SDR state and, unless
// shutdown has begun, enqueues one coalesced redraw. It returns the
// (possibly changed) ticker interval so the caller tracks cadence
// transitions across ticks.
//
// A running gain sweep needs ~4 Hz to animate the radar spinner one
// cell per tick; the idle UI is plenty at 1 Hz. Reset only fires
// when the desired cadence changes so a Reset every tick is avoided.
func tickUI(
	components *uiComponents, ticker *time.Ticker, current time.Duration, drawInFlight *atomic.Bool,
) time.Duration {
	desired := uiUpdateInterval
	if components.adsbStream.Sweeping() {
		desired = sweepUpdateInterval
	}

	if desired != current {
		ticker.Reset(desired)

		current = desired
	}

	// A tick that fires during shutdown must not enqueue: the event
	// loop is tearing down and the draw would strand with nothing
	// left to run it.
	if components.ctx.Err() != nil {
		return current
	}

	ui.TryQueueUpdateDraw(components.app, drawInFlight, func() {
		renderUI(components)
	})

	return current
}

// renderUI paints one frame of the TUI from the current domain
// snapshots. It runs on the tview event loop (enqueued by tickUI),
// never directly from the ticker goroutine, so every widget mutation
// here is serialised with tview's own Draw.
func renderUI(components *uiComponents) {
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
	renderCoverage(components)
	ui.UpdateFooter(components.commands, components.radarPanel, components.adsbStream,
		components.coverageView.Mode())
	ui.UpdateSourceStatus(components.sourceStatus, components.adsbStream)
	components.gpsStatus.SetText(components.myLocation.String())
	renderFlightDetails(components)
	ui.RenderNotificationBar(
		components.grid, components.bottomSection, components.notificationBar, components.notifications,
	)
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

// renderCoverage repaints the coverage panel from a fresh tracker snapshot
// unless the panel is off, in which case it is collapsed (the 'c' handler
// already resized it to zero) and there is nothing to draw. Runs on the
// tview event loop via renderUI, so the coverageView read is serialised
// with the 'c' key handler's write.
func renderCoverage(components *uiComponents) {
	mode := components.coverageView.Mode()
	if mode == ui.CoverageOff {
		return
	}

	ui.UpdateCoveragePanel(components.coveragePanel, mode, components.coverageTracker.Snapshot())
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
	gpsStatus.SetBackgroundColor(ui.ColorHeaderOKBackground)
	gpsStatus.SetTextColor(ui.ColorHeaderOKText)

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
	// tview defaults this to TertiaryTextColor (#008000), which is unreadable
	// on a dark terminal — and it is the line carrying distance and altitude.
	planeListPanel.SetSecondaryTextColor(ui.ColorSecondaryText)
	planeListPanel.SetBorder(false).SetTitle("Airplanes").
		SetTitleColor(ui.ColorPanelTitle).
		SetBorderPadding(1, 1, 1, 1)

	return planeListPanel
}

// configureStatsPanel configures the stats panel that lives
// below the plane list in the right column. tview-rendered text
// with dynamic colours so the renderer can dim secondary fields.
func configureStatsPanel() *tview.TextView {
	statsPanel := tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	statsPanel.SetBorder(true).SetTitle("Stats").
		SetTitleColor(ui.ColorPanelTitle).
		SetBorderPadding(0, 0, 1, 1)

	return statsPanel
}

// configureCoveragePanel configures the antenna-coverage panel that sits
// below the stats panel in the right column. Same styling as the stats
// panel: bordered, dynamic colours, wrap disabled so the ASCII plot is
// never reflowed.
func configureCoveragePanel() *tview.TextView {
	coveragePanel := tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	coveragePanel.SetBorder(true).SetTitle("Coverage").
		SetTitleColor(ui.ColorPanelTitle).
		SetBorderPadding(0, 0, 1, 1)

	return coveragePanel
}

// coverageWeight is the right-column Flex weight the coverage panel gets
// when visible. Toggling the panel off resizes the item to 0 so the plane
// list reclaims the rows. Package-level because both configureRightColumn
// (the initial layout) and applyCoverageVisibility (the runtime toggle)
// reference it.
const coverageWeight = 2

// configureRightColumn stacks the plane list (top), the stats panel, and
// the antenna-coverage panel into a single column so the existing grid
// slot hosts all three. The coverage panel starts visible (cone default);
// the 'c' key collapses it to hand its rows back to the plane list.
func configureRightColumn(
	planeListPanel *tview.List, statsPanel, coveragePanel *tview.TextView,
) *tview.Flex {
	const (
		planeListWeight = 5
		statsWeight     = 1
	)

	return tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(planeListPanel, 0, planeListWeight, true).
		AddItem(statsPanel, 0, statsWeight, false).
		AddItem(coveragePanel, 0, coverageWeight, false)
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
	sourceStatus.SetBackgroundColor(ui.ColorHeaderOKBackground)
	sourceStatus.SetTextColor(ui.ColorHeaderOKText)

	return sourceStatus
}

// configureStatusbar configures the status bar text view.
func configureStatusbar() *tview.TextView {
	statusBar := tview.NewTextView().SetTextAlign(tview.AlignRight).SetText("loading...")
	statusBar.SetDynamicColors(true)
	statusBar.SetBackgroundColor(ui.ColorHeaderOKBackground)
	statusBar.SetTextColor(ui.ColorHeaderOKText)

	return statusBar
}

// configureClock configures the clock text view.
func configureClock() *tview.TextView {
	clock := tview.NewTextView().SetTextAlign(tview.AlignLeft).SetText("..:..:..")
	clock.SetDynamicColors(true)
	clock.SetBackgroundColor(ui.ColorHeaderOKBackground)
	clock.SetTextColor(ui.ColorHeaderOKText)

	return clock
}

// biasTeeStream is the slice of *adsb.ADSB the bias-tee adapter
// drives. Hoisted to an interface so the adapter is testable
// against a fake without spinning up a real Stream. BiasTeeState is
// the USB-free cached read; SetBiasTee is the control transfer the
// worker runs off the event loop.
type biasTeeStream interface {
	BiasTeeState() (supported, enabled bool)
	SetBiasTee(enable bool) error
}

// biasTeeAdapter bridges the 'b' keybind dispatch to the ADSB
// stream. The flip is a USB control transfer, so it must not run on
// the tview event loop where the dispatcher fires: a wedged dongle
// (an unplug or bus reset mid-write) would freeze every redraw and
// keypress. ToggleBiasTee therefore hands the read-modify-write to a
// background worker via ui.LaunchWorker and returns immediately;
// inFlight collapses a burst of key presses to one in-flight toggle.
//
// The target state is computed from the cached adsb.BiasTeeState
// rather than a live poll, so no read transfer happens on press
// either. Trade-off: a flip performed outside this process (an
// external rtl_biast run) is not observed — the cached bit re-syncs
// at the next (re)open and on the next in-app toggle.
type biasTeeAdapter struct {
	stream    biasTeeStream
	waitGroup *sync.WaitGroup
	errChan   chan<- error
	inFlight  atomic.Bool
}

// ToggleBiasTee implements ui.BiasTeeController. Runs on the tview
// event loop, so it does no USB work itself: it claims the in-flight
// guard and dispatches the actual flip to a background worker. A
// press while a toggle is already running is a logged no-op — the
// GPIO write is not worth queueing.
func (a *biasTeeAdapter) ToggleBiasTee() {
	if !a.inFlight.CompareAndSwap(false, true) {
		slog.Info("bias-tee: toggle already in progress")

		return
	}

	ui.LaunchWorker(a.waitGroup, a.errChan, errBiasTeeRecover, a.toggle)
}

// toggle performs the read-modify-write off the event loop: read the
// cached state, drive the SetBiasTee control transfer, log the
// outcome, and clear the in-flight guard. It returns nil even when
// the flip fails — a bias-tee write failure must not tear down the
// app through errChan, so the sentinel exists only to tag a
// recovered panic. The defer clears inFlight on every exit path,
// including a panic, so the next press can start a fresh worker.
func (a *biasTeeAdapter) toggle() error {
	defer a.inFlight.Store(false)

	supported, enabled := a.stream.BiasTeeState()
	if !supported {
		slog.Warn("bias-tee: not available on the active source (BEAST / replay mode)")

		return nil
	}

	if err := a.stream.SetBiasTee(!enabled); err != nil {
		slog.Warn("bias-tee: set failed", slog.Any("error", err))

		return nil
	}

	slog.Info("bias-tee toggled", slog.Bool("enabled", !enabled))

	return nil
}

// coverageController bridges the 'c' keybind to the coverage panel. It
// cycles the view's mode and resizes the right-column Flex so the panel
// collapses to zero height when off, handing its rows back to the plane
// list. It runs on the tview event loop (the key dispatcher fires it), so
// the Flex mutation and the CoverageView write are both safe there.
type coverageController struct {
	view        *ui.CoverageView
	rightColumn *tview.Flex
	panel       *tview.TextView
}

// CycleCoverage implements ui.CoverageController: advance the mode, then
// apply the matching panel visibility.
func (c *coverageController) CycleCoverage() {
	c.view.Cycle()
	applyCoverageVisibility(c.rightColumn, c.panel, c.view.Mode())
}

// applyCoverageVisibility resizes the coverage panel inside the right
// column: collapsed to zero when off, restored to its weight otherwise.
// Extracted so the show/hide decision is testable without the event loop.
func applyCoverageVisibility(rightColumn *tview.Flex, panel *tview.TextView, mode ui.CoverageMode) {
	if mode == ui.CoverageOff {
		rightColumn.ResizeItem(panel, 0, 0)

		return
	}

	rightColumn.ResizeItem(panel, 0, coverageWeight)
}
