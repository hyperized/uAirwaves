package ui

import (
	"math"
	"strings"
	"testing"

	"github.com/hyperized/uAirwaves/pkg/coverage"
)

// TestNextCoverageMode pins the cone -> shadows -> off -> cone cycle and the
// out-of-range recovery to cone.
func TestNextCoverageMode(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		in   CoverageMode
		want CoverageMode
	}{
		{"cone advances to shadows", CoverageCone, CoverageShadows},
		{"shadows advances to off", CoverageShadows, CoverageOff},
		{"off wraps to cone", CoverageOff, CoverageCone},
		{"unknown recovers to cone", CoverageMode(9), CoverageCone},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := nextCoverageMode(testCase.in); got != testCase.want {
				t.Errorf("nextCoverageMode(%v) = %v, want %v", testCase.in, got, testCase.want)
			}
		})
	}
}

// TestConeGlyph pins the density-glyph thresholds at their boundaries.
func TestConeGlyph(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name  string
		count uint32
		want  string
	}{
		{"empty", 0, coneGlyphEmpty},
		{"low lower bound", 1, coneGlyphLow},
		{"low upper bound", 9, coneGlyphLow},
		{"mid lower bound", 10, coneGlyphMid},
		{"mid upper bound", 99, coneGlyphMid},
		{"high lower bound", 100, coneGlyphHigh},
		{"high large", 5000, coneGlyphHigh},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := coneGlyph(testCase.count); got != testCase.want {
				t.Errorf("coneGlyph(%d) = %q, want %q", testCase.count, got, testCase.want)
			}
		})
	}
}

// TestConeGutter pins which altitude bands carry a label.
func TestConeGutter(t *testing.T) {
	t.Parallel()

	const unlabeled = "    "

	for _, testCase := range []struct {
		name string
		band int
		want string
	}{
		{"band 0 unlabeled", 0, unlabeled},
		{"band 1 unlabeled", 1, unlabeled},
		{"band 2 is 10k", 2, "10k "},
		{"band 4 is 20k", 4, "20k "},
		{"band 6 is 30k", 6, "30k "},
		{"band 8 is 40k", 8, "40k "},
		{"band 9 unlabeled", 9, unlabeled},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := coneGutter(testCase.band); got != testCase.want {
				t.Errorf("coneGutter(%d) = %q, want %q", testCase.band, got, testCase.want)
			}
		})
	}
}

// TestClampInt covers the low, high, and pass-through arms.
func TestClampInt(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name             string
		value, low, high int
		want             int
	}{
		{"below low", -5, 0, 10, 0},
		{"above high", 15, 0, 10, 10},
		{"within range", 4, 0, 10, 4},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := clampInt(testCase.value, testCase.low, testCase.high); got != testCase.want {
				t.Errorf("clampInt(%d,%d,%d) = %d, want %d",
					testCase.value, testCase.low, testCase.high, got, testCase.want)
			}
		})
	}
}

// TestClampLines covers truncation, keep-all, and the non-positive height
// escape.
func TestClampLines(t *testing.T) {
	t.Parallel()

	lines := []string{"a", "b", "c"}

	for _, testCase := range []struct {
		name   string
		height int
		want   string
	}{
		{"truncates to height", 2, "a\nb"},
		{"keeps all when height exceeds", 5, "a\nb\nc"},
		{"keeps all when non-positive", 0, "a\nb\nc"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := clampLines(lines, testCase.height); got != testCase.want {
				t.Errorf("clampLines(%d) = %q, want %q", testCase.height, got, testCase.want)
			}
		})
	}
}

// countGridRune counts occurrences of target across the grid.
func countGridRune(grid [][]rune, target rune) int {
	total := 0

	for _, line := range grid {
		for _, cell := range line {
			if cell == target {
				total++
			}
		}
	}

	return total
}

// TestSetCell covers the in-bounds write plus every out-of-bounds guard.
func TestSetCell(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name      string
		row, col  int
		wantWrite bool
	}{
		{"in bounds writes", 1, 1, true},
		{"row negative", -1, 1, false},
		{"row too large", 5, 1, false},
		{"col negative", 1, -1, false},
		{"col too large", 1, 9, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			grid := [][]rune{{' ', ' '}, {' ', ' '}}
			setCell(grid, testCase.row, testCase.col, 'X')

			want := 0
			if testCase.wantWrite {
				want = 1
			}

			if got := countGridRune(grid, 'X'); got != want {
				t.Errorf("setCell(%d,%d) wrote %d cells, want %d", testCase.row, testCase.col, got, want)
			}
		})
	}
}

// TestMaxSectorRange covers the all-zero case and the growing-maximum walk.
func TestMaxSectorRange(t *testing.T) {
	t.Parallel()

	if got := maxSectorRange([coverage.BearingSectorCount]float64{}); got != 0 {
		t.Errorf("maxSectorRange(zero) = %v, want 0", got)
	}

	var sectors [coverage.BearingSectorCount]float64

	sectors[3] = 40
	sectors[9] = 120
	sectors[12] = 75

	if got := maxSectorRange(sectors); got != 120 {
		t.Errorf("maxSectorRange = %v, want 120", got)
	}
}

// TestSectorIndex covers the sector edges and the 360-degree clamp guard.
func TestSectorIndex(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		bearing float64
		want    int
	}{
		{"north", 0, 0},
		{"first boundary", 22.5, 1},
		{"just under full circle", 359.9, 15},
		{"clamped at full circle", 360, 15},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := sectorIndex(testCase.bearing); got != testCase.want {
				t.Errorf("sectorIndex(%v) = %d, want %d", testCase.bearing, got, testCase.want)
			}
		})
	}
}

// TestShadowBearing pins the four cardinal directions from the plot centre.
func TestShadowBearing(t *testing.T) {
	t.Parallel()

	const tolerance = 1e-9

	for _, testCase := range []struct {
		name        string
		east, north float64
		want        float64
	}{
		{"north", 0, 1, 0},
		{"east", 1, 0, 90},
		{"south", 0, -1, 180},
		{"west", -1, 0, 270},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := shadowBearing(testCase.east, testCase.north); math.Abs(got-testCase.want) > tolerance {
				t.Errorf("shadowBearing(%v,%v) = %v, want %v", testCase.east, testCase.north, got, testCase.want)
			}
		})
	}
}

// TestWriteRulerLabel covers the in-range overlay and the out-of-range clip.
func TestWriteRulerLabel(t *testing.T) {
	t.Parallel()

	ruler := []rune("     ")
	writeRulerLabel(ruler, 1, "42")

	if got := string(ruler); got != " 42  " {
		t.Errorf("writeRulerLabel in range = %q, want %q", got, " 42  ")
	}

	clipped := []rune("   ")
	writeRulerLabel(clipped, 5, "9")

	if got := string(clipped); got != "   " {
		t.Errorf("writeRulerLabel out of range = %q, want unchanged", got)
	}
}

// TestCoverageText covers the three dispatch arms without a tview widget.
func TestCoverageText(t *testing.T) {
	t.Parallel()

	empty := coverage.Snapshot{}

	if got := coverageText(CoverageOff, empty); got != "" {
		t.Errorf("coverageText(off) = %q, want empty", got)
	}

	if got := coverageText(CoverageShadows, empty); !strings.Contains(got, "no coverage yet") {
		t.Errorf("coverageText(shadows) = %q, want the placeholder caption", got)
	}

	if got := coverageText(CoverageCone, empty); !strings.Contains(got, "max — nm") {
		t.Errorf("coverageText(cone) = %q, want the max caption", got)
	}
}
