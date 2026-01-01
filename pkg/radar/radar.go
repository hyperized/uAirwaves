package radar

import (
	"fmt"
	"math"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplanes"
)

const YMultiplier = 1        // 2 before
const HeadingLineLength = 10 // pixels for heading indicator
const CircleCount = 4

// TODO: Increase / descrease scope range
// TODO: enable / disable heading trails
// TODO: filter altitudes

// View is a custom tview component
type View struct {
	*tview.Box
	scopeRange float64
	centerLat  float64
	centerLon  float64
	planes     airplanes.List
	showTrails bool
}

type PlanePos struct {
	Lat      float64
	Lon      float64
	Callsign string
}

func NewView() *View {
	return &View{
		Box:        tview.NewBox().SetBorder(true).SetTitle("Radar Scope (25-100nm)"),
		showTrails: true,
		scopeRange: 100,
	}
}

func (r *View) SetCenter(lat, lon float64) {
	r.centerLat = lat
	r.centerLon = lon
}

func (r *View) SetPlanes(planes airplanes.List) {
	r.planes = planes
}

// SetScopeRange sets the radar scope range in nautical miles
func (r *View) SetScopeRange(rangeNm float64) {
	r.scopeRange = rangeNm
	r.Box = tview.NewBox().SetBorder(true).SetTitle(fmt.Sprintf("Radar Scope (%.0f-%.0fnm)", r.scopeRange/CircleCount, r.scopeRange))
}

// ToggleTrails toggles the display of heading trails
func (r *View) ToggleTrails() {
	r.showTrails = !r.showTrails
}

// GetScopeRange returns the current scope range
func (r *View) GetScopeRange() float64 {
	return r.scopeRange
}

// GetTrailsEnabled returns whether trails are enabled
func (r *View) GetTrailsEnabled() bool {
	return r.showTrails
}

func (r *View) Draw(screen tcell.Screen) {
	r.Box.DrawForSubclass(screen, r)
	x, y, width, height := r.GetInnerRect()

	centerX, centerY := x+width/2, y+height/2

	// Scales: Terminal characters are usually ~2x taller than wide.
	// We adjust yScale to keep the rings circular.
	const nmToDegree = 1.0 / 60.0
	xScale := float64(width) / (r.scopeRange * 2)
	yScale := float64(height) / (r.scopeRange * 2) * 2.0 // 2.0

	increments := r.scopeRange / CircleCount

	// 1. Draw Scope Rings
	ringStyle := tcell.StyleDefault.Foreground(tcell.ColorGreen).Background(tcell.ColorBlack)
	for ring := increments; ring <= r.scopeRange; ring += increments {
		drawCircle(screen, centerX, centerY, int(ring*xScale), int(ring*yScale/2), ringStyle)
		tview.Print(screen, fmt.Sprintf("%0.0fnm", ring), centerX+int(ring*xScale), centerY, 5, tview.AlignLeft, tcell.ColorGreen)
	}

	// 2. Draw Center Point (You)
	screen.SetContent(centerX, centerY, '+', nil, tcell.StyleDefault.Foreground(tcell.ColorRed))

	// 3. Draw Planes
	planeStyle := tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorBlack)

	for _, p := range r.planes {
		plane := p.GetSnapshot()

		lat := plane.Latitude
		lon := plane.Longitude
		callsign := plane.Callsign

		if lat == 0 || lon == 0 {
			continue // Skip planes without a location
		}

		// Calculate delta in degrees
		dLat := lat - r.centerLat
		dLon := (lon - r.centerLon) * math.Cos(r.centerLat*math.Pi/180.0)

		// Convert degrees to Nautical Miles (~60nm per degree)
		nmY := dLat * 60.0
		nmX := dLon * 60.0

		// Skip if outside scope
		dist := math.Sqrt(nmX*nmX + nmY*nmY)
		if dist > r.scopeRange {
			continue
		}

		// Map to screen coordinates
		// Note: Y is inverted in screen space (up is negative)
		px := centerX + int(nmX*xScale)
		py := centerY - int(nmY*yScale/YMultiplier)

		// Use color-coded altitude display
		altColor := getAltitudeColor(plane.Altitude)

		// Draw heading indicator line if heading is valid
		if plane.Heading != -1 && r.showTrails {
			headingStyle := tcell.StyleDefault.Foreground(altColor).Background(tcell.ColorBlack)
			drawHeadingLine(screen, px, py, plane.Heading, headingStyle)
		}

		screen.SetContent(px, py, '+', nil, planeStyle)
		if callsign == "" {
			tview.Print(screen, fmt.Sprintf("%s", plane.ICAO), px+1, py, 20, tview.AlignLeft, tcell.ColorYellow)
		} else {
			tview.Print(screen, fmt.Sprintf("%s", plane.Callsign), px+1, py, 20, tview.AlignLeft, tcell.ColorYellow)
		}

		/// Display altitude with a vertical rate symbol
		vertSymbol := getVertRateSymbol(plane.VertRate)
		vertColor := getVerticalRateColor(plane.VertRate)

		// Print altitude in altitude color, then vertical rate symbol in vert rate color
		altText := fmt.Sprintf("%s ", altitudeToFL(plane.Altitude))
		tview.Print(screen, altText, px+1, py+1, len(altText), tview.AlignLeft, altColor)
		tview.Print(screen, vertSymbol, px+1+len(altText), py+1, 1, tview.AlignLeft, vertColor)
	}
}

func drawHeadingLine(screen tcell.Screen, x, y int, heading float64, style tcell.Style) {
	// Convert heading to radians
	// Aviation: 0° = North, 90° = East, 180° = South, 270° = West (clockwise)
	// Screen: X increases right, Y increases down
	// North (0°) should point up (-Y), East (90°) should point right (+X)
	radians := heading * math.Pi / 180.0

	// Calculate direction vector
	// sin(heading) gives us X component (East/West)
	// -cos(heading) gives us Y component (North is up, so negative)
	dx := math.Sin(radians)
	dy := -math.Cos(radians) * 0.5 // 0.5 to compensate for terminal character aspect ratio

	// Draw a dotted line using a Bresenham-like approach
	for i := 1; i <= HeadingLineLength; i++ {
		// Calculate position along the line
		px := x + int(float64(i)*dx+0.5) // +0.5 for rounding
		py := y + int(float64(i)*dy+0.5)

		// Draw dot every other step for dotted appearance
		if i%2 == 1 {
			screen.SetContent(px, py, '·', nil, style)
		}
	}
}

// Simple Bresenham-like circle helper for terminal
func drawCircle(screen tcell.Screen, cx, cy, rx, ry int, style tcell.Style) {
	for i := 0; i < 360; i += 2 {
		rad := float64(i) * math.Pi / 180.0
		x := cx + int(float64(rx)*math.Cos(rad))
		y := cy + int(float64(ry)*math.Sin(rad))
		screen.SetContent(x, y, '.', nil, style)
	}
}

func getVerticalRateColor(vertRate float64) tcell.Color {
	switch {
	case vertRate > 500:
		return tcell.ColorGreen
	case vertRate < -500:
		return tcell.ColorRed
	default:
		return tcell.ColorLightBlue
	}
}

// getVertRateSymbol returns an ASCII symbol for vertical rate
func getVertRateSymbol(vertRate float64) string {
	switch {
	case vertRate > 500:
		return "^" // Climbing significantly
	case vertRate > 100:
		return "+" // Climbing
	case vertRate < -500:
		return "v" // Descending significantly
	case vertRate < -100:
		return "-" // Descending
	default:
		return "=" // Level flight (within ±100 fpm)
	}
}

// altitudeToFL converts altitude in feet to flight level format (three digits max)
// Example: 35000 ft -> "350", 5000 ft -> "050", 500 ft -> "005"
func altitudeToFL(altitude float64) string {
	fl := int(altitude / 100)
	return fmt.Sprintf("FL%03d", fl)
}

// getAltitudeColor returns a color based on altitude in 5,000 ft increments
// 0-5k: Dark Blue, 5-10k: Blue, 10-15k: Cyan, 15-20k: Green, 20-25k: Yellow,
// 25-30k: Orange, 30-35k: Red, 35-40k: Magenta, 40-45k: Purple, 45k+: White

func getAltitudeColor(altitude float64) tcell.Color {
	switch {
	case altitude < 5000:
		return tcell.ColorDarkBlue
	case altitude < 10000:
		return tcell.ColorBlue
	case altitude < 15000:
		return tcell.ColorDarkCyan
	case altitude < 20000:
		return tcell.ColorGreen
	case altitude < 25000:
		return tcell.ColorYellow
	case altitude < 30000:
		return tcell.ColorOrange
	case altitude < 35000:
		return tcell.ColorRed
	case altitude < 40000:
		return tcell.ColorPurple
	case altitude < 45000:
		return tcell.ColorDarkMagenta
	default:
		return tcell.ColorWhite
	}
}
