package radar

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// TestHeatMapAddClampsToOne locks the add() saturation behaviour:
// once a cell reaches 1.0, further hits must not push it past.
// Without the clamp the cell would overflow the [0, 1] domain
// heatRune / heatColor expect and the brightest bucket would
// linger forever instead of decaying naturally.
func TestHeatMapAddClampsToOne(t *testing.T) {
	t.Parallel()

	heat := newHeatMap()
	for range 10 {
		heat.add(0, 0)
	}

	heat.mu.Lock()
	defer heat.mu.Unlock()

	got := heat.cells[heatKey{x: 0, y: 0}]
	if got != 1.0 {
		t.Errorf("cell value after saturation = %v, want 1.0", got)
	}
}

// TestHeatMapDecayEvictsBelowMinVisible locks the decay() eviction
// branch: cells whose value falls below heatMinVisible are deleted
// from the map. Without that path the map would grow unbounded
// over a long session as cool-and-still cells accumulate.
func TestHeatMapDecayEvictsBelowMinVisible(t *testing.T) {
	t.Parallel()

	heat := newHeatMap()

	// Seed a cell, then backdate lastDecay so the next decay()
	// call applies a multi-half-life factor and drives the value
	// below heatMinVisible. Five half-lives -> factor ≈ 0.031 ->
	// 0.5 * 0.031 = 0.0155 < 0.04 = heatMinVisible. Done.
	heat.add(0, 0)

	heat.mu.Lock()
	heat.lastDecay = time.Now().Add(-5 * heatHalfLife * time.Second)
	heat.mu.Unlock()

	heat.decay()

	heat.mu.Lock()
	defer heat.mu.Unlock()

	if _, ok := heat.cells[heatKey{x: 0, y: 0}]; ok {
		t.Error("expected cell to be evicted after decay below heatMinVisible; still present")
	}
}

func TestGetVerticalRateColor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		vertRate float64
		want     tcell.Color
	}{
		{600, tcell.ColorGreen},
		{-600, tcell.ColorRed},
		{0, tcell.ColorLightBlue},
		{500, tcell.ColorLightBlue},
		{-500, tcell.ColorLightBlue},
	}

	for _, testCase := range tests {
		t.Run(testCase.want.String(), func(t *testing.T) {
			t.Parallel()

			if got := getVerticalRateColor(testCase.vertRate); got != testCase.want {
				t.Errorf("getVerticalRateColor(%f) = %v, want %v", testCase.vertRate, got, testCase.want)
			}
		})
	}
}

func TestGetVertRateSymbol(t *testing.T) {
	t.Parallel()

	tests := []struct {
		vertRate float64
		want     string
	}{
		{600, "^"},
		{200, "+"},
		{-600, "v"},
		{-200, "-"},
		{0, "="},
		{100, "="},
		{-100, "="},
	}

	for _, testCase := range tests {
		t.Run(testCase.want, func(t *testing.T) {
			t.Parallel()

			if got := getVertRateSymbol(testCase.vertRate); got != testCase.want {
				t.Errorf("getVertRateSymbol(%f) = %s, want %s", testCase.vertRate, got, testCase.want)
			}
		})
	}
}

// TestHeatRune locks the rune-bucket boundaries: high (▓), mid
// (▒), low (░) match the heatHighThreshold / heatLowThreshold
// constants. Values that fall exactly on a threshold belong in
// the lower bucket because the comparisons in heatRune use `>`,
// not `>=`.
func TestHeatRune(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		heat float64
		want rune
	}{
		{name: "above high → solid block", heat: heatHighThreshold + 0.1, want: '▓'},
		{name: "exactly high → mid (>, not >=)", heat: heatHighThreshold, want: '▒'},
		{name: "above low → mid block", heat: heatLowThreshold + 0.1, want: '▒'},
		{name: "exactly low → light (>, not >=)", heat: heatLowThreshold, want: '░'},
		{name: "below low → light block", heat: 0.1, want: '░'},
		{name: "zero → light block", heat: 0.0, want: '░'},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := heatRune(testCase.heat); got != testCase.want {
				t.Errorf("heatRune(%v) = %q, want %q", testCase.heat, got, testCase.want)
			}
		})
	}
}

// TestHeatColor mirrors TestHeatRune for the colour bucket. The
// thresholds are shared, so the boundary semantics match: exact
// hits land in the lower bucket.
func TestHeatColor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		heat float64
		want tcell.Color
	}{
		{name: "above high → aqua", heat: heatHighThreshold + 0.1, want: tcell.ColorAqua},
		{name: "exactly high → teal (>, not >=)", heat: heatHighThreshold, want: tcell.ColorTeal},
		{name: "above low → teal", heat: heatLowThreshold + 0.1, want: tcell.ColorTeal},
		{name: "exactly low → navy (>, not >=)", heat: heatLowThreshold, want: tcell.ColorNavy},
		{name: "below low → navy", heat: 0.1, want: tcell.ColorNavy},
		{name: "zero → navy", heat: 0.0, want: tcell.ColorNavy},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := heatColor(testCase.heat); got != testCase.want {
				t.Errorf("heatColor(%v) = %v, want %v", testCase.heat, got, testCase.want)
			}
		})
	}
}

// TestDrawHeadingLineWritesDots drives drawHeadingLine through a
// SimulationScreen and asserts that the dotted-line rendering puts
// at least one '·' cell on the expected side of the origin. Four
// cardinal headings cover the four sin/cos sign combinations, so
// every direction the screen-coord conversion can produce is
// reached. We don't pin exact (x, y) values — those depend on the
// Bresenham-like stepping inside the function — but we do confirm
// the dots land in the expected quadrant.
func TestDrawHeadingLineWritesDots(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		heading float64
		// dxSign / dySign: required sign of (foundX-origin) /
		// (foundY-origin) for the dot to count as "in the expected
		// direction". 0 means either is fine.
		dxSign int
		dySign int
	}{
		{name: "north points up", heading: 0, dxSign: 0, dySign: -1},
		{name: "east points right", heading: 90, dxSign: 1, dySign: 0},
		{name: "south points down", heading: 180, dxSign: 0, dySign: 1},
		{name: "west points left", heading: 270, dxSign: -1, dySign: 0},
	}

	const (
		width    = 80
		height   = 40
		originX  = width / 2
		originY  = height / 2
		dotRune  = '·'
		styleFg  = tcell.ColorWhite
		styleBgB = tcell.ColorBlack
	)

	style := tcell.StyleDefault.Foreground(styleFg).Background(styleBgB)

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			screen := tcell.NewSimulationScreen("")
			if err := screen.Init(); err != nil {
				t.Fatalf("screen init: %v", err)
			}

			screen.SetSize(width, height)
			drawHeadingLine(screen, originX, originY, testCase.heading, style)

			if !hasDirectionalDot(screen, originX, originY, width, height, dotRune, testCase.dxSign, testCase.dySign) {
				t.Errorf("heading=%v: no '%c' dot found in expected quadrant", testCase.heading, dotRune)
			}
		})
	}
}

// hasDirectionalDot scans the simulation screen for at least one
// occurrence of want at a coordinate matching the requested
// directional signs from (originX, originY). dx/dySign==0 means
// either sign is acceptable. Helper for TestDrawHeadingLine.
//
//nolint:revive // argument-limit: SimulationScreen scanning needs all of these inputs.
func hasDirectionalDot(
	screen tcell.SimulationScreen, originX, originY, width, height int, want rune, dxSign, dySign int,
) bool {
	for posY := range height {
		for posX := range width {
			cellStr, _, _ := screen.Get(posX, posY)
			if []rune(cellStr)[0] != want {
				continue
			}

			if !signMatches(posX-originX, dxSign) || !signMatches(posY-originY, dySign) {
				continue
			}

			return true
		}
	}

	return false
}

func signMatches(delta, want int) bool {
	if want == 0 {
		return true
	}

	if want > 0 {
		return delta > 0
	}

	return delta < 0
}

func TestGetFlightLevelColor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		altitude float64
		want     tcell.Color
	}{
		{400, tcell.ColorWhite},
		{5000, tcell.ColorYellow},
		{15000, tcell.ColorGreen},
		{25000, tcell.ColorLightBlue},
		{35000, tcell.ColorDarkBlue},
		{45000, tcell.ColorPurple},
		{55000, tcell.ColorRed},
		{65000, tcell.ColorWhite},
	}

	for _, testCase := range tests {
		t.Run(testCase.want.String(), func(t *testing.T) {
			t.Parallel()

			if got := getFlightLevelColor(testCase.altitude); got != testCase.want {
				t.Errorf("getFlightLevelColor(%f) = %v, want %v", testCase.altitude, got, testCase.want)
			}
		})
	}
}

// TestSpinnerOffsetWalksEightPositions pins the clockwise sweep
// order N → NE → E → SE → S → SW → W → NW. Each row picks a wall
// clock instant that lands at a known frame index so the test
// asserts the (dx, dy) pair the radar will draw at that moment.
//
// The radar's drawCenterPoint feeds time.Now() into spinnerOffset
// at draw time; this test pins the time → offset mapping so a
// future tweak to sweepSpinnerStep or sweepSpinnerFrames trips a
// table failure rather than a silent visual drift.
func TestSpinnerOffsetWalksEightPositions(t *testing.T) {
	t.Parallel()

	base := time.Unix(0, 0)

	tests := []struct {
		name   string
		frame  int
		wantDX int
		wantDY int
	}{
		{name: "N", frame: 0, wantDX: 0, wantDY: -1},
		{name: "NE", frame: 1, wantDX: 1, wantDY: -1},
		{name: "E", frame: 2, wantDX: 1, wantDY: 0},
		{name: "SE", frame: 3, wantDX: 1, wantDY: 1},
		{name: "S", frame: 4, wantDX: 0, wantDY: 1},
		{name: "SW", frame: 5, wantDX: -1, wantDY: 1},
		{name: "W", frame: 6, wantDX: -1, wantDY: 0},
		{name: "NW", frame: 7, wantDX: -1, wantDY: -1},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			// time at frame N = base + frame * step + tiny offset so
			// floor lands on N (avoids exactly-on-boundary aliasing).
			instant := base.Add(time.Duration(testCase.frame)*sweepSpinnerStep + sweepSpinnerStep/2)

			gotDX, gotDY := spinnerOffset(instant)
			if gotDX != testCase.wantDX || gotDY != testCase.wantDY {
				t.Errorf("spinnerOffset = (%d, %d), want (%d, %d)",
					gotDX, gotDY, testCase.wantDX, testCase.wantDY)
			}
		})
	}
}

// TestSpinnerOffsetWrapsAround verifies the modulo behaviour: the
// 9th frame should land back at the 1st position (N), proving the
// rotation is endless rather than off-by-one.
func TestSpinnerOffsetWrapsAround(t *testing.T) {
	t.Parallel()

	base := time.Unix(0, 0).Add(sweepSpinnerStep / 2)
	frame0DX, frame0DY := spinnerOffset(base)
	wrappedDX, wrappedDY := spinnerOffset(base.Add(sweepSpinnerFrames * sweepSpinnerStep))

	if frame0DX != wrappedDX || frame0DY != wrappedDY {
		t.Errorf("wrap-around mismatch: frame 0 = (%d, %d), frame 8 = (%d, %d)",
			frame0DX, frame0DY, wrappedDX, wrappedDY)
	}
}

// stubSweep satisfies SweepIndicator for tests.
type stubSweep struct{ sweeping bool }

func (s stubSweep) Sweeping() bool { return s.sweeping }

// cellMainRune returns the first rune of the cell at (x, y) from
// the simulation screen's row-major buffer. drawCenterPoint
// writes one-rune cells via SetContent, so the leading rune is
// sufficient to assert what the renderer drew. Uses the public
// SimulationScreen.GetContents() instead of the deprecated
// Screen.GetContent (which trips staticcheck SA1019 and dogsled).
func cellMainRune(screen tcell.SimulationScreen, x, y int) rune {
	cells, width, _ := screen.GetContents()

	cell := cells[y*width+x]
	if len(cell.Runes) == 0 {
		return 0
	}

	return cell.Runes[0]
}

// TestDrawCenterPointStaticWhenIdle pins the default behaviour:
// no sweep indicator (or one reporting false) draws the static X
// in dark magenta at the centre. The X is what an operator sees
// during normal runtime, so a regression here surfaces as visual
// drift on the radar.
func TestDrawCenterPointStaticWhenIdle(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		sweep SweepIndicator
	}{
		{name: "nil indicator", sweep: nil},
		{name: "indicator reports false", sweep: stubSweep{sweeping: false}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			screen := tcell.NewSimulationScreen("UTF-8")
			if err := screen.Init(); err != nil {
				t.Fatalf("screen.Init: %v", err)
			}

			view := &View{sweep: testCase.sweep}
			view.drawCenterPoint(screen, 10, 5)
			screen.Show()

			if got := cellMainRune(screen, 10, 5); got != 'X' {
				t.Errorf("centre = %q, want 'X' (idle state)", got)
			}
		})
	}
}

// TestDrawCenterPointAnimatesWhenSweeping locks the sweep-branch:
// while the indicator reports true, the X is hidden and a '*' is
// drawn at one of the 8 spinner positions around the centre. The
// exact position depends on the clock; this test sweeps every
// adjacent cell and asserts exactly one of them holds the '*' and
// none of them holds an 'X'. Independent of the per-frame
// position mapping covered by TestSpinnerOffsetWalksEightPositions.
func TestDrawCenterPointAnimatesWhenSweeping(t *testing.T) {
	t.Parallel()

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen.Init: %v", err)
	}

	const centerCol, centerRow = 10, 5

	view := &View{sweep: stubSweep{sweeping: true}}
	view.drawCenterPoint(screen, centerCol, centerRow)
	screen.Show()

	if got := cellMainRune(screen, centerCol, centerRow); got == 'X' {
		t.Error("centre = 'X' while sweeping, want hidden")
	}

	stars := 0

	for offsetY := -1; offsetY <= 1; offsetY++ {
		for offsetX := -1; offsetX <= 1; offsetX++ {
			if offsetX == 0 && offsetY == 0 {
				continue
			}

			if cellMainRune(screen, centerCol+offsetX, centerRow+offsetY) == '*' {
				stars++
			}
		}
	}

	if stars != 1 {
		t.Errorf("spinner '*' count around centre = %d, want exactly 1", stars)
	}
}
