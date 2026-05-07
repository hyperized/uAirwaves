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
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplane"
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
	adsbStream     *adsb.ADSB
	statsTracker   *statsTracker
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
		errChan:        make(chan error, 3),
		myLocation:     myLocation,
		waitGroup:      &sync.WaitGroup{},
		planeList:      planeList,
		adsbStream:     adsb.New(buildADSBOptions(myLocation)...),
		statsTracker:   newStatsTracker(),
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
// runtime environment: WithLocation always; WithReceiverFactory
// when UAIRWAVES_REPLAY_IQ points at a capture file (which means
// pkg/adsb consumes the file instead of opening the SDR — the
// path stays bit-identical between live and replay so the same
// binary smoke-tests both modes on the device).
func buildADSBOptions(myLocation *location.Location) []adsb.Option {
	opts := []adsb.Option{adsb.WithLocation(myLocation)}

	if path := envOr("UAIRWAVES_REPLAY_IQ", ""); path != "" {
		opts = append(opts, adsb.WithReceiverFactory(func() (adsb.Receiver, error) {
			return adsb.NewFileReceiver(path)
		}))

		slog.Info("adsb: replaying from file", slog.String("path", path))
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
		return components.adsbStream.Stream(components.ctx, components.planeList)
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
					updateStatsPanel(components.statsPanel, components.adsbStream, components.statsTracker,
						components.myLocation, components.planeList)
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

// statsTracker keeps the prior tick's frame counters so the
// stats panel can derive a per-second rate without each call to
// Stats() mutating shared state.
type statsTracker struct {
	lastTotal     uint64
	lastRecovered uint64
	lastSampledAt time.Time
}

func newStatsTracker() *statsTracker {
	return &statsTracker{lastSampledAt: time.Now()}
}

// sample returns the per-second frame and recovery rate since
// the previous call, alongside the cumulative totals. The first
// call after newStatsTracker has a tiny window (now - construct
// time), so the rate is approximate until the second tick.
func (t *statsTracker) sample(stats adsb.Stats) (float64, float64) {
	now := time.Now()
	elapsed := now.Sub(t.lastSampledAt).Seconds()

	var framesPerSec, recoveredPerSec float64
	if elapsed > 0 {
		framesPerSec = float64(stats.TotalFrames-t.lastTotal) / elapsed
		recoveredPerSec = float64(stats.RecoveredFrames-t.lastRecovered) / elapsed
	}

	t.lastTotal = stats.TotalFrames
	t.lastRecovered = stats.RecoveredFrames
	t.lastSampledAt = now

	return framesPerSec, recoveredPerSec
}

// updateStatsPanel refreshes the stats panel with the latest
// ingest counters and plane-list-derived aggregates. Aircraft
// without resolved positions (lat/lon = 0) are excluded from the
// distance and altitude reductions; their absence is normal in
// the first few seconds after a position-less squitter.
func updateStatsPanel(
	statsPanel *tview.TextView,
	stream *adsb.ADSB,
	tracker *statsTracker,
	myLocation *location.Location,
	planeList *airplanes.Airplanes,
) {
	frameStats := stream.Stats()
	framesPerSec, recoveredPerSec := tracker.sample(frameStats)

	receiverLat, receiverLon := myLocation.GetCoordinates()

	tracked := planeList.Count()

	var (
		positioned                        int
		nearestDist                       = math.MaxFloat64
		farthestDist                      float64
		nearestCallsign, farthestCallsign string
		highestAlt                        float64
		highestCallsign                   string
	)

	for _, plane := range planeList.Sorted(receiverLat, receiverLon) {
		snap := plane.GetSnapshot()
		if snap.Latitude == 0 && snap.Longitude == 0 {
			continue
		}

		distance := airplanes.HaversineDistance(receiverLat, receiverLon, snap.Latitude, snap.Longitude)
		if distance == math.MaxFloat64 {
			continue
		}

		positioned++

		if distance < nearestDist {
			nearestDist = distance
			nearestCallsign = displayIdent(snap)
		}

		if distance > farthestDist {
			farthestDist = distance
			farthestCallsign = displayIdent(snap)
		}

		if snap.Altitude > highestAlt {
			highestAlt = snap.Altitude
			highestCallsign = displayIdent(snap)
		}
	}

	statsPanel.SetText(formatStatsText(statsRender{
		tracked:          tracked,
		positioned:       positioned,
		nearestDist:      nearestDist,
		nearestCallsign:  nearestCallsign,
		farthestDist:     farthestDist,
		farthestCallsign: farthestCallsign,
		highestAlt:       highestAlt,
		highestCallsign:  highestCallsign,
		framesPerSec:     framesPerSec,
		recoveredPerSec:  recoveredPerSec,
		totalFrames:      frameStats.TotalFrames,
		recoveredFrames:  frameStats.RecoveredFrames,
		callsignsDecoded: frameStats.CallsignsDecoded,
		callsignsApplied: frameStats.CallsignsApplied,
	}))
}

// statsRender packages the aggregated values formatStatsText
// renders. Hoisted to a struct so the formatter signature stays
// readable as fields accumulate.
type statsRender struct {
	tracked, positioned                int
	nearestDist, farthestDist          float64
	nearestCallsign, farthestCallsign  string
	highestAlt                         float64
	highestCallsign                    string
	framesPerSec, recoveredPerSec      float64
	totalFrames, recoveredFrames       uint64
	callsignsDecoded, callsignsApplied uint64
}

func formatStatsText(render statsRender) string {
	nearest := "—"
	if render.positioned > 0 {
		nearest = fmt.Sprintf("%.1f nm  [gray]%s[white]", render.nearestDist, render.nearestCallsign)
	}

	farthest := "—"
	if render.positioned > 0 {
		farthest = fmt.Sprintf("%.1f nm  [gray]%s[white]", render.farthestDist, render.farthestCallsign)
	}

	highest := "—"
	if render.highestAlt > 0 {
		highest = fmt.Sprintf("%.0f ft  [gray]%s[white]", render.highestAlt, render.highestCallsign)
	}

	return fmt.Sprintf(
		"[::b]Tracked[::-]    %d  ([gray]%d positioned[white])\n"+
			"[::b]Nearest[::-]    %s\n"+
			"[::b]Farthest[::-]   %s\n"+
			"[::b]Highest[::-]    %s\n"+
			"[::b]Frames/s[::-]   %.1f  ([gray]rec %.2f[white])\n"+
			"[::b]Total[::-]      %d  ([gray]IDs %d/%d[white])",
		render.tracked, render.positioned,
		nearest,
		farthest,
		highest,
		render.framesPerSec, render.recoveredPerSec,
		render.totalFrames, render.callsignsApplied, render.callsignsDecoded,
	)
}

// displayIdent picks the callsign when present, falling back to
// the bare ICAO so the stats panel always names something.
func displayIdent(snap airplane.Snapshot) string {
	if snap.Callsign != "" {
		return snap.Callsign
	}

	return snap.ICAO
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
