package ui_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/hyperized/uAirwaves/internal/ui"
	"github.com/rivo/tview"
)

const (
	flagPollStep     = time.Millisecond
	flagPollDeadline = 2 * time.Second
	settleDelay      = 10 * time.Millisecond
	completeDeadline = 5 * time.Second
	stopCycles       = 3
)

// blockingQueuer parks in QueueUpdateDraw until release is closed,
// then runs fn and signals ran — modelling tview's "enqueue blocks
// until the event loop executes fn" contract.
type blockingQueuer struct {
	entered chan struct{}
	release chan struct{}
	ran     chan struct{}
}

func newBlockingQueuer() *blockingQueuer {
	return &blockingQueuer{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
		ran:     make(chan struct{}),
	}
}

func (b *blockingQueuer) QueueUpdateDraw(fn func()) *tview.Application {
	b.entered <- struct{}{}

	<-b.release

	fn()
	close(b.ran)

	return nil
}

// syncQueuer executes fn immediately and counts its calls.
type syncQueuer struct {
	calls atomic.Int64
}

func (s *syncQueuer) QueueUpdateDraw(fn func()) *tview.Application {
	s.calls.Add(1)
	fn()

	return nil
}

func waitFlagClear(t *testing.T, flag *atomic.Bool) {
	t.Helper()

	deadline := time.Now().Add(flagPollDeadline)
	for flag.Load() {
		if time.Now().After(deadline) {
			t.Fatal("in-flight flag not cleared before deadline")
		}

		time.Sleep(flagPollStep)
	}
}

func waitClose(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()

	select {
	case <-done:
	case <-time.After(completeDeadline):
		t.Fatalf("%s did not complete before deadline", what)
	}
}

func TestTryQueueUpdateDrawCoalescesWhileInFlight(t *testing.T) {
	t.Parallel()

	var inFlight atomic.Bool

	first := newBlockingQueuer()
	if !ui.TryQueueUpdateDraw(first, &inFlight, func() {}) {
		t.Fatal("first call: want true, got false")
	}

	// The detached goroutine has entered QueueUpdateDraw and parks
	// on release, so the in-flight flag is still set.
	<-first.entered

	if ui.TryQueueUpdateDraw(first, &inFlight, func() {}) {
		t.Fatal("second call while in-flight: want false, got true")
	}

	close(first.release)
	<-first.ran
	waitFlagClear(t, &inFlight)

	// Once the enqueue returned and the flag cleared, a fresh call
	// is accepted again.
	second := newBlockingQueuer()
	if !ui.TryQueueUpdateDraw(second, &inFlight, func() {}) {
		t.Fatal("third call after clear: want true, got false")
	}

	<-second.entered
	close(second.release)
	<-second.ran
	waitFlagClear(t, &inFlight)
}

func TestTryQueueUpdateDrawRunsFnAndClearsFlag(t *testing.T) {
	t.Parallel()

	var inFlight atomic.Bool

	queuer := &syncQueuer{}
	ran := make(chan struct{})

	if !ui.TryQueueUpdateDraw(queuer, &inFlight, func() { close(ran) }) {
		t.Fatal("call: want true, got false")
	}

	waitClose(t, ran, "fn execution")
	waitFlagClear(t, &inFlight)

	if got := queuer.calls.Load(); got != 1 {
		t.Fatalf("QueueUpdateDraw calls = %d, want 1", got)
	}
}

func TestTryQueueUpdateDrawSurvivesStopUnderRace(t *testing.T) {
	t.Parallel()

	for range stopCycles {
		runStopCycle(t)
	}
}

func runStopCycle(t *testing.T) {
	t.Helper()

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen.Init: %v", err)
	}

	app := tview.NewApplication().SetScreen(screen).SetRoot(tview.NewBox(), true)

	appDone := make(chan struct{})

	go func() {
		_ = app.Run()

		close(appDone)
	}()

	var inFlight atomic.Bool

	stop := make(chan struct{})
	hammerDone := make(chan struct{})

	go func() {
		defer close(hammerDone)

		for {
			select {
			case <-stop:
				return
			default:
				ui.TryQueueUpdateDraw(app, &inFlight, func() {})
			}
		}
	}()

	// Let a few enqueues land, then Stop() concurrently with the
	// hammering so an enqueue can be stranded mid-flight.
	time.Sleep(settleDelay)
	app.Stop()
	close(stop)

	waitClose(t, hammerDone, "hammer goroutine")
	waitClose(t, appDone, "app.Run")
}
