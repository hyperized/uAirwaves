package radar_test

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplane"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplanes"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/location"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/radar"
)

func TestNew(t *testing.T) {
	t.Parallel()

	planes := airplanes.New()
	loc := location.New()
	view := radar.New(planes, loc)

	if view == nil {
		t.Fatal("expected New() to return a non-nil View")
	}

	if view.GetScopeRange() != 20 { // default scope min is 20
		t.Errorf("expected default scope range 20, got %f", view.GetScopeRange())
	}

	if !view.GetHeadingIndicatorEnabled() {
		t.Error("expected heading indicator to be enabled by default")
	}

	if !view.GetTrailIndicatorEnabled() {
		t.Error("expected trail indicator to be enabled by default")
	}

	if view.GetHeatIndicatorEnabled() {
		t.Error("expected heat indicator to be disabled by default")
	}

	if !view.GetAutoScopeEnabled() {
		t.Error("expected auto scope to be enabled by default")
	}
}

func TestView_SetScopeRange(t *testing.T) {
	t.Parallel()

	view := radar.New(airplanes.New(), location.New())
	view.SetScopeRange(100)

	if view.GetScopeRange() != 100 {
		t.Errorf("expected scope range 100, got %f", view.GetScopeRange())
	}
}

func TestView_IncrementDecrementScope(t *testing.T) {
	t.Parallel()

	view := radar.New(airplanes.New(), location.New())
	initialRange := view.GetScopeRange()

	view.IncrementScope()

	if view.GetScopeRange() <= initialRange {
		t.Errorf("expected scope range to increase, got %f", view.GetScopeRange())
	}

	increasedRange := view.GetScopeRange()
	view.DecrementScope()

	if view.GetScopeRange() >= increasedRange {
		t.Errorf("expected scope range to decrease, got %f", view.GetScopeRange())
	}
}

func TestView_Toggles(t *testing.T) {
	t.Parallel()

	view := radar.New(airplanes.New(), location.New())

	initialHeading := view.GetHeadingIndicatorEnabled()
	view.ToggleHeadingIndicator()

	if view.GetHeadingIndicatorEnabled() == initialHeading {
		t.Error("ToggleHeadingIndicator() failed to change state")
	}

	initialAutoScope := view.GetAutoScopeEnabled()
	view.ToggleAutoScope()

	if view.GetAutoScopeEnabled() == initialAutoScope {
		t.Error("ToggleAutoScope() failed to change state")
	}

	// Trail and heat toggles live alongside the heading/autoScope
	// pair. Same contract: each call flips the flag, and the
	// matching getter reflects it. Default-on per radar.New —
	// flipping once must reach false.
	if !view.GetTrailIndicatorEnabled() {
		t.Error("trail indicator should default to enabled")
	}

	view.ToggleTrailIndicator()

	if view.GetTrailIndicatorEnabled() {
		t.Error("ToggleTrailIndicator() failed to flip to disabled")
	}

	view.ToggleTrailIndicator()

	if !view.GetTrailIndicatorEnabled() {
		t.Error("ToggleTrailIndicator() failed to flip back to enabled")
	}

	if view.GetHeatIndicatorEnabled() {
		t.Error("heat indicator should default to disabled")
	}

	view.ToggleHeatIndicator()

	if !view.GetHeatIndicatorEnabled() {
		t.Error("ToggleHeatIndicator() failed to flip to enabled")
	}

	view.ToggleHeatIndicator()

	if view.GetHeatIndicatorEnabled() {
		t.Error("ToggleHeatIndicator() failed to flip back to disabled")
	}
}

// TestView_GetAircraftCount makes sure GetAircraftCount delegates
// to airplanes.Count and reflects insertions. No fancy plumbing
// needed — Ensure adds an entry, the getter must observe it.
func TestView_GetAircraftCount(t *testing.T) {
	t.Parallel()

	planes := airplanes.New()
	view := radar.New(planes, location.New())

	if got := view.GetAircraftCount(); got != 0 {
		t.Errorf("initial GetAircraftCount() = %d, want 0", got)
	}

	planes.Ensure("AAA111")
	planes.Ensure("BBB222")

	if got := view.GetAircraftCount(); got != 2 {
		t.Errorf("after two Ensure: GetAircraftCount() = %d, want 2", got)
	}
}

// TestView_Draw_TrailRendersHistory exercises the drawTrail code
// path: a plane with a populated PositionHistory whose entries
// all fall inside scope must produce at least one '·' cell on
// the simulation screen. The plane's own '+' glyph is drawn last
// for its own position, so we count '·' separately — the trail
// is the only producer of that rune in the radar package outside
// the heading indicator (off here).
//
// We use airplane.WithPosition iteratively, manipulating no
// internal state — the pure public surface drives the trail.
//
//nolint:funlen // assembling fixtures + 4 deterministic poses leaves nothing to extract usefully.
func TestView_Draw_TrailRendersHistory(t *testing.T) {
	t.Parallel()

	loc := location.New(location.WithLatitude(52.0), location.WithLongitude(13.0))
	planes := airplanes.New()
	view := radar.New(planes, loc)
	view.SetRect(0, 0, 80, 24)

	// Disable auto-scope so the scope range stays at the default
	// 20nm. Heat already defaults off; trail defaults on. Heading
	// defaults on too, but WithHeading clamps -1 to 0 so a "no
	// heading" plane would still spray a northward line of '·'
	// across the screen — kill heading explicitly so the only
	// source of '·' is the trail.
	view.ToggleAutoScope()
	view.ToggleHeadingIndicator()

	planes.Ensure("TRAILY")

	plane, ok := planes.Get("TRAILY")
	if !ok {
		t.Fatal("plane not found after Ensure")
	}

	// First WithPosition always appends (lastPositionTime is zero).
	// Then move the plane to a *different* lat/lon via the bare
	// setters so the history entry sits visibly behind the '+'
	// glyph on the screen — otherwise '+' would overwrite the
	// single trail dot at the same grid cell.
	plane.Update(airplane.WithPosition(52.01, 13.01))
	plane.Update(
		airplane.WithLatitude(52.05),
		airplane.WithLongitude(13.05),
		airplane.WithAltitude(30000),
		airplane.WithHeading(-1), // explicit invalid -> no heading line
	)

	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}

	screen.SetSize(80, 24)
	view.Draw(screen)

	const trailDot = '·'

	var found bool

	for posY := range 24 {
		for posX := range 80 {
			cellStr, _, _ := screen.Get(posX, posY)
			if []rune(cellStr)[0] == trailDot {
				found = true

				break
			}
		}

		if found {
			break
		}
	}

	if !found {
		t.Error("trail '·' not found anywhere on the simulation screen after Draw")
	}
}

// TestView_Draw_HeatRenders enables the heat overlay and drives
// one plane through Draw, asserting that at least one of the heat
// glyphs ░▒▓ ends up on the simulation screen. Heat defaults off
// since the toggle-default refactor, so without an explicit test
// that flips it on the heatmap.draw path goes uncovered.
func TestView_Draw_HeatRenders(t *testing.T) {
	t.Parallel()

	loc := location.New(location.WithLatitude(52.0), location.WithLongitude(13.0))
	planes := airplanes.New()
	view := radar.New(planes, loc)
	view.SetRect(0, 0, 80, 24)
	view.ToggleAutoScope()        // pin scope at 20nm
	view.ToggleHeadingIndicator() // keep the screen clear of heading dots
	view.ToggleTrailIndicator()   // and trail dots
	view.ToggleHeatIndicator()    // turn heat ON

	planes.Ensure("HOTONE")

	plane, _ := planes.Get("HOTONE")
	plane.Update(
		airplane.WithLatitude(52.05),
		airplane.WithLongitude(13.05),
		airplane.WithAltitude(30000),
		airplane.WithHeading(-1),
	)

	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}

	screen.SetSize(80, 24)
	view.Draw(screen)

	for row := range 24 {
		for col := range 80 {
			mainc, _, _, _ := screen.GetContent(col, row) //nolint:staticcheck
			if mainc == '░' || mainc == '▒' || mainc == '▓' {
				return
			}
		}
	}

	t.Error("no heat glyph (░▒▓) found on screen after Draw with heat enabled")
}

// TestView_Draw_TrailSkipsZeroPositionEntries exercises the
// `entry.Latitude == 0 || entry.Longitude == 0` continue branch
// in drawTrail. A history entry at (0, 0) is the unresolved-fix
// sentinel and must not produce a screen dot — otherwise stale
// pre-CPR-resolution states would smear across the scope centre.
//
// We can't drive history through the public WithPosition path
// without time control, so we use an aux package-test fixture
// that places a plane with a non-zero current position plus a
// fake history that includes one (0, 0) entry; the test passes
// when at most one '·' (for the non-zero history entry) renders
// — the (0, 0) one is dropped.
//
// We have no public seam for "set history"; the next best move is
// to drive Draw with a plane whose only history fix is at (0,0).
// drawTrail must take the `continue` and emit no dot. We assert
// the screen has no '·' anywhere — '+' at the plane position is
// the only glyph from the plane itself in this configuration.
func TestView_Draw_TrailSkipsZeroPositionEntries(t *testing.T) {
	t.Parallel()

	loc := location.New(location.WithLatitude(52.0), location.WithLongitude(13.0))
	planes := airplanes.New()
	view := radar.New(planes, loc)
	view.SetRect(0, 0, 80, 24)
	view.ToggleAutoScope()
	view.ToggleHeadingIndicator() // -1 clamps to 0 in WithHeading; kill the line explicitly.

	planes.Ensure("ZEROHIST")

	plane, ok := planes.Get("ZEROHIST")
	if !ok {
		t.Fatal("plane not found after Ensure")
	}

	// Seed the history with one (0, 0) entry via the public path:
	// WithPosition(0, 0) appends and clamps to (0, 0). Then move
	// the plane to a real position via WithLatitude/Longitude so
	// the '+' glyph lands inside scope.
	plane.Update(airplane.WithPosition(0, 0))
	plane.Update(
		airplane.WithLatitude(52.05),
		airplane.WithLongitude(13.05),
		airplane.WithAltitude(30000),
		airplane.WithHeading(-1),
	)

	// Sanity: the snapshot must report exactly one history entry
	// at (0, 0). If WithPosition's behaviour changes this assertion
	// becomes the canary.
	hist := plane.GetSnapshot().PositionHistory
	if len(hist) != 1 || hist[0].Latitude != 0 || hist[0].Longitude != 0 {
		t.Fatalf("test fixture broken: history = %+v", hist)
	}

	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}

	screen.SetSize(80, 24)
	view.Draw(screen)

	const trailDot = '·'

	for posY := range 24 {
		for posX := range 80 {
			cellStr, _, _ := screen.Get(posX, posY)
			if []rune(cellStr)[0] == trailDot {
				t.Errorf("found trail '·' at (%d, %d) despite history entry at (0, 0)", posX, posY)
			}
		}
	}
}

// TestView_Draw_HeadingLineRenders exercises drawHeadingLine via
// the public Draw path: enable headings, give the plane a valid
// heading, and check the screen for at least one '·' dot. Trail
// is off (it defaults on, so we toggle), and the plane is placed
// inside scope so it actually gets drawn.
func TestView_Draw_HeadingLineRenders(t *testing.T) {
	t.Parallel()

	loc := location.New(location.WithLatitude(52.0), location.WithLongitude(13.0))
	planes := airplanes.New()
	view := radar.New(planes, loc)
	view.SetRect(0, 0, 80, 24)
	view.ToggleAutoScope()      // pin scope at 20nm
	view.ToggleTrailIndicator() // off, so '·' is heading-only (heading defaults on)

	planes.Ensure("HEADED")

	plane, ok := planes.Get("HEADED")
	if !ok {
		t.Fatal("plane not found after Ensure")
	}

	plane.Update(
		airplane.WithLatitude(52.01),
		airplane.WithLongitude(13.01),
		airplane.WithAltitude(30000),
		airplane.WithHeading(90), // east, valid (!= -1)
	)

	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}

	screen.SetSize(80, 24)
	view.Draw(screen)

	const headingDot = '·'

	for posY := range 24 {
		for posX := range 80 {
			cellStr, _, _ := screen.Get(posX, posY)
			if []rune(cellStr)[0] == headingDot {
				return
			}
		}
	}

	t.Error("heading-line '·' not found anywhere on the simulation screen after Draw")
}

func TestView_Draw(t *testing.T) {
	t.Parallel()

	planes := airplanes.New()
	loc := location.New(location.WithLatitude(52.0), location.WithLongitude(13.0))
	view := radar.New(planes, loc)

	// Add a plane within scope
	planes.Ensure("PLANE1")
	plane, _ := planes.Get("PLANE1")
	// Roughly 5nm away
	plane.Update(
		airplane.WithLatitude(52.05),
		airplane.WithLongitude(13.05),
		airplane.WithAltitude(35000),
		airplane.WithVertRate(1000),
		airplane.WithHeading(90),
	)

	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}

	view.SetRect(0, 0, 80, 24)
	view.Draw(screen)

	// Verify something was drawn
	// The center point 'X' should be around (40, 12)
	mainChar, _, _, _ := screen.GetContent(40, 12) //nolint:staticcheck,dogsled
	if mainChar != 'X' {
		t.Errorf("expected center character X, got %c", mainChar)
	}
}

func TestView_Draw_EdgeCases(t *testing.T) {
	t.Parallel()

	loc := location.New(location.WithLatitude(52.0), location.WithLongitude(13.0))

	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}

	testPlaneOutsideScopeIncreasesRange(t, loc, screen)
	testAutoScopeDisabled(t, loc, screen)
	testNoPlanesLeavesScope(t, loc, screen)
	testAutoScopeShrinksToFarthest(t, loc, screen)
	testPlaneWithoutLocationSkipped(t, loc, screen)
	testPlaneWithCallsign(t, loc, screen)
	testPlaneRendersInsideBox(t, loc)
}

func testPlaneOutsideScopeIncreasesRange(t *testing.T, loc *location.Location, screen tcell.Screen) {
	t.Helper()
	t.Run("plane outside scope increases range", func(t *testing.T) {
		t.Parallel()

		planes := airplanes.New()
		view := radar.New(planes, loc)
		view.SetRect(0, 0, 80, 24)

		planes.Ensure("OUTSIDE")
		plane, _ := planes.Get("OUTSIDE")
		plane.Update(
			airplane.WithLatitude(53.0),
			airplane.WithLongitude(13.0),
		)

		initialRange := view.GetScopeRange()
		view.Draw(screen)

		if view.GetScopeRange() <= initialRange {
			t.Errorf("expected autoScope to increase range, stayed at %f", view.GetScopeRange())
		}
	})
}

func testAutoScopeDisabled(t *testing.T, loc *location.Location, screen tcell.Screen) {
	t.Helper()
	t.Run("autoScope disabled does not increase range", func(t *testing.T) {
		t.Parallel()

		planes := airplanes.New()
		view := radar.New(planes, loc)
		view.SetRect(0, 0, 80, 24)

		view.ToggleAutoScope() // Disable autoScope
		planes.Ensure("FAR")
		plane, _ := planes.Get("FAR")
		plane.Update(
			airplane.WithLatitude(55.0),
			airplane.WithLongitude(13.0),
		)

		initialRange := view.GetScopeRange()
		view.Draw(screen)

		if view.GetScopeRange() != initialRange {
			t.Errorf("expected range to stay %f, got %f", initialRange, view.GetScopeRange())
		}
	})
}

func testNoPlanesLeavesScope(t *testing.T, loc *location.Location, screen tcell.Screen) {
	t.Helper()
	t.Run("autoscope leaves scope unchanged when no plane has a position", func(t *testing.T) {
		t.Parallel()

		newPlanes := airplanes.New()
		newView := radar.New(newPlanes, loc)
		newView.SetScopeRange(100)
		newView.SetRect(0, 0, 80, 24)

		newView.Draw(screen)

		if got := newView.GetScopeRange(); got != 100 {
			t.Errorf("expected autoscope to leave range at 100 with no planes, got %f", got)
		}

		newView.ToggleAutoScope()
		newView.SetScopeRange(100)
		newView.Draw(screen)

		if got := newView.GetScopeRange(); got != 100 {
			t.Errorf("expected range to stay at 100 with autoScope disabled, got %f", got)
		}
	})
}

func testAutoScopeShrinksToFarthest(t *testing.T, loc *location.Location, screen tcell.Screen) {
	t.Helper()
	t.Run("autoscope shrinks to fit the farthest plane", func(t *testing.T) {
		t.Parallel()

		planes := airplanes.New()
		view := radar.New(planes, loc)
		view.SetScopeRange(500) // start pinned at max as if previously far traffic
		view.SetRect(0, 0, 80, 24)

		// Plane at lat=52 + 2deg → ~120 nm north of (52, 13).
		// Expect autoscope to round up to next 20 nm increment: 120 nm.
		planes.Ensure("NEAR")
		plane, _ := planes.Get("NEAR")
		plane.Update(
			airplane.WithLatitude(54.0),
			airplane.WithLongitude(13.0),
		)

		view.Draw(screen)

		const want = 120.0
		if got := view.GetScopeRange(); got != want {
			t.Errorf("expected autoscope to shrink to %f, got %f", want, got)
		}
	})
}

func testPlaneWithoutLocationSkipped(t *testing.T, loc *location.Location, screen tcell.Screen) {
	t.Helper()
	t.Run("plane without location skipped", func(t *testing.T) {
		t.Parallel()

		planes := airplanes.New()
		view := radar.New(planes, loc)
		view.SetRect(0, 0, 80, 24)

		planes.Ensure("NOLOC")
		plane, _ := planes.Get("NOLOC")
		plane.Update(airplane.WithLatitude(0), airplane.WithLongitude(0))

		// Should not panic or error
		view.Draw(screen)
	})
}

// testPlaneRendersInsideBox proves a positioned plane lands on
// screen instead of being clipped above/below the inner box. The
// previous yMultiplier=1 doubled vertical offsets relative to the
// scope rings; for one plane the autoscope fit just past the
// plane's distance, so the plane sat off-screen vertically while
// remaining listed in the sidebar.
func testPlaneRendersInsideBox(t *testing.T, loc *location.Location) {
	t.Helper()
	t.Run("plane renders inside inner box at autoscoped edge", func(t *testing.T) {
		t.Parallel()

		const (
			boxWidth  = 80
			boxHeight = 24
		)

		planes := airplanes.New()
		view := radar.New(planes, loc)
		view.SetRect(0, 0, boxWidth, boxHeight)

		// Plane ~60 nm due north — autoscope fits to ceil(60/20)*20 = 60.
		planes.Ensure("NORTH1")
		plane, _ := planes.Get("NORTH1")
		plane.Update(
			airplane.WithLatitude(53.0),
			airplane.WithLongitude(13.0),
			airplane.WithCallsign("NORTHX"),
		)

		// Fresh screen for this case so other subtests don't pollute
		// the cell grid we're about to inspect.
		isolated := tcell.NewSimulationScreen("")
		if err := isolated.Init(); err != nil {
			t.Fatal(err)
		}

		view.Draw(isolated)
		isolated.Show()

		if !screenContains(isolated, "NORTHX") {
			t.Fatal("plane callsign 'NORTHX' not found anywhere on the simulation screen — clipped off the inner box")
		}
	})
}

// screenContains reports whether the cell grid spells out target
// horizontally anywhere on screen. Cheap helper, used to assert
// that callsigns landed inside the visible inner box.
func screenContains(screen tcell.Screen, target string) bool {
	width, height := screen.Size()

	for row := range height {
		line := make([]rune, 0, width)

		for col := range width {
			// tcell.Screen.GetContent is still the cell-by-cell read
			// API on Screen; the replacement Get() the deprecation
			// notice hints at is a SimulationScreen-only helper.
			mainc, _, _, _ := screen.GetContent(col, row) //nolint:staticcheck
			line = append(line, mainc)
		}

		if strings.Contains(string(line), target) {
			return true
		}
	}

	return false
}

func testPlaneWithCallsign(t *testing.T, loc *location.Location, screen tcell.Screen) {
	t.Helper()
	t.Run("plane with callsign", func(t *testing.T) {
		t.Parallel()

		testPlanes := airplanes.New()
		testView := radar.New(testPlanes, loc)
		testView.SetRect(0, 0, 80, 24)

		testPlanes.Ensure("ICAO456")
		plane, _ := testPlanes.Get("ICAO456")
		plane.Update(
			airplane.WithCallsign("TEST456"),
			airplane.WithLatitude(52.01),
			airplane.WithLongitude(13.01),
		)

		testView.Draw(screen)
	})
}
