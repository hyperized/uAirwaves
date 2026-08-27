package ui

import (
	"fmt"
	"math"
	"strings"

	"github.com/hyperized/uAirwaves/pkg/coverage"
	"github.com/rivo/tview"
)

// CoverageMode is the operator-cycled state of the antenna-coverage panel:
// a distance/altitude density cone, a top-down shadow silhouette, or
// hidden. The 'c' key cycles cone -> shadows -> off.
type CoverageMode uint8

// Coverage-mode enumerators. The zero value is CoverageCone so a freshly
// constructed CoverageView shows the density cone by default.
const (
	CoverageCone CoverageMode = iota
	CoverageShadows
	CoverageOff
)

// String returns the operator-facing label for a coverage mode, matching
// the footer chip text.
func (m CoverageMode) String() string {
	switch m {
	case CoverageCone:
		return "cone"
	case CoverageShadows:
		return "shadows"
	case CoverageOff:
		return "off"
	default:
		return "?"
	}
}

// nextCoverageMode advances the cycle cone -> shadows -> off -> cone.
// Unrecognised inputs fall through to CoverageCone so an out-of-range value
// recovers on the next cycle.
func nextCoverageMode(current CoverageMode) CoverageMode {
	if current == CoverageCone {
		return CoverageShadows
	}

	if current == CoverageShadows {
		return CoverageOff
	}

	return CoverageCone
}

// CoverageView holds the coverage panel's cyclable mode. Both the key
// dispatcher (Cycle) and the per-tick render path (Mode) run on the tview
// event loop, which serialises every access, so the mode is a plain field
// with no mutex.
type CoverageView struct {
	mode CoverageMode
}

// NewCoverageView returns a view defaulting to the density cone.
func NewCoverageView() *CoverageView {
	return &CoverageView{mode: CoverageCone}
}

// Mode returns the current coverage mode.
func (v *CoverageView) Mode() CoverageMode {
	return v.mode
}

// Cycle advances the coverage mode one step through the cone/shadows/off
// cycle.
func (v *CoverageView) Cycle() {
	v.mode = nextCoverageMode(v.mode)
}

const (
	// coverageContentWidth and coverageContentHeight are the logical
	// character dimensions the panel renders into. Sized for the 50-col
	// right column (border + padding trims roughly 4 cols); the TextView
	// clips vertically when the terminal is short.
	coverageContentWidth  = 46
	coverageContentHeight = 12

	// coverageLabelGutter is the width of the left altitude-label column in
	// the cone view: a 3-char "40k" label plus one separator space.
	coverageLabelGutter = 4

	// terminalCellAspect corrects the roughly 2:1 tall-to-wide terminal
	// cell so the shadow plot reads as a circle rather than an ellipse.
	// Local to this package by design — the same idea as pkg/radar's ratio,
	// kept separate so the two panels stay independent.
	terminalCellAspect = 2.0

	// Cone density glyph thresholds: counts below coneLowMax use the light
	// shade, below coneMidMax the dark shade, at or above it the full block.
	coneLowMax = 10
	coneMidMax = 100

	// flightLevelDivisor converts altitude in feet to a flight level
	// (FL = altitude_ft / 100).
	flightLevelDivisor = 100

	// coneLabelStepFt / thousandFeet drive the cone's left-axis labels:
	// bands whose lower bound is a non-zero multiple of coneLabelStepFt are
	// labelled in thousands of feet ("10k".."40k").
	coneLabelStepFt = 10000
	thousandFeet    = 1000

	// Distance-ruler anchor columns (in bins). Each bin is
	// coverage.DistanceBinNm wide, so bin 10 is 100 nm and bin 20 is 200 nm.
	rulerAnchorMid = 10
	rulerAnchorFar = 20

	// coverageSectorWidthDeg is the width of one bearing sector, matching
	// the tracker's sector count.
	coverageSectorWidthDeg = fullCircleDegrees / coverage.BearingSectorCount
)

// Cone glyphs keyed on detection count, and the shadow-plot glyphs.
const (
	coneGlyphEmpty = "·"
	coneGlyphLow   = "▒"
	coneGlyphMid   = "▓"
	coneGlyphHigh  = "█"

	shadowGlyphFill   = '▓'
	shadowGlyphBlank  = ' '
	shadowGlyphCenter = '+'
)

// UpdateCoveragePanel renders the coverage snapshot for the given mode into
// the panel TextView. CoverageOff yields empty text; the caller collapses
// the panel separately. Thin dispatch wrapper so the pure formatters stay
// independently testable.
func UpdateCoveragePanel(panel *tview.TextView, mode CoverageMode, snap coverage.Snapshot) {
	panel.SetText(coverageText(mode, snap))
}

// coverageText dispatches to the formatter for the active mode, rendering at
// the panel's content dimensions.
func coverageText(mode CoverageMode, snap coverage.Snapshot) string {
	if mode == CoverageOff {
		return ""
	}

	if mode == CoverageShadows {
		return FormatCoverageShadows(snap, coverageContentWidth, coverageContentHeight)
	}

	return FormatCoverageCone(snap, coverageContentWidth, coverageContentHeight)
}

// FormatCoverageCone renders the density cone: distance on X (near on the
// left), altitude band on Y (highest at top), one glyph per cell keyed on
// detection count. width/height bound the rendered grid; production passes
// the panel content dimensions. Pure function — deterministic for a given
// snapshot, so a golden test pins the layout.
func FormatCoverageCone(snap coverage.Snapshot, width, height int) string {
	cols := clampInt(width-coverageLabelGutter, 0, coverage.DistanceBinCount)

	lines := make([]string, 0, coverage.AltitudeBandCount+2)
	for band := coverage.AltitudeBandCount - 1; band >= 0; band-- {
		lines = append(lines, coneRow(snap, band, cols))
	}

	lines = append(lines, coneRuler(cols), coneCaption(snap))

	return clampLines(lines, height)
}

// coneRow renders one altitude band as its gutter label plus a glyph per
// distance bin.
func coneRow(snap coverage.Snapshot, band, cols int) string {
	var builder strings.Builder

	builder.WriteString(coneGutter(band))

	for bin := range cols {
		builder.WriteString(coneGlyph(snap.Cells[band][bin]))
	}

	return builder.String()
}

// coneGutter returns the left-axis label for an altitude band. Bands whose
// lower bound is a non-zero multiple of coneLabelStepFt are labelled
// (10k..40k); the rest get a blank gutter so the labelled rows stand out.
func coneGutter(band int) string {
	lowerFt := band * int(coverage.AltitudeBandFt)
	if lowerFt != 0 && lowerFt%coneLabelStepFt == 0 {
		return fmt.Sprintf("%3s ", fmt.Sprintf("%dk", lowerFt/thousandFeet))
	}

	return strings.Repeat(" ", coverageLabelGutter)
}

// coneGlyph maps a detection count to its density glyph.
func coneGlyph(count uint32) string {
	switch {
	case count == 0:
		return coneGlyphEmpty
	case count < coneLowMax:
		return coneGlyphLow
	case count < coneMidMax:
		return coneGlyphMid
	default:
		return coneGlyphHigh
	}
}

// coneRuler builds the bottom distance ruler, anchoring 0/100/200 nm marks
// under their bins and clipping anchors past the grid width.
func coneRuler(cols int) string {
	gutter := strings.Repeat(" ", coverageLabelGutter)
	ruler := []rune(strings.Repeat(" ", cols))

	writeRulerLabel(ruler, 0, "0")
	writeRulerLabel(ruler, rulerAnchorMid, "100")
	writeRulerLabel(ruler, rulerAnchorFar, "200")

	return gutter + string(ruler) + " nm"
}

// writeRulerLabel overlays label onto the ruler starting at col, clipping to
// the ruler width so a narrow grid drops out-of-range anchors.
func writeRulerLabel(ruler []rune, col int, label string) {
	for offset, char := range label {
		pos := col + offset
		if pos < 0 || pos >= len(ruler) {
			return
		}

		ruler[pos] = char
	}
}

// coneCaption summarises the global maximum range and the altitude it
// occurred at, or a dash when nothing has been observed.
func coneCaption(snap coverage.Snapshot) string {
	if snap.MaxRangeNm <= 0 {
		return DimTag + "max — nm" + ResetTag
	}

	flightLevel := int(snap.MaxRangeAltFt / flightLevelDivisor)

	return fmt.Sprintf(DimTag+"max %.0f nm @ FL%03d"+ResetTag, snap.MaxRangeNm, flightLevel)
}

// FormatCoverageShadows renders the top-down shadow silhouette: the receiver
// at plot centre, each cell filled when its range (scaled so the farthest
// sector reaches the plot edge) is within that bearing sector's observed
// maximum. Antenna nulls read as missing wedges. width/height bound the
// plot; production passes the panel content dimensions. Pure function —
// deterministic for a given snapshot.
func FormatCoverageShadows(snap coverage.Snapshot, width, height int) string {
	plotRows := clampInt(height-1, 1, height)
	centerRow := plotRows / 2
	radius := math.Min(float64(centerRow)*terminalCellAspect, float64((width-1)/2))
	radiusCols := int(radius)
	plotCols := 2*radiusCols + 1
	centerCol := radiusCols
	maxRange := maxSectorRange(snap.Sectors)

	grid := make([][]rune, plotRows)
	for row := range plotRows {
		line := make([]rune, plotCols)
		for col := range plotCols {
			line[col] = shadowCell(shadowCoord{row, col, centerRow, centerCol}, radius, maxRange, snap.Sectors)
		}

		grid[row] = line
	}

	placeCardinals(grid, centerRow, centerCol, radiusCols)

	return shadowLines(grid, maxRange, height)
}

// shadowCoord bundles a cell's position and the plot centre so shadowCell
// stays inside revive's argument-count limit.
type shadowCoord struct {
	row, col, centerRow, centerCol int
}

// shadowCell picks the glyph for one plot cell: '+' at the centre, a fill
// glyph inside the sector's scaled range, blank otherwise.
func shadowCell(
	coord shadowCoord, radius, maxRange float64, sectors [coverage.BearingSectorCount]float64,
) rune {
	east := float64(coord.col - coord.centerCol)
	north := float64(coord.centerRow-coord.row) * terminalCellAspect
	dist := math.Hypot(east, north)

	if dist == 0 {
		return shadowGlyphCenter
	}

	if dist > radius || maxRange <= 0 {
		return shadowGlyphBlank
	}

	threshold := (sectors[sectorIndex(shadowBearing(east, north))] / maxRange) * radius
	if dist <= threshold {
		return shadowGlyphFill
	}

	return shadowGlyphBlank
}

// shadowBearing returns the compass bearing (degrees, [0,360)) of a cell
// from the plot centre, with north up and east right.
func shadowBearing(east, north float64) float64 {
	bearing := math.Atan2(east, north) * radiansToDegrees

	return math.Mod(bearing+fullCircleDegrees, fullCircleDegrees)
}

// sectorIndex maps a normalised bearing to its sector, clamping the
// 360-degree edge into the last sector.
func sectorIndex(bearingDeg float64) int {
	return clampInt(int(bearingDeg/coverageSectorWidthDeg), 0, coverage.BearingSectorCount-1)
}

// maxSectorRange returns the largest per-sector range, the value that maps
// to the plot edge. Zero means nothing has been observed yet.
func maxSectorRange(sectors [coverage.BearingSectorCount]float64) float64 {
	maxRange := 0.0
	for _, sector := range sectors {
		if sector > maxRange {
			maxRange = sector
		}
	}

	return maxRange
}

// placeCardinals overlays the N/E/S/W labels on the plot edges when they
// fall inside the grid.
func placeCardinals(grid [][]rune, centerRow, centerCol, radiusCols int) {
	radiusRows := int(float64(radiusCols) / terminalCellAspect)

	setCell(grid, centerRow-radiusRows, centerCol, 'N')
	setCell(grid, centerRow+radiusRows, centerCol, 'S')
	setCell(grid, centerRow, centerCol+radiusCols, 'E')
	setCell(grid, centerRow, centerCol-radiusCols, 'W')
}

// setCell writes char into the grid when (row, col) is in bounds.
func setCell(grid [][]rune, row, col int, char rune) {
	if row < 0 || row >= len(grid) {
		return
	}

	if col < 0 || col >= len(grid[row]) {
		return
	}

	grid[row][col] = char
}

// shadowLines joins the plot rows and appends the edge-scale caption,
// clipped to height.
func shadowLines(grid [][]rune, maxRange float64, height int) string {
	lines := make([]string, 0, len(grid)+1)
	for _, row := range grid {
		lines = append(lines, string(row))
	}

	lines = append(lines, shadowCaption(maxRange))

	return clampLines(lines, height)
}

// shadowCaption reports the plot-edge range in nm, or a placeholder when no
// coverage has been recorded.
func shadowCaption(maxRange float64) string {
	if maxRange <= 0 {
		return DimTag + "no coverage yet" + ResetTag
	}

	return fmt.Sprintf(DimTag+"edge %.0f nm"+ResetTag, maxRange)
}

// clampInt bounds value into [low, high].
func clampInt(value, low, high int) int {
	if value < low {
		return low
	}

	if value > high {
		return high
	}

	return value
}

// clampLines joins lines with newlines, keeping at most height lines when
// height is positive. A non-positive height keeps them all.
func clampLines(lines []string, height int) string {
	if height > 0 && height < len(lines) {
		lines = lines[:height]
	}

	return strings.Join(lines, "\n")
}
