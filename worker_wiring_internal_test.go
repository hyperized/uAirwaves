package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/location"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/selflocate"
)

const (
	workerDeadline = 3 * time.Second
	// tickSettleDelay lets the millisecond-cadence UI loop fire several
	// ticks before a test cancels it, so the ticker.C -> tickUI path is
	// exercised deterministically.
	tickSettleDelay = 30 * time.Millisecond
)

// errInjectedLoop is the sentinel a test pushes onto errChan to drive
// runUIUpdateLoop's error-teardown branch.
var errInjectedLoop = errors.New("injected loop failure")

// newCancelledUIC builds a uiComponents with its context already
// cancelled, so any worker started against it returns on its first
// context check instead of running its real poll/stream loop. The
// cleanup cancel is idempotent.
func newCancelledUIC(t *testing.T, cfg cliConfig) *uiComponents {
	t.Helper()

	uic := configureUI(cfg)
	t.Cleanup(uic.cancel)
	uic.cancel()

	return uic
}

// waitGroupDone drains a WaitGroup with a deadline so a wedged worker
// fails the test loudly instead of hanging the suite.
func waitGroupDone(t *testing.T, uic *uiComponents) {
	t.Helper()

	done := make(chan struct{})

	go func() {
		uic.waitGroup.Wait()

		close(done)
	}()

	select {
	case <-done:
	case <-time.After(workerDeadline):
		t.Fatal("workers did not return after context cancellation")
	}
}

// TestStartBatteryWatcherAutoDiscover covers the empty-path branch:
// battery.Watch is launched and returns once the cancelled context
// stops its poll loop, without landing anything on errChan.
func TestStartBatteryWatcherAutoDiscover(t *testing.T) {
	t.Parallel()

	uic := newCancelledUIC(t, cliConfig{})

	startBatteryWatcher(uic, "")
	waitGroupDone(t, uic)

	if len(uic.errChan) != 0 {
		t.Errorf("errChan len = %d, want 0 on clean cancellation", len(uic.errChan))
	}
}

// TestStartBatteryWatcherExplicitPath covers the non-empty-path branch:
// WatchWithInterval is selected instead of Watch. The path points at a
// non-existent file so any read fails transiently and the cancelled
// context ends the loop cleanly.
func TestStartBatteryWatcherExplicitPath(t *testing.T) {
	t.Parallel()

	uic := newCancelledUIC(t, cliConfig{})
	path := filepath.Join(t.TempDir(), "uevent")

	startBatteryWatcher(uic, path)
	waitGroupDone(t, uic)

	if len(uic.errChan) != 0 {
		t.Errorf("errChan len = %d, want 0 on clean cancellation", len(uic.errChan))
	}
}

// TestStartGPSWatcher covers the GPS wiring: startGPSWatcher composes
// the fix-stamp callback and runs runGPSWatch with an explicit address.
// The cancelled context makes the reconnecting watch return without a
// live gpsd.
func TestStartGPSWatcher(t *testing.T) {
	t.Parallel()

	uic := newCancelledUIC(t, cliConfig{})

	startGPSWatcher(uic, "127.0.0.1:0")
	waitGroupDone(t, uic)

	if len(uic.errChan) != 0 {
		t.Errorf("errChan len = %d, want 0 on clean cancellation", len(uic.errChan))
	}
}

// TestRunGPSWatchDefaultsReturnOnCancel covers the address-empty and
// nil-callback branches of runGPSWatch directly. A cancelled context
// drives the reconnecting watch to a clean nil return whether or not a
// gpsd is reachable.
func TestRunGPSWatchDefaultsReturnOnCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)

	go func() {
		done <- runGPSWatch(ctx, location.New(), "", nil)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runGPSWatch = %v, want nil after cancellation", err)
		}
	case <-time.After(workerDeadline):
		t.Fatal("runGPSWatch did not return after cancellation")
	}
}

// TestStartSelfLocateWorker covers the self-locate wiring: the worker
// is launched and its ticker loop returns on the first context check.
func TestStartSelfLocateWorker(t *testing.T) {
	t.Parallel()

	uic := newCancelledUIC(t, cliConfig{})

	startSelfLocateWorker(uic)
	waitGroupDone(t, uic)

	if len(uic.errChan) != 0 {
		t.Errorf("errChan len = %d, want 0 on clean cancellation", len(uic.errChan))
	}
}

// TestStartADSBStreamer covers the ADSB streamer wiring. A replay source
// pointed at a non-existent file makes Stream fail its first open and
// return the error, which LaunchWorker forwards to errChan — proving the
// wrapper wires the stream, worker supervision, and error channel
// together.
func TestStartADSBStreamer(t *testing.T) {
	t.Parallel()

	uic := newCancelledUIC(t, cliConfig{replayIQPath: "/nonexistent/uairwaves-test.iq"})

	startADSBStreamer(uic)
	waitGroupDone(t, uic)

	select {
	case err := <-uic.errChan:
		if err == nil {
			t.Error("errChan delivered a nil error; want the failed replay open")
		}
	default:
		t.Error("startADSBStreamer did not forward the replay-open failure to errChan")
	}
}

// TestStartADSBStreamerOpensReplay covers the replay receiver factory's
// success return: a readable file opens cleanly, so the factory hands
// back a receiver before the cancelled context stops the read. The
// non-existent-file sibling test covers the factory's error return.
func TestStartADSBStreamerOpensReplay(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "capture.iq")
	if err := os.WriteFile(path, []byte{0, 0}, 0o600); err != nil {
		t.Fatalf("write replay fixture: %v", err)
	}

	uic := newCancelledUIC(t, cliConfig{replayIQPath: path})

	startADSBStreamer(uic)
	waitGroupDone(t, uic)
}

// TestStartUIUpdater covers the startUIUpdater wrapper: it registers the
// redraw loop on the WaitGroup, and the loop returns on the pre-cancelled
// context.
func TestStartUIUpdater(t *testing.T) {
	t.Parallel()

	uic := newCancelledUIC(t, cliConfig{})

	startUIUpdater(uic)
	waitGroupDone(t, uic)
}

// TestRunSelfLocateSkipsWhenEstimateNotReady covers the not-ready branch:
// with GPS stale but the locator empty, every tick's Estimate returns
// false, so the worker skips without ever touching myLocation.
func TestRunSelfLocateSkipsWhenEstimateNotReady(t *testing.T) {
	t.Parallel()

	loc := location.New()
	locator := selflocate.New()

	var slot atomic.Pointer[time.Time]

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)

		runSelfLocate(ctx, loc, locator, &slot, time.Millisecond, time.Hour)
	}()

	time.Sleep(tickSettleDelay)
	cancel()
	<-done

	if lat, lon := loc.GetCoordinates(); lat != 0 || lon != 0 {
		t.Errorf("location updated from an empty locator: (%v, %v)", lat, lon)
	}
}

// TestRunSelfLocateDedupsRepeatEstimate covers the dedup branch: once a
// fix is applied, a subsequent identical estimate is skipped rather than
// re-written into myLocation. The fixture yields a deterministic estimate,
// so every tick after the first repeats it. feedSelfLocateFixture is the
// shared seeder from the self-locate wiring tests.
func TestRunSelfLocateDedupsRepeatEstimate(t *testing.T) {
	t.Parallel()

	loc := location.New()
	locator := selflocate.New()
	feedSelfLocateFixture(locator)

	var slot atomic.Pointer[time.Time]

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)

		runSelfLocate(ctx, loc, locator, &slot, time.Millisecond, time.Hour)
	}()

	deadline := time.Now().Add(workerDeadline)

	for {
		if lat, lon := loc.GetCoordinates(); lat != 0 || lon != 0 {
			break
		}

		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("no fix applied before deadline")
		}

		time.Sleep(time.Millisecond)
	}

	firstLat, firstLon := loc.GetCoordinates()

	// Let several more ticks fire so the repeated identical estimate
	// exercises the dedup skip, then stop.
	time.Sleep(tickSettleDelay)
	cancel()
	<-done

	if lat, lon := loc.GetCoordinates(); lat != firstLat || lon != firstLon {
		t.Errorf("dedup failed: coords changed from (%v, %v) to (%v, %v)", firstLat, firstLon, lat, lon)
	}
}

// TestRunUIUpdateLoopReturnsOnCancel covers the context-done branch: a
// pre-cancelled context ends the loop without tearing anything down.
func TestRunUIUpdateLoopReturnsOnCancel(t *testing.T) {
	t.Parallel()

	uic := newCancelledUIC(t, cliConfig{})

	done := make(chan struct{})

	go func() {
		runUIUpdateLoop(uic, time.Millisecond)

		close(done)
	}()

	select {
	case <-done:
	case <-time.After(workerDeadline):
		t.Fatal("runUIUpdateLoop did not return on a cancelled context")
	}
}

// TestRunUIUpdateLoopTearsDownOnError covers the errChan branch: an error
// on the shared channel makes the loop cancel the context, stop the app,
// and return.
func TestRunUIUpdateLoopTearsDownOnError(t *testing.T) {
	t.Parallel()

	uic := configureUI(cliConfig{})
	t.Cleanup(uic.cancel)

	uic.errChan <- errInjectedLoop

	done := make(chan struct{})

	go func() {
		runUIUpdateLoop(uic, uiUpdateInterval)

		close(done)
	}()

	select {
	case <-done:
	case <-time.After(workerDeadline):
		t.Fatal("runUIUpdateLoop did not return after an errChan failure")
	}

	if uic.ctx.Err() == nil {
		t.Error("errChan teardown did not cancel the context")
	}
}

// TestRunUIUpdateLoopRunsTickPath covers the ticker branch: with a live
// context, a running simulation-screen app, and a millisecond cadence, a
// tick fires and hands off to tickUI before the context is cancelled.
func TestRunUIUpdateLoopRunsTickPath(t *testing.T) {
	t.Parallel()

	uic := newRenderableUIC(t)

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen.Init: %v", err)
	}

	uic.app.SetScreen(screen).SetRoot(uic.grid, true)

	appDone := make(chan struct{})

	go func() {
		_ = uic.app.Run()

		close(appDone)
	}()

	loopDone := make(chan struct{})

	go func() {
		runUIUpdateLoop(uic, time.Millisecond)

		close(loopDone)
	}()

	// Let several ticks fire so the ticker.C -> tickUI path runs, then
	// cancel to unwind the loop.
	time.Sleep(tickSettleDelay)
	uic.cancel()

	select {
	case <-loopDone:
	case <-time.After(workerDeadline):
		t.Fatal("runUIUpdateLoop did not return after cancellation")
	}

	uic.app.Stop()

	select {
	case <-appDone:
	case <-time.After(workerDeadline):
		t.Fatal("app.Run did not return after Stop")
	}
}

// TestRunUIUpdateLoopRecoversTickPanic covers the deferred recover: a nil
// ADSB stream makes tickUI panic on the first tick (Sweeping dereferences
// the nil receiver). The recover must cancel the context, stop the app,
// and let the goroutine exit rather than aborting the process.
func TestRunUIUpdateLoopRecoversTickPanic(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	uic := &uiComponents{
		ctx:     ctx,
		cancel:  cancel,
		app:     tview.NewApplication(),
		errChan: make(chan error, 1),
	}

	done := make(chan struct{})

	go func() {
		runUIUpdateLoop(uic, time.Millisecond)

		close(done)
	}()

	select {
	case <-done:
	case <-time.After(workerDeadline):
		t.Fatal("runUIUpdateLoop did not recover the tick panic and return")
	}

	if ctx.Err() == nil {
		t.Error("panic recovery did not cancel the context")
	}
}
