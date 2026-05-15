package ui_test

import (
	"sync"
	"testing"

	"lab.hyperized.net/hyperized/uAirwaves/internal/ui"
)

func TestPlaneFilterDefaultsPositionedOnly(t *testing.T) {
	t.Parallel()

	filter := ui.NewPlaneFilter()

	if !filter.PositionedOnly() {
		t.Error("expected NewPlaneFilter() to default to positioned-only")
	}
}

func TestPlaneFilterToggle(t *testing.T) {
	t.Parallel()

	filter := ui.NewPlaneFilter()

	filter.TogglePositionedOnly()

	if filter.PositionedOnly() {
		t.Error("after first toggle, expected positioned-only = false")
	}

	filter.TogglePositionedOnly()

	if !filter.PositionedOnly() {
		t.Error("after second toggle, expected positioned-only = true")
	}
}

// TestPlaneFilterConcurrentAccess exercises the mutex by hammering
// Toggle/PositionedOnly from multiple goroutines under -race.
func TestPlaneFilterConcurrentAccess(t *testing.T) {
	t.Parallel()

	filter := ui.NewPlaneFilter()

	const iterations = 1_000

	var waitGroup sync.WaitGroup

	waitGroup.Add(2) //nolint:mnd // one reader goroutine, one writer goroutine.

	go func() {
		defer waitGroup.Done()

		for range iterations {
			filter.TogglePositionedOnly()
		}
	}()

	go func() {
		defer waitGroup.Done()

		for range iterations {
			_ = filter.PositionedOnly()
		}
	}()

	waitGroup.Wait()
}
