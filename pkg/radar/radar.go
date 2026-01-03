package radar

import (
	"fmt"
	"math"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
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
)

// View is a custom tview component.
type View struct {
	*tview.Box

	headingIndicator bool
	autoScope        bool
	planes           *airplanes.Airplanes
	myLocation       *location.Location
	myScope          *scope.Scope
	mu               sync.RWMutex
}

// New initializes a new radar scope view.
func New(planes *airplanes.Airplanes, myLocation *location.Location) *View {
	return &View{
		Box:              tview.NewBox().SetBorder(true).SetTitle("Radar Scope (5-20nm)"),
		headingIndicator: true,
		myScope:          scope.New(),
		autoScope:        true,
		planes:           planes,
		myLocation:       myLocation,
	}
}

// SetScopeRange sets the radar scope range in nautical miles.
func (r *View) SetScopeRange(rangeNm float64) {
	r.myScope.Update(scope.WithCurrent(rangeNm))
}

// IncrementScope increases the radar scope range by the minimum step size.
func (r *View) IncrementScope() {
	r.myScope.Update(scope.WithCurrent(r.myScope.GetCurrent() + r.myScope.GetMin()))
}

// DecrementScope decreases the radar scope range by the minimum step size.
func (r *View) DecrementScope() {
	r.myScope.Update(scope.WithCurrent(r.myScope.GetCurrent() - r.myScope.GetMin()))
}

// ToggleHeadingIndicator toggles the display of heading trails.
func (r *View) ToggleHeadingIndicator() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.headingIndicator = !r.headingIndicator
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

// Draw draws the radar scope view on the screen.
func (r *View) Draw(screen tcell.Screen) { //nolint:funlen
	r.mu.RLock()
	defer r.mu.RUnlock()

	r.DrawForSubclass(screen, r)
	x, y, width, height := r.GetInnerRect()

	centerX, centerY := x+width/2, y+height/2

	// Scales: Terminal characters are usually ~2x taller than wide.
	// We adjust yScale to keep the rings circular.
	xScale := float64(width) / (r.myScope.GetCurrent() * 2)
	yScale := float64(height) / (r.myScope.GetCurrent() * 2) * terminalCharacterRatio

	// 1. Draw Scope Rings
	r.drawScopeRings(screen, centerX, centerY, xScale, yScale)

	// 2. Draw Center Point (You)
	screen.SetContent(centerX, centerY, 'X', nil, tcell.StyleDefault.Foreground(tcell.ColorRed))

	// 3. Draw Planes
	planeStyle := tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorBlack)
	centerLatitude, centerLongitude := r.myLocation.GetCoordinates()

	planeList := r.planes.Sorted(centerLatitude, centerLongitude)
	if len(planeList) == 0 && r.autoScope {
		// If there are no planes, reset the scope view
		r.myScope.Update(scope.WithCurrent(r.myScope.GetMin()))
	}

	// If there are planes, display them
	for _, p := range planeList {
		plane := p.GetSnapshot()

		lat := plane.Latitude
		lon := plane.Longitude
		callsign := plane.Callsign

		if lat == 0 || lon == 0 {
			// Skip planes without a location, as they can't be plotted
			continue
		}

		// Calculate delta in degrees
		dLat := lat - centerLatitude
		dLon := (lon - centerLongitude) * math.Cos(centerLatitude*degreesToRadiansRatio)

		// Convert degrees to Nautical Miles (~60nm per degree)
		nmY := dLat * nauticalMilePerDegree
		nmX := dLon * nauticalMilePerDegree

		// Skip if outside scope
		dist := math.Sqrt(nmX*nmX + nmY*nmY)
		if dist > r.myScope.GetCurrent() {
			// Increase the scope range and try again with the next loop cycle
			if r.autoScope {
				r.myScope.Update(scope.WithCurrent(r.myScope.GetCurrent() + r.myScope.GetMin()))
			}

			continue
		}

		// airplaneMap to screen coordinates
		// Note: Y is inverted in screen space (up is negative)
		planeX := centerX + int(nmX*xScale)
		planeY := centerY - int(nmY*yScale/yMultiplier)

		// Use color-coded altitude display
		altColor := getFlightLevelColor(plane.Altitude)

		// Draw heading indicator line if heading is valid
		if plane.Heading != -1 && r.headingIndicator {
			headingStyle := tcell.StyleDefault.Foreground(altColor).Background(tcell.ColorBlack)
			drawHeadingLine(screen, planeX, planeY, plane.Heading, headingStyle)
		}

		screen.SetContent(planeX, planeY, '+', nil, planeStyle)

		if callsign == "" {
			tview.Print(screen, plane.ICAO, planeX+1, planeY, callsignMaxWidth, tview.AlignLeft, tcell.ColorYellow)
		} else {
			tview.Print(screen, plane.Callsign, planeX+1, planeY, callsignMaxWidth, tview.AlignLeft, tcell.ColorYellow)
		}

		// Display altitude with a vertical rate symbol
		vertSymbol := getVertRateSymbol(plane.VertRate)
		vertColor := getVerticalRateColor(plane.VertRate)

		// Print altitude in altitude color, then vertical rate symbol in vert rate color
		altText := altitudeToFL(plane.Altitude) + " "
		tview.Print(screen, altText, planeX+1, planeY+1, len(altText), tview.AlignLeft, altColor)
		tview.Print(screen, vertSymbol, planeX+1+len(altText), planeY+1, 1, tview.AlignLeft, vertColor)
	}

	// Set title
	r.SetTitle(fmt.Sprintf(
		"Radar Scope (%.0f-%.0fnm)",
		r.myScope.GetCurrent()/r.myScope.GetSteps(),
		r.myScope.GetCurrent(),
	))
}

func (r *View) drawScopeRings(screen tcell.Screen, centerX, centerY int, xScale, yScale float64) {
	ringStyle := tcell.StyleDefault.Foreground(tcell.ColorGreen).Background(tcell.ColorBlack)
	increments := r.myScope.GetCurrent() / r.myScope.GetSteps()

	for ring := increments; ring <= r.myScope.GetCurrent(); ring += increments {
		drawCircle(screen, centerX, centerY, int(ring*xScale), int(ring*yScale/2), ringStyle)
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
	case altitude < 500: //nolint:mnd
		return tcell.ColorWhite
	case altitude < 10000: //nolint:mnd
		return tcell.ColorYellow
	case altitude < 20000: //nolint:mnd
		return tcell.ColorGreen
	case altitude < 30000: //nolint:mnd
		return tcell.ColorLightBlue
	case altitude < 40000: //nolint:mnd
		return tcell.ColorDarkBlue
	case altitude < 50000: //nolint:mnd
		return tcell.ColorPurple
	case altitude < 60000: //nolint:mnd
		return tcell.ColorRed
	default:
		return tcell.ColorWhite
	}
}
