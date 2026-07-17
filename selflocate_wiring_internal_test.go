package main

import (
	"context"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hyperized/uAirwaves/pkg/location"
	"github.com/hyperized/uAirwaves/pkg/selflocate"
)

// TestGPSIsFreshNilSlotReportsStale covers the nil-slot
// short-circuit: nothing tracks freshness yet, self-locate is
// free to push.
func TestGPSIsFreshNilSlotReportsStale(t *testing.T) {
	t.Parallel()

	if gpsIsFresh(nil, time.Minute) {
		t.Error("gpsIsFresh(nil slot) = true, want false")
	}
}

// TestGPSIsFreshUnsetPointerReportsStale covers the
// pointer-is-nil branch: slot exists but nothing has stamped it,
// so GPS has never produced a fix.
func TestGPSIsFreshUnsetPointerReportsStale(t *testing.T) {
	t.Parallel()

	var slot atomic.Pointer[time.Time]

	if gpsIsFresh(&slot, time.Minute) {
		t.Error("gpsIsFresh with unset pointer = true, want false")
	}
}

// TestGPSIsFreshRecentStampReportsFresh and TestGPSIsFreshStale
// pin the two boundary cases of the freshness predicate.
func TestGPSIsFreshRecentStampReportsFresh(t *testing.T) {
	t.Parallel()

	var slot atomic.Pointer[time.Time]

	now := time.Now()
	slot.Store(&now)

	if !gpsIsFresh(&slot, time.Minute) {
		t.Error("gpsIsFresh with now-stamp = false, want true")
	}
}

func TestGPSIsFreshStaleStampReportsStale(t *testing.T) {
	t.Parallel()

	var slot atomic.Pointer[time.Time]

	ancient := time.Now().Add(-time.Hour)
	slot.Store(&ancient)

	if gpsIsFresh(&slot, time.Minute) {
		t.Error("gpsIsFresh with hour-old stamp = true, want false")
	}
}

// TestStampLastFixWritesToSlot covers the callback returned by
// stampLastFix: invoking it stamps the slot with the supplied
// instant.
func TestStampLastFixWritesToSlot(t *testing.T) {
	t.Parallel()

	var slot atomic.Pointer[time.Time]

	callback := stampLastFix(&slot)
	if callback == nil {
		t.Fatal("stampLastFix returned nil for a non-nil slot")
	}

	mark := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	callback(mark)

	got := slot.Load()
	if got == nil {
		t.Fatal("slot is still nil after callback invocation")
	}

	if !got.Equal(mark) {
		t.Errorf("slot stored %v, want %v", *got, mark)
	}
}

// TestStampLastFixNilSlotReturnsNil pins the defensive nil-slot
// path: passing nil yields nil, so callers can use the helper
// unconditionally and gps.WithFixCallback(nil) gates the hook.
func TestStampLastFixNilSlotReturnsNil(t *testing.T) {
	t.Parallel()

	if cb := stampLastFix(nil); cb != nil {
		t.Error("stampLastFix(nil) returned a non-nil callback")
	}
}

// TestRunSelfLocateAppliesEstimateWhenGPSStale drives the worker
// against a Locator pre-seeded with enough synthetic
// observations to clear the readiness gates, with no GPS
// freshness stamp. After one tick the worker must push a fix
// into the supplied Location.
func TestRunSelfLocateAppliesEstimateWhenGPSStale(t *testing.T) {
	t.Parallel()

	loc := location.New()
	locator := selflocate.New()
	feedSelfLocateFixture(locator)

	var slot atomic.Pointer[time.Time]

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)

		runSelfLocate(ctx, loc, locator, &slot, 10*time.Millisecond, time.Minute)
	}()

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()

	for {
		lat, lon := loc.GetCoordinates()
		if lat != 0 || lon != 0 {
			cancel()
			<-done

			if got := loc.Source(); got != location.SourceInferred {
				t.Errorf("Source() = %d after self-locate push, want SourceInferred (%d)",
					got, location.SourceInferred)
			}

			if got := loc.ConfidenceRadiusNm(); got <= 0 {
				t.Errorf("ConfidenceRadiusNm() = %f after self-locate push, want > 0", got)
			}

			return
		}

		select {
		case <-deadline.C:
			cancel()
			<-done
			t.Fatal("runSelfLocate did not push a fix within the deadline")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestRunSelfLocateDefersWhileGPSFresh confirms the worker
// honours a fresh GPS stamp: locator has plenty of observations,
// but slot says GPS just delivered a fix, so myLocation must
// remain untouched.
func TestRunSelfLocateDefersWhileGPSFresh(t *testing.T) {
	t.Parallel()

	loc := location.New()
	locator := selflocate.New()
	feedSelfLocateFixture(locator)

	var slot atomic.Pointer[time.Time]

	now := time.Now()
	slot.Store(&now)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)

		runSelfLocate(ctx, loc, locator, &slot, 10*time.Millisecond, time.Hour)
	}()

	// Give the worker time to tick a few times; it must not
	// push anything because GPS is stamped fresh.
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	if lat, lon := loc.GetCoordinates(); lat != 0 || lon != 0 {
		t.Errorf("location updated despite fresh GPS stamp: (%v, %v)", lat, lon)
	}
}

// feedSelfLocateFixture drives 60 Observe calls around (52, 4)
// so the locator passes its readiness gates. The fixtures sit on
// a half-degree ring around the receiver — every fix lands well
// inside its own horizon, so the locator converges.
func feedSelfLocateFixture(locator *selflocate.Locator) {
	const (
		receiverLat   = 52.0
		receiverLon   = 4.0
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

		locator.Observe(receiverLat+dLat, receiverLon+dLon, altFt)
	}
}
