// Package radar draws the live ADS-B picture as a text-mode
// plan-position indicator. The main scope centres on the receiver
// and carries range rings, altitude-coloured plane markers and
// their trails; a plane-centred MiniView insets a single selected
// contact. Terminal cells run about 2:1 tall-to-wide, so vertical
// offsets are scaled by terminalCharacterRatio to keep rings
// circular, and screen Y is inverted so northward offsets subtract
// from the centre row.
package radar

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/hyperized/uAirwaves/pkg/airplane"
	"github.com/hyperized/uAirwaves/pkg/airplanes"
	"github.com/hyperized/uAirwaves/pkg/airports"
	"github.com/hyperized/uAirwaves/pkg/location"
	"github.com/hyperized/uAirwaves/pkg/scope"
	"github.com/rivo/tview"
)

const (
	// yMultiplier divides yScale when placing planes, trails and
	// heat dots so they land on the same ellipse the scope rings
	// draw. drawScopeRings halves yScale via int(ring*yScale/2);
	// without the matching divisor, plane offsets are 2× the ring's
	// vertical radius and most N/S contacts are clipped off-screen.
	yMultiplier            = 2
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

	// airportLabelPad is the pixel offset from the airport
	// symbol to the start of its ICAO label. 2 columns leaves
	// room for the symbol (1 col) plus a one-space gutter so
	// labels don't collide with the marker glyph.
	airportLabelPad = 2
	// airportLabelWidth caps the label width so an unusually
	// long ICAO can't bleed onto another airport's row. ICAO
	// codes are 4 chars; the cap leaves a one-char safety margin.
	airportLabelWidth = 5

	// sweepSpinnerFrames is the number of positions in the
	// "loading" ring drawn around the radar centre while the
	// gain sweep is in progress. Eight matches a compass rose
	// (N, NE, E, SE, S, SW, W, NW), which is the historical
	// radar-style layout.
	sweepSpinnerFrames = 8

	// sweepSpinnerStep is how long each spinner frame stays
	// visible. With sweepSpinnerFrames=8, a full rotation takes
	// 2 s — fast enough to feel alive at the operator console,
	// slow enough that single-cell movement reads cleanly on a
	// 60-Hz terminal. The main UI ticker also drops to this
	// cadence while sweeping so the redraw rate matches.
	sweepSpinnerStep = 250 * time.Millisecond

	// headingBlinkPeriod is the full on→off cycle for the heading
	// projection dots. Picked at 1.5 s so the 1 Hz UI ticker
	// can't lock onto a single phase — the operator sees the
	// projection pulse off intermittently, which visually
	// separates it from the steady, altitude-coloured trail dots.
	headingBlinkPeriod = 1500 * time.Millisecond
	// headingBlinkOn is how much of each period the heading is
	// visible. 1 s on / 0.5 s off (~67 % duty cycle) makes the
	// blink read as "slow pulse" — the projection stays present
	// most of the time but visibly flickers off often enough to
	// be unmistakably distinct from the trail.
	headingBlinkOn = 1000 * time.Millisecond

	// shortTrailEntries caps the number of PositionEntry the
	// renderer paints when TrailMode is Short. Matches the old
	// hard cap in pkg/airplane so the short setting feels
	// identical to the pre-toggle behaviour; "long" mode draws
	// the full slice.
	shortTrailEntries = 10

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

// TrailMode is the operator-cycled state of the radar's trail
// rendering: Off (no dots), Short (last shortTrailEntries fixes,
// matches the old default), Long (every recorded fix until the
// plane is pruned). The 't' key cycles in that order, mirroring
// the natural verbosity gradient.
type TrailMode uint8

// Trail-mode enumerators. The zero value is TrailOff so a freshly
// constructed View with no explicit mode renders no trail — same
// no-side-effect default the other indicator bools follow.
const (
	TrailOff TrailMode = iota
	TrailShort
	TrailLong
)

// String returns the operator-facing label for a trail mode.
// Used by the footer renderer; kept on the type so callers don't
// hand-roll switch statements at every render site.
func (m TrailMode) String() string {
	switch m {
	case TrailOff:
		return "off"
	case TrailShort:
		return "short"
	case TrailLong:
		return "long"
	default:
		return "?"
	}
}

// nextTrailMode advances the cycle: off → short → long → off.
// Hoisted to a package function so the cycle order has one
// canonical definition and tests can pin it without touching
// View state. Unrecognised inputs fall through to TrailOff
// (treated identically to TrailLong) so an out-of-range value
// recovers cleanly on the next cycle.
func nextTrailMode(current TrailMode) TrailMode {
	if current == TrailOff {
		return TrailShort
	}

	if current == TrailShort {
		return TrailLong
	}

	return TrailOff
}

// SweepIndicator reports whether a long-running boot operation
// (typically the SDR gain auto-sweep) is in progress. The radar
// uses this to swap its centre crosshair for a circling spinner
// so the operator sees that the system is making progress even
// before any frames flow. The full *adsb.ADSB satisfies this
// interface; tests can pass a small stub.
type SweepIndicator interface {
	Sweeping() bool
}

// View is a custom tview component.
type View struct {
	*tview.Box

	headingIndicator bool
	headingBlinking  bool
	trailMode        TrailMode
	heatIndicator    bool
	autoScope        bool
	airportIndicator bool
	planes           *airplanes.Airplanes
	myLocation       *location.Location
	myScope          *scope.Scope
	heat             *heatMap
	sweep            SweepIndicator
	mu               sync.RWMutex
}

// New initializes a new radar scope view. sweep may be nil for
// callers (tests, replay-mode) that don't have a real sweep
// source — the centre crosshair stays static in that case.
func New(planes *airplanes.Airplanes, myLocation *location.Location, sweep SweepIndicator) *View {
	return &View{
		Box:              tview.NewBox().SetBorder(false).SetBorderPadding(1, 1, 1, 1),
		headingIndicator: true,
		headingBlinking:  true,
		trailMode:        TrailShort,
		heatIndicator:    false,
		airportIndicator: true,
		myScope:          scope.New(),
		autoScope:        true,
		planes:           planes,
		myLocation:       myLocation,
		heat:             newHeatMap(),
		sweep:            sweep,
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

// SetHeadingBlinking toggles the heading-line slow-blink effect.
// Production code leaves the default (true) so the projection
// pulses visibly against the steady trail; tests disable it to
// pin the on phase and assert dot presence deterministically.
func (r *View) SetHeadingBlinking(on bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.headingBlinking = on
}

// CycleTrailMode advances the trail-length cycle:
// off → short → long → off. Cycling is the only mutator;
// callers can't jump straight to a specific mode, which keeps
// the operator's mental model aligned with what the 't' key
// actually does at the keyboard.
func (r *View) CycleTrailMode() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.trailMode = nextTrailMode(r.trailMode)
}

// GetTrailMode returns the current trail-length setting. The
// footer reads this to render the on/short/long label and the
// snapshot reader folds it into drawToggles so render code stays
// lock-free at View level.
func (r *View) GetTrailMode() TrailMode {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.trailMode
}

// ToggleHeatIndicator toggles the heat map overlay.
func (r *View) ToggleHeatIndicator() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.heatIndicator = !r.heatIndicator
}

// ToggleAirportIndicator toggles the static airport overlay
// (curated set drawn beneath the live plane layer).
func (r *View) ToggleAirportIndicator() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.airportIndicator = !r.airportIndicator
}

// GetAirportIndicatorEnabled returns whether the airport overlay
// is currently visible. Used by the footer renderer so the
// operator can see the toggle's state at a glance.
func (r *View) GetAirportIndicatorEnabled() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.airportIndicator
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

// drawToggles is a point-in-time copy of the five boolean
// indicators Draw consults. Bundled into a struct so the lock
// window in snapshotToggles is a single statement and so the
// downstream rendering helpers receive one read-only argument
// instead of a fan of control-flag bools.
type drawToggles struct {
	heading, autoScope, heat, airport bool
	trail                             TrailMode
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
	planeList := r.planes.Sorted(centerLatitude, centerLongitude, airplanes.WithTrails())

	if toggles.autoScope {
		r.applyAutoScope(planeList, centerLatitude, centerLongitude)
	}

	xScale, yScale := r.calculateScales(width, height)

	// 1. Draw Scope Rings
	r.drawScopeRings(screen, centerX, centerY, xScale, yScale)

	// 2. Draw Center Point (You) and compass indicators
	r.drawCenterPoint(screen, centerX, centerY)
	r.drawCompassIndicators(screen, innerX, innerY, centerX, centerY, width, height)

	// 3. Draw Airports (static overlay, beneath the live plane
	//    layer so plane markers always win on overlap).
	if toggles.airport {
		drawAirports(screen, airports.All(), airportFrame{
			centerX: centerX, centerY: centerY,
			xScale: xScale, yScale: yScale,
			centerLat: centerLatitude, centerLon: centerLongitude,
			scopeRange: r.myScope.GetCurrent(),
		})
	}

	// 4. Draw Planes (also accumulates heat as a side effect)
	r.drawPlanes(screen, planeList, centerX, centerY, xScale, yScale, centerLatitude, centerLongitude, toggles)

	// 5. Decay and draw heat map last so nothing overwrites it
	r.heat.decay()

	if toggles.heat {
		r.heat.draw(screen, centerX, centerY, xScale, yScale)
	}
}

// snapshotToggles captures the toggle indicators Draw needs under
// a single short RLock window. The struct return lets the rest of
// Draw run lock-free at View level while the heatmap and scope
// keep their own internal serialisation.
//
// The heading bit folds in the blink phase: if blinking is on and
// the wall clock currently sits in the off slice of the period,
// heading drops to false for this frame even though the indicator
// is enabled — that gives the slow-blink effect without
// per-renderer state.
func (r *View) snapshotToggles() drawToggles {
	r.mu.RLock()
	defer r.mu.RUnlock()

	heading := r.headingIndicator
	if heading && r.headingBlinking && !headingVisible(time.Now()) {
		heading = false
	}

	return drawToggles{
		heading:   heading,
		autoScope: r.autoScope,
		heat:      r.heatIndicator,
		trail:     r.trailMode,
		airport:   r.airportIndicator,
	}
}

// headingVisible returns whether the heading projection should
// paint at the supplied wall-clock instant. Pure function so
// every Draw call lands on a deterministic phase derived from
// the clock — no shared counter, no atomic, no lock.
func headingVisible(now time.Time) bool {
	phase := now.UnixNano() % headingBlinkPeriod.Nanoseconds()

	return phase < headingBlinkOn.Nanoseconds()
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

// drawCenterPoint draws the receiver marker at the radar's centre.
// Default: a static 'X' in dark magenta. While the supplied sweep
// indicator reports true the X is hidden and a single bright dot
// circles the centre cell at the 8 cardinal/intercardinal
// positions, advancing one step per sweepSpinnerStep — the
// operator gets a clear "boot still in progress" cue while the
// auto-sweep walks the gain grid and no frames are flowing yet.
func (r *View) drawCenterPoint(screen tcell.Screen, centerX, centerY int) {
	if r.sweep == nil || !r.sweep.Sweeping() {
		screen.SetContent(centerX, centerY, 'X', nil, tcell.StyleDefault.Foreground(tcell.ColorDarkMagenta))

		return
	}

	dx, dy := spinnerOffset(time.Now())
	screen.SetContent(centerX+dx, centerY+dy, '*', nil,
		tcell.StyleDefault.Foreground(tcell.ColorYellow))
}

// spinnerPositions is the clockwise sequence of (dx, dy) offsets
// the radar's centre spinner cycles through during a sweep.
// Indexed by frame number: 0 = N, 1 = NE, 2 = E, 3 = SE, 4 = S,
// 5 = SW, 6 = W, 7 = NW. Layout matches the radar.go compass
// indicators so a viewer reads the rotation direction
// intuitively (clockwise like a real radar's PPI sweep).
//
//nolint:gochecknoglobals // read-only lookup table, scoped to the spinner animation.
var spinnerPositions = [sweepSpinnerFrames][2]int{
	{0, -1},  // N
	{1, -1},  // NE
	{1, 0},   // E
	{1, 1},   // SE
	{0, 1},   // S
	{-1, 1},  // SW
	{-1, 0},  // W
	{-1, -1}, // NW
}

// spinnerOffset maps the supplied wall-clock instant onto one of
// the eight (dx, dy) positions around the radar centre. The
// rotation is purely a function of time, so a new Draw call (even
// out-of-band from the UI ticker) always lands on the position
// the clock dictates — no frame-counter state to keep in sync.
//
//nolint:nonamedreturns // (dx, dy) reads clearer named at this signature.
func spinnerOffset(now time.Time) (dx, dy int) {
	frame := int(now.UnixNano()/sweepSpinnerStep.Nanoseconds()) % sweepSpinnerFrames
	pos := spinnerPositions[frame]

	return pos[0], pos[1]
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

		if toggles.trail != TrailOff {
			drawTrail(
				screen, sliceTrail(plane.PositionHistory, toggles.trail),
				centerX, centerY, xScale, yScale,
				centerLatitude, centerLongitude,
			)
		}

		drawPlane(screen, plane, planeX, planeY, planeStyle, toggles)
	}
}

func drawPlane(
	screen tcell.Screen, plane airplane.Snapshot, planeX, planeY int, style tcell.Style, toggles drawToggles,
) {
	altColor := getFlightLevelColor(plane.Altitude)

	// Draw heading indicator line if heading is valid. The heading
	// line is a projection (where the plane *will* be), not a flown
	// path, so it renders white — altitude colour is reserved for
	// the trail, which represents actual flight levels flown.
	if plane.Heading != -1 && toggles.heading {
		headingStyle := tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorBlack)
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

// sliceTrail returns the slice of position entries the renderer
// should paint for the given trail mode. Long passes the whole
// history through; Short clamps to the last shortTrailEntries
// fixes (matching the old hard cap); everything else short-
// circuits to nil so the caller can still hand the result to
// drawTrail without an extra guard (drawTrail over an empty
// slice is a no-op).
func sliceTrail(history []airplane.PositionEntry, mode TrailMode) []airplane.PositionEntry {
	if mode == TrailLong {
		return history
	}

	if mode == TrailShort {
		if len(history) <= shortTrailEntries {
			return history
		}

		return history[len(history)-shortTrailEntries:]
	}

	return nil
}

// trailGlyph is the rune painted for each historical position
// fix. A filled bullet ('•', U+2022) is deliberately larger and
// more solid than the heading line's middle dot ('·', U+00B7),
// so the operator can distinguish "where I've been" from "where
// I'm projected to go" by shape alone — colour isn't load-bearing
// for terminals that render dark altitude buckets dimly against
// the black background.
const trailGlyph = '•'

// drawTrail renders historical position dots behind a plane.
// Each dot is coloured by the flight level the plane was at when
// the fix was sampled (entry.Altitude) using the same palette the
// plane symbol uses, so the operator reads the altitude profile
// of the flight directly off the scope. Entries with unresolved
// positions (lat/lon both zero) are skipped.
func drawTrail(
	screen tcell.Screen,
	history []airplane.PositionEntry,
	centerX, centerY int,
	xScale, yScale, centerLatitude, centerLongitude float64,
) {
	for _, entry := range history {
		if entry.Latitude == 0 || entry.Longitude == 0 {
			continue
		}

		dLat := entry.Latitude - centerLatitude
		dLon := (entry.Longitude - centerLongitude) * math.Cos(centerLatitude*degreesToRadiansRatio)
		nmY := dLat * nauticalMilePerDegree
		nmX := dLon * nauticalMilePerDegree

		screenX := centerX + int(nmX*xScale)
		screenY := centerY - int(nmY*yScale/yMultiplier)

		style := tcell.StyleDefault.
			Foreground(getFlightLevelColor(entry.Altitude)).
			Background(tcell.ColorBlack)
		screen.SetContent(screenX, screenY, trailGlyph, nil, style)
	}
}

// airportFrame is the projection context drawAirports needs:
// screen-space centre, per-axis scales, receiver lat/lon, and
// the active scope range. Bundled so drawAirports stays inside
// revive's argument-count gate (and so future projection tweaks
// touch one struct, not a fan of positional floats).
type airportFrame struct {
	centerX, centerY     int
	xScale, yScale       float64
	centerLat, centerLon float64
	scopeRange           float64
}

// drawAirports renders the static airport overlay. Each entry
// is projected with the same flat-earth math the planes use,
// filtered to those within the current scope, and drawn as a
// dim cyan ⊕ symbol followed by the ICAO label. The marker glyph
// sits exactly on the airport's coordinate and the label trails
// to the east — matching the plane convention.
func drawAirports(screen tcell.Screen, list []airports.Airport, frame airportFrame) {
	symbolStyle := tcell.StyleDefault.Foreground(tcell.ColorDarkCyan).Background(tcell.ColorBlack)

	for _, airport := range list {
		dLat := airport.Latitude - frame.centerLat
		dLon := (airport.Longitude - frame.centerLon) * math.Cos(frame.centerLat*degreesToRadiansRatio)
		nmY := dLat * nauticalMilePerDegree
		nmX := dLon * nauticalMilePerDegree

		if math.Sqrt(nmX*nmX+nmY*nmY) > frame.scopeRange {
			continue
		}

		screenX := frame.centerX + int(nmX*frame.xScale)
		screenY := frame.centerY - int(nmY*frame.yScale/yMultiplier)

		screen.SetContent(screenX, screenY, '⊕', nil, symbolStyle)
		tview.Print(screen, airport.ICAO,
			screenX+airportLabelPad, screenY,
			airportLabelWidth, tview.AlignLeft, tcell.ColorDarkCyan)
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

// Scope palette.
//
// Every entry clears WCAG 4.5:1 against both a black and a slate terminal
// background, which the tcell named colours mostly did not: DarkBlue sat at
// 1.5:1, Purple at 1.1:1 and Green at 2.0:1. FL300-400 is where most cruise
// traffic lives, so the old DarkBlue made the busiest band the least legible
// thing on the scope. Hexes rather than names because a terminal palette can
// remap "green" to anything it likes. TestPaletteContrast pins the ratios.
var (
	colorAltBelowFL050 = tcell.NewHexColor(0xFFFFFF) // white
	colorAltBelowFL100 = tcell.NewHexColor(0xFFFF00) // yellow
	colorAltBelowFL200 = tcell.NewHexColor(0xADFF2F) // green-yellow
	colorAltBelowFL300 = tcell.NewHexColor(0x00FFFF) // aqua
	colorAltBelowFL400 = tcell.NewHexColor(0x87CEFA) // light sky blue
	colorAltBelowFL500 = tcell.NewHexColor(0xE9A6F0) // orchid
	colorAltBelowFL600 = tcell.NewHexColor(0xFFA07A) // light salmon
	colorAltAboveFL600 = tcell.NewHexColor(0xFFFFFF) // white

	colorClimbing   = tcell.NewHexColor(0x90EE90) // light green
	colorDescending = tcell.NewHexColor(0xFFA0A0) // light red
	colorLevel      = tcell.NewHexColor(0xADD8E6) // light blue
)

// getVerticalRateColor returns a color based on vertical rate in fpm.
func getVerticalRateColor(vertRate float64) tcell.Color {
	switch {
	case vertRate > fastVerticalRate:
		return colorClimbing
	case vertRate < -fastVerticalRate:
		return colorDescending
	default:
		return colorLevel
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
		return colorAltBelowFL050
	case altitude < flightLevelSub100:
		return colorAltBelowFL100
	case altitude < flightLevelSub200:
		return colorAltBelowFL200
	case altitude < flightLevelSub300:
		return colorAltBelowFL300
	case altitude < flightLevelSub400:
		return colorAltBelowFL400
	case altitude < flightLevelSub500:
		return colorAltBelowFL500
	case altitude < flightLevelSub600:
		return colorAltBelowFL600
	default:
		return colorAltAboveFL600
	}
}
