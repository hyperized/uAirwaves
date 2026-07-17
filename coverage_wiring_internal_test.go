package main

import (
	"math"
	"testing"

	"github.com/hyperized/uAirwaves/internal/ui"
	"github.com/hyperized/uAirwaves/pkg/adsb"
	"github.com/hyperized/uAirwaves/pkg/coverage"
	"github.com/hyperized/uAirwaves/pkg/location"
	"github.com/hyperized/uAirwaves/pkg/selflocate"
	"github.com/rivo/tview"
)

// feedObserverRing pushes a convergent ring of synthetic fixes through the
// observer so a wired self-locate estimator can resolve a receiver
// position. The ring sits half a degree around (52, 4); the fixes only
// reach the coverage tracker when the observer's location has a real fix,
// so callers control that independently.
func feedObserverRing(observe adsb.PositionObserver) {
	const (
		centreLat     = 52.0
		centreLon     = 4.0
		obsCount      = 60
		ringDegrees   = 0.5
		fullCircleDeg = 360
	)

	altitudes := []float64{1500, 5000, 12000, 25000, 38000}

	for index := range obsCount {
		altFt := altitudes[index%len(altitudes)]
		bearingRad := (float64(index) / float64(obsCount)) * fullCircleDeg * math.Pi / 180
		dLat := ringDegrees * math.Cos(bearingRad)
		dLon := ringDegrees * math.Sin(bearingRad)

		observe(centreLat+dLat, centreLon+dLon, altFt)
	}
}

// TestPositionObserverNilWhenNoConsumers pins the no-op case: with neither
// a self-locator nor a tracker, no hook is installed.
func TestPositionObserverNilWhenNoConsumers(t *testing.T) {
	t.Parallel()

	if positionObserver(location.New(), nil, nil) != nil {
		t.Error("positionObserver with no consumers should be nil")
	}
}

// TestPositionObserverFeedsSelfLocatorNotTrackerWhenReceiverUnknown proves
// the fan-out feeds the self-locate estimator regardless of receiver state
// while dropping coverage fixes until a receiver position is known.
func TestPositionObserverFeedsSelfLocatorNotTrackerWhenReceiverUnknown(t *testing.T) {
	t.Parallel()

	loc := location.New() // (0,0): receiver unknown
	locator := selflocate.New()
	tracker := coverage.New()

	observe := positionObserver(loc, locator, tracker)
	feedObserverRing(observe)

	if _, ok := locator.Estimate(); !ok {
		t.Error("self-locator did not converge; observer did not feed it")
	}

	if snap := tracker.Snapshot(); snap.MaxRangeNm != 0 {
		t.Errorf("tracker was fed with an unknown receiver: MaxRangeNm=%v", snap.MaxRangeNm)
	}
}

// TestPositionObserverFeedsTrackerWhenReceiverKnown proves a fix reaches
// the coverage tracker (distance/bearing/altitude) once the receiver has a
// real position, and that a nil self-locator is skipped cleanly.
func TestPositionObserverFeedsTrackerWhenReceiverKnown(t *testing.T) {
	t.Parallel()

	const (
		receiverLat = 52.0
		receiverLon = 4.0
		planeLat    = 52.5
		planeAltFt  = 35000.0
	)

	loc := location.New()
	loc.Update(location.WithLatitude(receiverLat), location.WithLongitude(receiverLon))

	tracker := coverage.New()

	observe := positionObserver(loc, nil, tracker)
	observe(planeLat, receiverLon, planeAltFt)

	snap := tracker.Snapshot()
	if snap.MaxRangeNm == 0 {
		t.Fatal("tracker recorded no range for a known receiver")
	}

	if snap.MaxRangeAltFt != planeAltFt {
		t.Errorf("tracker MaxRangeAltFt = %v, want %v", snap.MaxRangeAltFt, planeAltFt)
	}
}

// TestPositionObserverSkipsSentinelDistance proves a null-island plane
// (0,0) is dropped rather than clamped into the last distance bin, honouring
// HaversineDistance's MaxFloat64 sentinel.
func TestPositionObserverSkipsSentinelDistance(t *testing.T) {
	t.Parallel()

	loc := location.New()
	loc.Update(location.WithLatitude(52.0), location.WithLongitude(4.0))

	tracker := coverage.New()

	observe := positionObserver(loc, nil, tracker)
	observe(0, 0, 35000)

	if snap := tracker.Snapshot(); snap.MaxRangeNm != math.MaxFloat64 && snap.MaxRangeNm != 0 {
		t.Errorf("tracker recorded a sentinel-distance plane: MaxRangeNm=%v", snap.MaxRangeNm)
	}
}

// TestPositionObserverTrackerNilStopsAfterSelfLocator covers the tracker-nil
// early return: the self-locator is still fed, and the observer returns
// without touching a coverage tracker.
func TestPositionObserverTrackerNilStopsAfterSelfLocator(t *testing.T) {
	t.Parallel()

	locator := selflocate.New()

	observe := positionObserver(location.New(), locator, nil)
	feedObserverRing(observe)

	if _, ok := locator.Estimate(); !ok {
		t.Error("self-locator did not converge with a nil tracker")
	}
}

// TestCoverageControllerCycleAdvancesMode drives the 'c' adapter through the
// full cone -> shadows -> off -> cone cycle, exercising both panel-visibility
// arms. The right-column Flex is real so the ResizeItem calls run without a
// tview event loop.
func TestCoverageControllerCycleAdvancesMode(t *testing.T) {
	t.Parallel()

	view := ui.NewCoverageView()
	panel := tview.NewTextView()
	rightColumn := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(panel, 0, coverageWeight, false)
	ctrl := &coverageController{view: view, rightColumn: rightColumn, panel: panel}

	for _, want := range []ui.CoverageMode{ui.CoverageShadows, ui.CoverageOff, ui.CoverageCone} {
		ctrl.CycleCoverage()

		if got := view.Mode(); got != want {
			t.Errorf("after CycleCoverage mode = %v, want %v", got, want)
		}
	}
}

// TestRenderCoverageOffIsNoOp covers the collapsed-panel branch: with the
// coverage view cycled to off, renderCoverage returns without repainting, so
// the panel keeps whatever it last held (the 'c' handler already resized it
// to zero height).
func TestRenderCoverageOffIsNoOp(t *testing.T) {
	t.Parallel()

	uic := newRenderableUIC(t)
	uic.coverageView.Cycle() // cone -> shadows
	uic.coverageView.Cycle() // shadows -> off
	uic.coveragePanel.SetText("stale")

	renderCoverage(uic)

	if got := uic.coveragePanel.GetText(true); got != "stale" {
		t.Errorf("renderCoverage(off) repainted the panel: %q", got)
	}
}
