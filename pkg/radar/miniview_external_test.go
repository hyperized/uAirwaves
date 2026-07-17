package radar_test

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/hyperized/uAirwaves/pkg/airplane"
	"github.com/hyperized/uAirwaves/pkg/location"
	"github.com/hyperized/uAirwaves/pkg/radar"
)

const (
	miniBoxWidth  = 40
	miniBoxHeight = 20
)

// newMiniScreen returns an initialised SimulationScreen sized to
// the mini view's box so projected cells land inside the buffer.
//
//nolint:ireturn // tcell's constructor only hands back the SimulationScreen interface; there is no concrete type.
func newMiniScreen(t *testing.T) tcell.SimulationScreen {
	t.Helper()

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen.Init: %v", err)
	}

	screen.SetSize(miniBoxWidth, miniBoxHeight)

	return screen
}

// screenHasRune reports whether want appears as the leading rune of
// any cell. Uses SimulationScreen.GetContents so it stays off the
// deprecated Screen.GetContent path.
func screenHasRune(screen tcell.SimulationScreen, want rune) bool {
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

// TestMiniViewDrawRendersPlaneAndTrail drives the full MiniView
// render: a positioned plane with a one-entry trail centres the
// scope, and the compass, plane marker and trail bullet must all
// land on the screen. The trail entry sits north of the plane so
// its '•' occupies a distinct cell from the plane '+'.
func TestMiniViewDrawRendersPlaneAndTrail(t *testing.T) {
	t.Parallel()

	loc := location.New(location.WithLatitude(52.0), location.WithLongitude(13.0))
	view := radar.NewMiniView(loc)
	view.SetRect(0, 0, miniBoxWidth, miniBoxHeight)

	view.SetSnapshot(airplane.Snapshot{
		Callsign:  "MINI01",
		Latitude:  52.05,
		Longitude: 13.05,
		Altitude:  30000,
		Heading:   -1, // invalid -> no heading line to confuse the trail assertion
		PositionHistory: []airplane.PositionEntry{
			{Latitude: 52.08, Longitude: 13.05, Altitude: 30000},
		},
	})

	screen := newMiniScreen(t)
	view.Draw(screen)
	screen.Show()

	if !screenContains(screen, "N") {
		t.Error("mini compass 'N' not rendered")
	}

	if !screenHasRune(screen, '+') {
		t.Error("plane '+' marker not rendered")
	}

	if !screenHasRune(screen, '•') {
		t.Error("trail '•' bullet not rendered")
	}
}

// TestMiniViewDrawZeroSizeIsNoOp covers the early return when the
// inner box has no area: nothing is painted even with a plane held.
func TestMiniViewDrawZeroSizeIsNoOp(t *testing.T) {
	t.Parallel()

	loc := location.New(location.WithLatitude(52.0), location.WithLongitude(13.0))
	view := radar.NewMiniView(loc)
	view.SetRect(0, 0, 0, 0)
	view.SetSnapshot(airplane.Snapshot{Latitude: 52.05, Longitude: 13.05})

	screen := newMiniScreen(t)
	view.Draw(screen)
	screen.Show()

	if screenHasRune(screen, '+') {
		t.Error("plane rendered despite zero-size inner box")
	}

	if screenContains(screen, "N") {
		t.Error("compass rendered despite zero-size inner box")
	}
}

// TestMiniViewDrawWithoutSnapshotDrawsScopeOnly covers the no-snap
// return: the scope chrome (compass) and the receiver crosshair are
// drawn, but no plane marker is. With no plane the scope centres on
// the receiver, so its 'X' lands at the centre cell.
func TestMiniViewDrawWithoutSnapshotDrawsScopeOnly(t *testing.T) {
	t.Parallel()

	loc := location.New(location.WithLatitude(52.0), location.WithLongitude(13.0))
	view := radar.NewMiniView(loc)
	view.SetRect(0, 0, miniBoxWidth, miniBoxHeight)

	screen := newMiniScreen(t)
	view.Draw(screen)
	screen.Show()

	if !screenContains(screen, "N") {
		t.Error("mini compass 'N' not rendered without a snapshot")
	}

	if !screenHasRune(screen, 'X') {
		t.Error("receiver 'X' crosshair not rendered without a snapshot")
	}

	if screenHasRune(screen, '+') {
		t.Error("plane '+' rendered despite no snapshot")
	}
}

// TestMiniViewDrawWithZeroPositionSnapshot covers the second half
// of the final guard: a held snapshot whose position is the (0,0)
// sentinel draws the scope chrome but no plane, because the plane
// half of the pipeline is skipped for an unresolved fix.
func TestMiniViewDrawWithZeroPositionSnapshot(t *testing.T) {
	t.Parallel()

	loc := location.New(location.WithLatitude(52.0), location.WithLongitude(13.0))
	view := radar.NewMiniView(loc)
	view.SetRect(0, 0, miniBoxWidth, miniBoxHeight)
	view.SetSnapshot(airplane.Snapshot{ICAO: "ZERO"})

	screen := newMiniScreen(t)
	view.Draw(screen)
	screen.Show()

	if !screenContains(screen, "N") {
		t.Error("mini compass 'N' not rendered with a zero-position snapshot")
	}

	if screenHasRune(screen, '+') {
		t.Error("plane '+' rendered for a zero-position snapshot")
	}
}
