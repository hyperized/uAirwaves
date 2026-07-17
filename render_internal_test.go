package main

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/hyperized/uAirwaves/pkg/radar"
)

const (
	renderPollStep = time.Millisecond
	renderDeadline = 2 * time.Second
)

// newRenderableUIC builds a uiComponents wired exactly as main does
// before it enters the event loop: configureUI, the radar panel, and
// the grid (which populates leftPages). The context is cancelled on
// cleanup so no test leaks the background context. The app is
// constructed but not run — renderUI mutates widgets directly and does
// not need the event loop.
func newRenderableUIC(t *testing.T) *uiComponents {
	t.Helper()

	uic := configureUI(cliConfig{})
	t.Cleanup(uic.cancel)

	uic.radarPanel = radar.New(uic.planeList, uic.myLocation, uic.adsbStream)
	uic.grid = configureGrid(uic)

	return uic
}

// frontPage returns the name of the currently-shown left-column page.
func frontPage(t *testing.T, uic *uiComponents) string {
	t.Helper()

	name, _ := uic.leftPages.GetFrontPage()

	return name
}

// TestRenderUIWithClosedSelectionShowsRadar drives a full renderUI frame
// against empty domain state. With the selection closed (the default),
// renderFlightDetails must leave the radar page in front and the frame
// must complete without touching a nil widget.
func TestRenderUIWithClosedSelectionShowsRadar(t *testing.T) {
	t.Parallel()

	uic := newRenderableUIC(t)

	renderUI(uic)

	if got := frontPage(t, uic); got != leftPageRadar {
		t.Errorf("front page = %q, want %q", got, leftPageRadar)
	}
}

// TestRenderFlightDetailsOpenWithoutPick covers the "panel open but no
// plane chosen yet" branch: IsOpen true, ICAO empty. The details page
// comes to the front and the renderer draws its nil-snapshot hint.
func TestRenderFlightDetailsOpenWithoutPick(t *testing.T) {
	t.Parallel()

	uic := newRenderableUIC(t)
	uic.selection.SetICAOs([]string{""})
	uic.selection.OpenAt(0)

	renderFlightDetails(uic)

	if got := frontPage(t, uic); got != leftPageDetails {
		t.Errorf("front page = %q, want %q", got, leftPageDetails)
	}
}

// TestRenderFlightDetailsPrunedPlane covers the "selected plane no
// longer tracked" branch: the ICAO is open but absent from the live
// list, so the panel shows the pruned placeholder.
func TestRenderFlightDetailsPrunedPlane(t *testing.T) {
	t.Parallel()

	uic := newRenderableUIC(t)
	uic.selection.SetICAOs([]string{"ABCDEF"})
	uic.selection.OpenAt(0)

	renderFlightDetails(uic)

	if got := frontPage(t, uic); got != leftPageDetails {
		t.Fatalf("front page = %q, want %q", got, leftPageDetails)
	}

	if body := uic.flightDetailsText.GetText(true); !strings.Contains(body, "no longer tracked") {
		t.Errorf("details text = %q, want the pruned-plane placeholder", body)
	}
}

// TestRenderFlightDetailsTrackedPlane covers the happy branch: the open
// ICAO resolves to a live plane, so its snapshot feeds both the text
// panel and the mini-scope. The placeholder must be gone.
func TestRenderFlightDetailsTrackedPlane(t *testing.T) {
	t.Parallel()

	uic := newRenderableUIC(t)

	const icao = "C0FFEE"

	uic.planeList.Ensure(icao)
	uic.selection.SetICAOs([]string{icao})
	uic.selection.OpenAt(0)

	renderFlightDetails(uic)

	if got := frontPage(t, uic); got != leftPageDetails {
		t.Fatalf("front page = %q, want %q", got, leftPageDetails)
	}

	if body := uic.flightDetailsText.GetText(true); strings.Contains(body, "no longer tracked") {
		t.Errorf("details text shows the pruned placeholder for a tracked plane: %q", body)
	}
}

// TestTickUIResetsCadenceAndSkipsShutdownEnqueue covers two branches in
// one call: a desired cadence that differs from the current one resets
// the ticker, and a cancelled context short-circuits before any redraw
// is enqueued. Passing the sweep interval as "current" while the stream
// is idle forces the reset; the pre-cancelled context forces the skip.
func TestTickUIResetsCadenceAndSkipsShutdownEnqueue(t *testing.T) {
	t.Parallel()

	uic := newRenderableUIC(t)
	uic.cancel()

	ticker := time.NewTicker(sweepUpdateInterval)
	defer ticker.Stop()

	var drawInFlight atomic.Bool

	got := tickUI(uic, ticker, sweepUpdateInterval, &drawInFlight)
	if got != uiUpdateInterval {
		t.Errorf("returned interval = %v, want %v (idle cadence)", got, uiUpdateInterval)
	}

	if drawInFlight.Load() {
		t.Error("a redraw was enqueued during shutdown; want none")
	}
}

// TestTickUIEnqueuesRedraw covers the live path: with the context alive
// and a running app, tickUI enqueues one coalesced redraw. The app runs
// on a simulation screen so the enqueue drains (renderUI executes on the
// event loop) and the in-flight guard clears, confirming the frame ran.
func TestTickUIEnqueuesRedraw(t *testing.T) {
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

	ticker := time.NewTicker(uiUpdateInterval)
	defer ticker.Stop()

	var drawInFlight atomic.Bool

	if got := tickUI(uic, ticker, uiUpdateInterval, &drawInFlight); got != uiUpdateInterval {
		t.Errorf("returned interval = %v, want %v", got, uiUpdateInterval)
	}

	waitDrawDrained(t, &drawInFlight)

	uic.app.Stop()

	select {
	case <-appDone:
	case <-time.After(renderDeadline):
		t.Fatal("app.Run did not return after Stop")
	}
}

// waitDrawDrained blocks until the coalescing guard clears, i.e. the
// enqueued redraw has run on the event loop.
func waitDrawDrained(t *testing.T, flag *atomic.Bool) {
	t.Helper()

	deadline := time.Now().Add(renderDeadline)
	for flag.Load() {
		if time.Now().After(deadline) {
			t.Fatal("redraw enqueue did not drain before deadline")
		}

		time.Sleep(renderPollStep)
	}
}
