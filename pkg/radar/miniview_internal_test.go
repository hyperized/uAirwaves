package radar

import (
	"math"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/hyperized/uAirwaves/pkg/airplane"
	"github.com/hyperized/uAirwaves/pkg/location"
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

// TestMiniCenterPicksPlaneWhenAvailable covers the centering
// rule: a snapshot with a resolved position drives the centre;
// no-snap or (0,0) sentinel falls back to the receiver.
func TestMiniCenterPicksPlaneWhenAvailable(t *testing.T) {
	t.Parallel()

	snap := airplane.Snapshot{Latitude: 51.0, Longitude: 5.0}

	gotLat, gotLon := miniCenter(snap, true, 52.0, 4.0)
	if gotLat != snap.Latitude || gotLon != snap.Longitude {
		t.Errorf("with snap: centre = (%v, %v), want (%v, %v) (plane)",
			gotLat, gotLon, snap.Latitude, snap.Longitude)
	}

	gotLat, gotLon = miniCenter(airplane.Snapshot{}, false, 52.0, 4.0)
	if gotLat != 52.0 || gotLon != 4.0 {
		t.Errorf("no snap: centre = (%v, %v), want (52, 4) (receiver fallback)", gotLat, gotLon)
	}

	gotLat, gotLon = miniCenter(airplane.Snapshot{}, true, 52.0, 4.0)
	if gotLat != 52.0 || gotLon != 4.0 {
		t.Errorf("snap-without-position: centre = (%v, %v), want (52, 4) (still falls back)", gotLat, gotLon)
	}
}

// TestMiniViewManualScopeDisablesAutoFit pins the implicit-off
// rule: IncrementScope / DecrementScope must flip autoScope off
// so the next Draw doesn't immediately overwrite the operator's
// manual range.
func TestMiniViewManualScopeDisablesAutoFit(t *testing.T) {
	t.Parallel()

	view := NewMiniView(location.New())

	if !view.GetAutoScopeEnabled() {
		t.Fatal("precondition: MiniView should construct with autoScope=true")
	}

	view.IncrementScope()

	if view.GetAutoScopeEnabled() {
		t.Error("IncrementScope must disable autoScope so the manual range sticks")
	}

	view.ToggleAutoScope() // back on for the next assertion

	if !view.GetAutoScopeEnabled() {
		t.Fatal("ToggleAutoScope should re-enable autoScope")
	}

	view.DecrementScope()

	if view.GetAutoScopeEnabled() {
		t.Error("DecrementScope must disable autoScope so the manual range sticks")
	}
}

// TestMiniViewIncrementDecrementMoveScope confirms +/- actually
// shift the scope range in the expected direction, not just flip
// the autoScope flag. Starts from a few increments above the
// scope's min so a single Decrement actually has headroom to
// shrink into.
func TestMiniViewIncrementDecrementMoveScope(t *testing.T) {
	t.Parallel()

	view := NewMiniView(location.New())
	view.ToggleAutoScope() // off so the manual range sticks
	view.IncrementScope()
	view.IncrementScope()

	afterIncrement := view.GetScopeRange()

	view.DecrementScope()

	if got := view.GetScopeRange(); got >= afterIncrement {
		t.Errorf("DecrementScope should shrink the range; before=%v after=%v", afterIncrement, got)
	}
}

// TestScopeForFrameRespectsAutoScopeFlag pins the resolver:
// auto-on draws miniScopeRange (and persists it back into the
// shared scope so the operator sees a sensible value when they
// switch to manual); auto-off uses the stored scope verbatim.
func TestScopeForFrameRespectsAutoScopeFlag(t *testing.T) {
	t.Parallel()

	view := NewMiniView(location.New())

	snap := airplane.Snapshot{Latitude: 52.5, Longitude: 4.5}

	autoFit := view.scopeForFrame(snap, true, true, 52.0, 4.0)
	if autoFit < miniMinScopeNm {
		t.Errorf("auto-on resolved range = %v, want >= %v", autoFit, miniMinScopeNm)
	}

	view.ToggleAutoScope() // off
	view.myScope.Update()  // leave defaults

	manual := view.scopeForFrame(snap, true, false, 52.0, 4.0)
	if got := view.GetScopeRange(); manual != got {
		t.Errorf("auto-off resolver should return the stored range; resolved=%v stored=%v", manual, got)
	}

	// auto-on with no snap returns the floor.
	floor := view.scopeForFrame(airplane.Snapshot{}, false, true, 52.0, 4.0)
	if floor != miniMinScopeNm {
		t.Errorf("auto-on no-snap = %v, want floor %v", floor, miniMinScopeNm)
	}
}

// TestDrawMiniReceiverBranches pins the three drawMiniReceiver
// paths directly: an unresolved (0,0) receiver and an out-of-scope
// receiver both paint nothing, while a resolved receiver inside
// the scope paints the 'X' crosshair. Driving the helper directly
// makes the two skip branches deterministic without orchestrating
// receiver-vs-centre geometry through the full Draw pipeline.
func TestDrawMiniReceiverBranches(t *testing.T) {
	t.Parallel()

	const (
		centerX, centerY = 40, 20
		scale            = 4.0
	)

	cases := []struct {
		name       string
		recvLat    float64
		recvLon    float64
		scopeRange float64
		wantX      bool
	}{
		{name: "unresolved receiver skips", recvLat: 0, recvLon: 0, scopeRange: 20, wantX: false},
		{name: "out-of-scope receiver skips", recvLat: 53.0, recvLon: 13.0, scopeRange: 5, wantX: false},
		{name: "in-scope receiver draws X", recvLat: 52.05, recvLon: 13.0, scopeRange: 20, wantX: true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			screen := tcell.NewSimulationScreen("UTF-8")
			if err := screen.Init(); err != nil {
				t.Fatalf("screen.Init: %v", err)
			}

			screen.SetSize(80, 40)
			drawMiniReceiver(screen, testCase.recvLat, testCase.recvLon, airportFrame{
				centerX: centerX, centerY: centerY,
				xScale: scale, yScale: scale,
				centerLat: 52.0, centerLon: 13.0,
				scopeRange: testCase.scopeRange,
			})
			screen.Show()

			if got := anyCellHasRune(screen, 'X'); got != testCase.wantX {
				t.Errorf("receiver X present = %v, want %v", got, testCase.wantX)
			}
		})
	}
}

// anyCellHasRune reports whether any cell on the simulation screen
// holds want as its leading rune. Scans the public GetContents()
// buffer so it dodges the deprecated Screen.GetContent.
func anyCellHasRune(screen tcell.SimulationScreen, want rune) bool {
	cells, width, height := screen.GetContents()

	for row := range height {
		for col := range width {
			cell := cells[row*width+col]
			if len(cell.Runes) > 0 && cell.Runes[0] == want {
				return true
			}
		}
	}

	return false
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
