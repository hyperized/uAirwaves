package ui_test

import (
	"sync"
	"testing"

	"github.com/hyperized/uAirwaves/internal/ui"
)

const icaoFixtureC = "CCC003"

func TestSelectionOpenAtMapsIndexToICAO(t *testing.T) {
	t.Parallel()

	sel := ui.NewSelection()
	sel.SetICAOs([]string{icaoFixtureA, icaoFixtureB, icaoFixtureC})

	sel.OpenAt(1)

	if got := sel.ICAO(); got != icaoFixtureB {
		t.Errorf("ICAO after OpenAt(1) = %q, want %q", got, icaoFixtureB)
	}

	if !sel.IsOpen() {
		t.Error("Selection should report IsOpen after OpenAt")
	}
}

func TestSelectionOpenAtOutOfRangeIsNoOp(t *testing.T) {
	t.Parallel()

	sel := ui.NewSelection()
	sel.SetICAOs([]string{icaoFixtureA})

	sel.OpenAt(5)

	if sel.IsOpen() {
		t.Error("OpenAt out-of-range should not open the panel")
	}

	if got := sel.ICAO(); got != "" {
		t.Errorf("out-of-range open should leave ICAO empty, got %q", got)
	}

	sel.OpenAt(-1)

	if sel.IsOpen() {
		t.Error("OpenAt(-1) should not open the panel")
	}
}

func TestSelectionCloseDoesNotForgetICAO(t *testing.T) {
	t.Parallel()

	sel := ui.NewSelection()
	sel.SetICAOs([]string{icaoFixtureA})
	sel.OpenAt(0)
	sel.Close()

	if sel.IsOpen() {
		t.Error("Close should hide the panel")
	}

	if got := sel.ICAO(); got != icaoFixtureA {
		t.Errorf("Close should keep ICAO for re-open, got %q", got)
	}
}

func TestSelectionICAOAtIndexReturnsMappedICAO(t *testing.T) {
	t.Parallel()

	sel := ui.NewSelection()
	sel.SetICAOs([]string{icaoFixtureA, icaoFixtureB, icaoFixtureC})

	if got := sel.ICAOAtIndex(1); got != icaoFixtureB {
		t.Errorf("ICAOAtIndex(1) = %q, want %q", got, icaoFixtureB)
	}
}

func TestSelectionICAOAtIndexOutOfRangeReturnsEmpty(t *testing.T) {
	t.Parallel()

	sel := ui.NewSelection()
	sel.SetICAOs([]string{icaoFixtureA})

	if got := sel.ICAOAtIndex(5); got != "" {
		t.Errorf("ICAOAtIndex past end = %q, want empty", got)
	}

	if got := sel.ICAOAtIndex(-1); got != "" {
		t.Errorf("ICAOAtIndex(-1) = %q, want empty", got)
	}
}

func TestSelectionConcurrentAccess(t *testing.T) {
	t.Parallel()

	sel := ui.NewSelection()
	sel.SetICAOs([]string{icaoFixtureA, icaoFixtureB})

	var waitGroup sync.WaitGroup
	for range 50 {
		waitGroup.Go(func() {
			sel.SetICAOs([]string{"XXX111", "YYY222"})
			sel.OpenAt(0)
			sel.Close()
		})
		waitGroup.Go(func() {
			_ = sel.IsOpen()
			_ = sel.ICAO()
		})
	}

	waitGroup.Wait()
}
