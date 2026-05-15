package radar

import (
	"fmt"
	"math"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplane"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplanes"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/location"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/scope"
)

const (
	yMultiplier            = 1
	terminalCharacterRatio = 2.0
	headingLineLength      = 12
	degreesToRadiansRatio  = math.Pi / 180
	nauticalMilePerDegree  = 60.0
	fastVerticalRate       = 500
	slowVerticalRate       = 100
	altitudeMultiplier     = 1000
	circleSteps            = 8
	circleMaxWidth         = 5
	callsignMaxWidth       = 7

	// Trail dot colours, from darkest (oldest entry) to lightest (most recent).
	trailColor1 = 0x606060
	trailColor2 = 0x808080
	trailColor3 = 0xa0a0a0

	// Flight-level bucket boundaries (in feet) used by
	// getFlightLevelColor. Each constant is the exclusive upper
	// bound of a colour band; altitudes at or above the highest
	// band fall through to white. Hoisted out of the switch so the
	// numbers carry their meaning in the file rather than relying
	// on inline comments.
	flightLevelSub5   = 500
	flightLevelSub100 = 10000
	flightLevelSub200 = 20000
	flightLevelSub300 = 30000
	flightLevelSub400 = 40000
	flightLevelSub500 = 50000
	flightLevelSub600 = 60000
)

// View is a custom tview component.
type View struct {
	*tview.Box

	headingIndicator bool
	trailIndicator   bool
	heatIndicator    bool
	autoScope        bool
	planes           *airplanes.Airplanes
	myLocation       *location.Location
	myScope          *scope.Scope
	heat             *heatMap
	mu               sync.RWMutex
}

// New initializes a new radar scope view.
func New(planes *airplanes.Airplanes, myLocation *location.Location) *View {
	return &View{
		Box:              tview.NewBox().SetBorder(false).SetBorderPadding(1, 1, 1, 1),
		headingIndicator: false,
		trailIndicator:   true,
		heatIndicator:    true,
		myScope:          scope.New(),
		autoScope:        true,
		planes:           planes,
		myLocation:       myLocation,
		heat:             newHeatMap(),
	}
}

// SetScopeRange sets the radar scope range in nautical miles.
func (r *View) SetScopeRange(rangeNm float64) {
	r.myScope.Update(scope.WithCurrent(rangeNm))
}

// IncrementScope increases the radar scope range by one increment step.
func (r *View) IncrementScope() {
	r.myScope.Update(scope.WithCurrent(r.myScope.GetCurrent() + r.myScope.GetIncrement()))
}

// DecrementScope decreases the radar scope range by one increment step.
func (r *View) DecrementScope() {
	r.myScope.Update(scope.WithCurrent(r.myScope.GetCurrent() - r.myScope.GetIncrement()))
}

// ToggleHeadingIndicator toggles the display of heading trails.
func (r *View) ToggleHeadingIndicator() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.headingIndicator = !r.headingIndicator
}

// ToggleTrailIndicator toggles the display of position history trails.
func (r *View) ToggleTrailIndicator() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.trailIndicator = !r.trailIndicator
}

// GetTrailIndicatorEnabled returns whether trail display is enabled.
func (r *View) GetTrailIndicatorEnabled() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.trailIndicator
}

// ToggleHeatIndicator toggles the heat map overlay.
func (r *View) ToggleHeatIndicator() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.heatIndicator = !r.heatIndicator
}

// GetHeatIndicatorEnabled returns whether the heat map is enabled.
func (r *View) GetHeatIndicatorEnabled() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.heatIndicator
}

// ToggleAutoScope toggles the automatic scope range adjustment.
func (r *View) ToggleAutoScope() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.autoScope = !r.autoScope
}

// GetScopeRange returns the current scope range.
func (r *View) GetScopeRange() float64 {
	return r.myScope.GetCurrent()
}

// GetHeadingIndicatorEnabled returns whether headings are enabled.
func (r *View) GetHeadingIndicatorEnabled() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.headingIndicator
}

// GetAutoScopeEnabled returns whether auto scope is enabled.
func (r *View) GetAutoScopeEnabled() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.autoScope
}

// GetAircraftCount returns the number of tracked aircraft.
func (r *View) GetAircraftCount() int {
	return r.planes.Count()
}

// drawToggles is a point-in-time copy of the four boolean
// indicators Draw consults. Bundled into a struct so the lock
// window in snapshotToggles is a single statement and so the
// downstream rendering helpers receive one read-only argument
// instead of a fan of control-flag bools.
type drawToggles struct {
	heading, autoScope, heat, trail bool
}

// Draw draws the radar scope view on the screen.
//
// The toggle/auto-scope booleans are sampled into a single
// drawToggles value under a short RLock window and released
// before any rendering happens. The heatmap and scope have their
// own internal locks, so the entire plane-walk and scope-grow
// path runs lock-free at View level — getter-style readers
// (footer updater, key dispatch) never block on the full render
// path.
func (r *View) Draw(screen tcell.Screen) {
	toggles := r.snapshotToggles()

	r.DrawForSubclass(screen, r)
	innerX, innerY, width, height := r.GetInnerRect()
	centerX, centerY := innerX+width/2, innerY+height/2

	centerLatitude, centerLongitude := r.myLocation.GetCoordinates()
	planeList := r.planes.Sorted(centerLatitude, centerLongitude)

	if toggles.autoScope {
		r.applyAutoScope(planeList, centerLatitude, centerLongitude)
	}

	xScale, yScale := r.calculateScales(width, height)

	// 1. Draw Scope Rings
	r.drawScopeRings(screen, centerX, centerY, xScale, yScale)

	// 2. Draw Center Point (You) and compass indicators
	r.drawCenterPoint(screen, centerX, centerY)
	r.drawCompassIndicators(screen, innerX, innerY, centerX, centerY, width, height)

	// 3. Draw Planes (also accumulates heat as a side effect)
	r.drawPlanes(screen, planeList, centerX, centerY, xScale, yScale, centerLatitude, centerLongitude, toggles)

	// 4. Decay and draw heat map last so nothing overwrites it
	r.heat.decay()

	if toggles.heat {
		r.heat.draw(screen, centerX, centerY, xScale, yScale)
	}
}

// snapshotToggles captures the toggle indicators Draw needs under
// a single short RLock window. The struct return lets the rest of
// Draw run lock-free at View level while the heatmap and scope
// keep their own internal serialisation.
func (r *View) snapshotToggles() drawToggles {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return drawToggles{
		heading:   r.headingIndicator,
		autoScope: r.autoScope,
		heat:      r.heatIndicator,
		trail:     r.trailIndicator,
	}
}

// applyAutoScope sizes the scope to the farthest position-bearing
// plane, rounded up to the next Scope.Increment. When no plane has
// a valid position the current scope is left alone — this avoids
// the empty-airspace snap-to-Min that drove c3a81e2d and the
// permanent pin-to-Max that replaced it.
func (r *View) applyAutoScope(planeList []airplane.Snapshot, centerLatitude, centerLongitude float64) {
	var maxDist float64

	for _, plane := range planeList {
		if plane.Latitude == 0 || plane.Longitude == 0 {
			continue
		}

		dLat := plane.Latitude - centerLatitude
		dLon := (plane.Longitude - centerLongitude) * math.Cos(centerLatitude*degreesToRadiansRatio)
		nmY := dLat * nauticalMilePerDegree
		nmX := dLon * nauticalMilePerDegree

		if dist := math.Sqrt(nmX*nmX + nmY*nmY); dist > maxDist {
			maxDist = dist
		}
	}

	if maxDist == 0 {
		return
	}

	increment := r.myScope.GetIncrement()
	target := math.Ceil(maxDist/increment) * increment
	r.myScope.Update(scope.WithCurrent(target))
}

func (r *View) calculateScales(width, height int) (float64, float64) {
	// Scales: Terminal characters are usually ~2x taller than wide.
	// We adjust yScale to keep the rings circular.
	xScale := float64(width) / (r.myScope.GetCurrent() * 2)
	yScale := float64(height) / (r.myScope.GetCurrent() * 2) * terminalCharacterRatio

	return xScale, yScale
}

func (*View) drawCenterPoint(screen tcell.Screen, centerX, centerY int) {
	screen.SetContent(centerX, centerY, 'X', nil, tcell.StyleDefault.Foreground(tcell.ColorDarkMagenta))
}

func (*View) drawCompassIndicators(
	screen tcell.Screen,
	innerX, innerY, centerX, centerY, width, height int,
) {
	compassStyle := tcell.StyleDefault.Foreground(tcell.ColorGreen)

	// North: top-center
	tview.Print(screen, "N", centerX, innerY, 1, tview.AlignLeft, tcell.ColorGreen)
	// South: bottom-center
	tview.Print(screen, "S", centerX, innerY+height-1, 1, tview.AlignLeft, tcell.ColorGreen)
	// East: right-center
	screen.SetContent(innerX+width-1, centerY, 'E', nil, compassStyle)
	// West: left edge
	screen.SetContent(innerX, centerY, 'W', nil, compassStyle)
}

//nolint:revive // argument-limit: the scale, centre and toggle parameters are all load-bearing here.
func (r *View) drawPlanes(
	screen tcell.Screen,
	planeList []airplane.Snapshot,
	centerX, centerY int,
	xScale, yScale, centerLatitude, centerLongitude float64,
	toggles drawToggles,
) {
	planeStyle := tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorBlack)

	for _, plane := range planeList {
		if plane.Latitude == 0 || plane.Longitude == 0 {
			continue
		}

		// Calculate delta in degrees
		dLat := plane.Latitude - centerLatitude
		dLon := (plane.Longitude - centerLongitude) * math.Cos(centerLatitude*degreesToRadiansRatio)

		// Convert degrees to Nautical Miles (~60nm per degree)
		nmY := dLat * nauticalMilePerDegree
		nmX := dLon * nauticalMilePerDegree

		// Skip if outside scope
		dist := math.Sqrt(nmX*nmX + nmY*nmY)
		if dist > r.myScope.GetCurrent() {
			continue
		}

		// airplaneMap to screen coordinates
		// Note: Y is inverted in screen space (up is negative)
		planeX := centerX + int(nmX*xScale)
		planeY := centerY - int(nmY*yScale/yMultiplier)

		r.heat.add(nmX, nmY)

		if toggles.trail {
			r.drawTrail(
				screen, plane.PositionHistory,
				centerX, centerY, xScale, yScale,
				centerLatitude, centerLongitude,
			)
		}

		r.drawPlane(screen, plane, planeX, planeY, planeStyle, toggles)
	}
}

func (*View) drawPlane(
	screen tcell.Screen, plane airplane.Snapshot, planeX, planeY int, style tcell.Style, toggles drawToggles,
) {
	altColor := getFlightLevelColor(plane.Altitude)

	// Draw heading indicator line if heading is valid
	if plane.Heading != -1 && toggles.heading {
		headingStyle := tcell.StyleDefault.Foreground(altColor).Background(tcell.ColorBlack)
		drawHeadingLine(screen, planeX, planeY, plane.Heading, headingStyle)
	}

	screen.SetContent(planeX, planeY, '+', nil, style)

	label := plane.Callsign
	if label == "" {
		label = plane.ICAO
	}

	tview.Print(screen, label, planeX+1, planeY, callsignMaxWidth, tview.AlignLeft, altColor)

	// Display altitude with a vertical rate symbol
	vertSymbol := getVertRateSymbol(plane.VertRate)
	vertColor := getVerticalRateColor(plane.VertRate)

	altText := altitudeToFL(plane.Altitude) + " "
	tview.Print(screen, altText, planeX+1, planeY+1, len(altText), tview.AlignLeft, altColor)
	tview.Print(screen, vertSymbol, planeX+1+len(altText), planeY+1, 1, tview.AlignLeft, vertColor)
}

// drawTrail renders historical position dots behind a plane.
// Older entries are drawn in darker grey; the most recent in lighter grey.
func (*View) drawTrail(
	screen tcell.Screen,
	history []airplane.PositionEntry,
	centerX, centerY int,
	xScale, yScale, centerLatitude, centerLongitude float64,
) {
	trailColors := []tcell.Color{
		tcell.ColorGray,
		tcell.NewHexColor(trailColor1),
		tcell.NewHexColor(trailColor2),
		tcell.NewHexColor(trailColor3),
	}

	historyLen := len(history)
	for idx, entry := range history {
		if entry.Latitude == 0 || entry.Longitude == 0 {
			continue
		}

		dLat := entry.Latitude - centerLatitude
		dLon := (entry.Longitude - centerLongitude) * math.Cos(centerLatitude*degreesToRadiansRatio)
		nmY := dLat * nauticalMilePerDegree
		nmX := dLon * nauticalMilePerDegree

		screenX := centerX + int(nmX*xScale)
		screenY := centerY - int(nmY*yScale/yMultiplier)

		// Map entry index to a color bucket: older entries use darker colors.
		// (idx * len) / historyLen with idx ∈ [0, historyLen-1] is always < len,
		// so no clamp is needed.
		colorIdx := (idx * len(trailColors)) / historyLen

		style := tcell.StyleDefault.Foreground(trailColors[colorIdx]).Background(tcell.ColorBlack)
		screen.SetContent(screenX, screenY, '·', nil, style)
	}
}

func (r *View) drawScopeRings(screen tcell.Screen, centerX, centerY int, xScale, yScale float64) {
	ringStyle := tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorBlack)
	increments := r.myScope.GetCurrent() / r.myScope.GetSteps()

	for ring := increments; ring <= r.myScope.GetCurrent(); ring += increments {
		drawCircle(screen, centerX, centerY, int(ring*xScale), int(ring*yScale/2), ringStyle)

		if ring < r.myScope.GetCurrent() {
			tview.Print(screen,
				fmt.Sprintf("%0.0fnm", ring),
				centerX+int(ring*xScale),
				centerY,
				circleMaxWidth,
				tview.AlignLeft,
				tcell.ColorGreen,
			)
		}
	}
}

func drawHeadingLine(screen tcell.Screen, x, y int, heading float64, style tcell.Style) { //nolint:varnamelen
	const lineRounding = 0.5

	// Convert heading to radians
	// Aviation: 0° = North, 90° = East, 180° = South, 270° = West (clockwise)
	// Screen: X increases right, Y increases down
	// North (0°) should point up (-Y), East (90°) should point right (+X)
	radians := heading * degreesToRadiansRatio

	// Calculate direction vector
	// sin(heading) gives us X component (East/West)
	// -cos(heading) gives us Y component (North is up, so negative)
	directionX := math.Sin(radians)
	directionY := -math.Cos(radians) * lineRounding // to compensate for the terminal character aspect ratio

	// Draw a dotted line using a Bresenham-like approach
	for dot := 1; dot <= headingLineLength; dot++ {
		// Calculate position along the dot
		px := x + int(float64(dot)*directionX+lineRounding) // +0.5 for rounding
		py := y + int(float64(dot)*directionY+lineRounding)

		// Draw dot every other step for a dotted appearance
		if dot%2 == 1 {
			screen.SetContent(px, py, '·', nil, style)
		}
	}
}

// Simple Bresenham-like circle helper for terminal.
func drawCircle(screen tcell.Screen, cx, cy, rx, ry int, style tcell.Style) {
	for degrees := 0; degrees < 360; degrees += circleSteps {
		rad := float64(degrees) * degreesToRadiansRatio
		x := cx + int(float64(rx)*math.Cos(rad))
		y := cy + int(float64(ry)*math.Sin(rad))
		screen.SetContent(x, y, '.', nil, style)
	}
}

// getVerticalRateColor returns a color based on vertical rate in fpm.
func getVerticalRateColor(vertRate float64) tcell.Color {
	switch {
	case vertRate > fastVerticalRate:
		return tcell.ColorGreen
	case vertRate < -fastVerticalRate:
		return tcell.ColorRed
	default:
		return tcell.ColorLightBlue
	}
}

// getVertRateSymbol returns an ASCII symbol for vertical rate.
func getVertRateSymbol(vertRate float64) string {
	switch {
	case vertRate > fastVerticalRate:
		return "^" // Climbing significantly
	case vertRate > slowVerticalRate:
		return "+" // Climbing
	case vertRate < -fastVerticalRate:
		return "v" // Descending significantly
	case vertRate < -slowVerticalRate:
		return "-" // Descending
	default:
		return "=" // Level flight (within ±100 fpm)
	}
}

// altitudeToFL converts altitude in feet to flight level format (three digits max).
func altitudeToFL(altitude float64) string {
	return fmt.Sprintf("FL%03d", int(altitude/altitudeMultiplier))
}

// getFlightLevelColor returns a color based on altitude.
func getFlightLevelColor(altitude float64) tcell.Color {
	switch {
	case altitude < flightLevelSub5:
		return tcell.ColorWhite
	case altitude < flightLevelSub100:
		return tcell.ColorYellow
	case altitude < flightLevelSub200:
		return tcell.ColorGreen
	case altitude < flightLevelSub300:
		return tcell.ColorLightBlue
	case altitude < flightLevelSub400:
		return tcell.ColorDarkBlue
	case altitude < flightLevelSub500:
		return tcell.ColorPurple
	case altitude < flightLevelSub600:
		return tcell.ColorRed
	default:
		return tcell.ColorWhite
	}
}
