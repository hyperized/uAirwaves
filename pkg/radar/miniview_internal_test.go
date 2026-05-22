package radar

import (
	"math"
	"testing"

	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplane"
)

func TestMiniScopeRangeFloorsAtMinimum(t *testing.T) {
	t.Parallel()

	got := miniScopeRange(airplane.Snapshot{}, 52, 4)
	if got != miniMinScopeNm {
		t.Errorf("empty snapshot should yield %v nm floor, got %v", miniMinScopeNm, got)
	}
}

func TestMiniScopeRangeRoundsUpToIncrement(t *testing.T) {
	t.Parallel()

	// Pick a position approximately 7 nm away. After 1.2x padding
	// = 8.4, round up to the next 5 nm step = 10 nm.
	const oneDegreeNm = 60.0

	snap := airplane.Snapshot{
		Latitude:  52.0 + 7.0/oneDegreeNm,
		Longitude: 4.0,
	}

	got := miniScopeRange(snap, 52.0, 4.0)
	if math.Abs(got-10.0) > 0.01 {
		t.Errorf("7 nm plane should round to 10 nm scope, got %v", got)
	}
}

func TestMiniScopeRangeIncludesTrail(t *testing.T) {
	t.Parallel()

	// Plane is close, but a trail entry sits 20 nm away. The
	// auto-fit must cover the farthest trail entry, not just the
	// current fix, so the operator sees the full historical track.
	const oneDegreeNm = 60.0

	snap := airplane.Snapshot{
		Latitude:  52.0 + 1.0/oneDegreeNm,
		Longitude: 4.0,
		PositionHistory: []airplane.PositionEntry{
			{Latitude: 52.0 + 20.0/oneDegreeNm, Longitude: 4.0},
		},
	}

	got := miniScopeRange(snap, 52.0, 4.0)

	// 20 * 1.2 = 24, rounded up to 25.
	if math.Abs(got-25.0) > 0.01 {
		t.Errorf("trail-only entry at 20 nm should expand scope to 25 nm, got %v", got)
	}
}

func TestNauticalMilesFromCenterSkipsUnresolved(t *testing.T) {
	t.Parallel()

	if got := nauticalMilesFromCenter(52, 4, 0, 0); got != 0 {
		t.Errorf("unresolved (0,0) should return 0, got %v", got)
	}
}

func TestMiniScalesLockToTheConstrainedAxis(t *testing.T) {
	t.Parallel()

	// A very wide-but-short box (50 cols × 6 rows) at a 10 nm
	// scope. xRaw = 50/20 = 2.5; yRaw = 6/20 * 2 = 0.6. Both
	// scales must lock to the smaller (yRaw), so rings stay
	// visually circular instead of stretching horizontally.
	xScale, yScale := miniScales(50, 6, 10.0)

	if math.Abs(xScale-yScale) > 1e-9 {
		t.Errorf("xScale=%v yScale=%v should be equal (uniform), differ by %v",
			xScale, yScale, xScale-yScale)
	}

	if math.Abs(xScale-0.6) > 0.01 {
		t.Errorf("uniform scale = %v, want ~0.6 (locked to the more constrained yRaw)", xScale)
	}
}

func TestMiniViewSetSnapshotAndClearAreThreadSafe(t *testing.T) {
	t.Parallel()

	view := NewMiniView(nil)

	// Just exercise the locking — race detector covers the
	// rest. We don't need a real Location for this surface.
	view.SetSnapshot(airplane.Snapshot{ICAO: "ABC123"})

	view.mu.RLock()
	got := view.snap.ICAO
	hasSnap := view.hasSnap
	view.mu.RUnlock()

	if got != "ABC123" || !hasSnap {
		t.Errorf("SetSnapshot did not persist; ICAO=%q hasSnap=%v", got, hasSnap)
	}

	view.Clear()

	view.mu.RLock()
	hasSnap = view.hasSnap
	view.mu.RUnlock()

	if hasSnap {
		t.Error("Clear did not flip hasSnap to false")
	}
}
