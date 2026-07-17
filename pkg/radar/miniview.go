package radar

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/hyperized/uAirwaves/pkg/airplane"
	"github.com/hyperized/uAirwaves/pkg/airports"
	"github.com/hyperized/uAirwaves/pkg/location"
	"github.com/hyperized/uAirwaves/pkg/scope"
)

const (
	// miniMinScopeNm is the floor for the mini-scope range. Below
	// 5 nm the auto-fit produces a single ring that crowds the
	// crosshair, which reads worse than a slightly oversized scope.
	miniMinScopeNm = 5.0
	// miniScopePaddingFactor pads the auto-fit distance so the
	// plane symbol doesn't sit on the outermost ring. 1.2 = 20 %
	// headroom which leaves the callsign / altitude tags inside
	// the frame without clipping.
	miniScopePaddingFactor = 1.2
	// miniScopeIncrementNm is the rounding step for the auto-fit
	// scope. Picked to mirror the main radar's default Scope.Increment
	// so the operator's eye reads similar tick spacing in both views.
	miniScopeIncrementNm = 5.0
	// miniRingCount controls how many concentric rings the mini
	// scope draws. Two rings (mid + outer) are enough for spatial
	// reference at this size without crowding the trail.
	miniRingCount = 2
)

// miniToggles returns the per-frame toggle set the mini-scope
// renders with. Trail is always rendered long here (the panel
// exists *to* show the full trail) and heat/auto-scope are
// inert in this view. Heading folds in the same wall-clock blink
// phase the main radar uses, so both views pulse in sync — the
// operator never sees the heading line steady in one view and
// blinking in the other.
func miniToggles(now time.Time) drawToggles {
	return drawToggles{
		heading: headingVisible(now),
		trail:   TrailLong,
	}
}

// MiniView is a single-flight scope inset shown inside the flight
// details panel. It centres on the selected plane (not the
// receiver) so the operator sees the aircraft's airspace in
// detail; when the receiver itself falls inside the rendered
// scope it appears as a regular `X` crosshair at its projected
// position. Auto-scope fits the rendered ring to encompass the
// plane's full known trail; the operator can disable it and
// drive the range manually with the same +/- and 'a' keys that
// control the main radar, redirected by the key dispatcher
// while the details panel is open.
//
// Thread-safety: SetSnapshot/Clear (called from the UI ticker)
// take a Lock; Draw (called by tview on the main loop) takes
// RLock. scope.Scope owns its own mutex.
type MiniView struct {
	*tview.Box

	myLocation *location.Location
	myScope    *scope.Scope

	mu        sync.RWMutex
	snap      airplane.Snapshot
	hasSnap   bool
	autoScope bool
}

// NewMiniView returns a MiniView wired to the given receiver
// location. The view starts in the "no snapshot" state and
// renders an empty scope (centred on the receiver) until
// SetSnapshot lands the first plane.
func NewMiniView(loc *location.Location) *MiniView {
	return &MiniView{
		Box:        tview.NewBox().SetBorder(false),
		myLocation: loc,
		myScope:    scope.New(),
		autoScope:  true,
	}
}

// SetSnapshot replaces the plane the mini-scope renders. Called
// from the UI ticker once per redraw — the snapshot is a value
// copy, no aliasing with the live airplane.
func (m *MiniView) SetSnapshot(snap airplane.Snapshot) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.snap = snap
	m.hasSnap = true
}

// Clear drops the held snapshot so subsequent Draws render just
// the empty scope. Used when the selected plane gets pruned or
// the operator closes the details panel.
func (m *MiniView) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.hasSnap = false
	m.snap = airplane.Snapshot{}
}

// ToggleAutoScope flips the mini view's auto-fit behaviour. When
// off the manual scope range (driven by +/-) is used as-is; when
// on the scope grows to encompass the plane's known trail every
// tick.
func (m *MiniView) ToggleAutoScope() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.autoScope = !m.autoScope
}

// GetAutoScopeEnabled returns whether the mini view's auto-fit
// scoping is currently active.
func (m *MiniView) GetAutoScopeEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.autoScope
}

// IncrementScope grows the manual scope range one step and
// implicitly disables auto-scope so the operator's manual zoom
// sticks instead of getting overwritten on the next redraw.
func (m *MiniView) IncrementScope() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.autoScope = false
	m.myScope.Update(scope.WithCurrent(m.myScope.GetCurrent() + m.myScope.GetIncrement()))
}

// DecrementScope shrinks the manual scope range one step (with
// the same auto-scope disable side effect as IncrementScope).
func (m *MiniView) DecrementScope() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.autoScope = false
	m.myScope.Update(scope.WithCurrent(m.myScope.GetCurrent() - m.myScope.GetIncrement()))
}

// GetScopeRange returns the current rendered scope range in
// nautical miles — useful for footer / debug rendering that
// wants to expose the live value.
func (m *MiniView) GetScopeRange() float64 {
	return m.myScope.GetCurrent()
}

// Draw renders the mini-scope onto screen. Centre is the
// selected plane's lat/lon (so the operator sees the airspace
// the contact is flying through). The receiver crosshair is
// painted when it falls inside the rendered scope. Auto-scope
// keeps the scope wide enough to show the whole known trail;
// once the operator zooms via +/- it switches to the manual
// scope value until ToggleAutoScope reverts.
func (m *MiniView) Draw(screen tcell.Screen) {
	m.DrawForSubclass(screen, m)

	innerX, innerY, width, height := m.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	screenCenterX, screenCenterY := innerX+width/2, innerY+height/2

	m.mu.RLock()
	snap := m.snap
	hasSnap := m.hasSnap
	autoScope := m.autoScope
	m.mu.RUnlock()

	receiverLat, receiverLon := m.myLocation.GetCoordinates()
	centerLat, centerLon := miniCenter(snap, hasSnap, receiverLat, receiverLon)

	scopeRange := m.scopeForFrame(snap, hasSnap, autoScope, centerLat, centerLon)
	xScale, yScale := miniScales(width, height, scopeRange)

	drawMiniRings(screen, screenCenterX, screenCenterY, xScale, yScale, scopeRange)
	drawMiniCompass(screen, innerX, innerY, screenCenterX, screenCenterY, width, height)
	drawAirports(screen, airports.All(), airportFrame{
		centerX: screenCenterX, centerY: screenCenterY,
		xScale: xScale, yScale: yScale,
		centerLat: centerLat, centerLon: centerLon,
		scopeRange: scopeRange,
	})

	drawMiniReceiver(screen, receiverLat, receiverLon, airportFrame{
		centerX: screenCenterX, centerY: screenCenterY,
		xScale: xScale, yScale: yScale,
		centerLat: centerLat, centerLon: centerLon,
		scopeRange: scopeRange,
	})

	if !hasSnap || (snap.Latitude == 0 && snap.Longitude == 0) {
		return
	}

	drawMiniPlaneAndTrail(screen, snap, screenCenterX, screenCenterY, xScale, yScale, centerLat, centerLon)
}

// scopeForFrame picks the per-frame range. The autoScope flag
// is the *current per-frame snapshot of that toggle* — Draw
// reads it once under the lock and threads it through, so this
// helper is the operational dispatch, not a flag selector. Auto
// fits to the snapshot's trail and writes the result back into
// the scope so a later switch to manual mode lands on the value
// the operator was just looking at instead of jumping to an
// unrelated stale range.
//
//nolint:revive // flag-parameter: autoScope is the per-frame snapshot, not a behaviour selector.
func (m *MiniView) scopeForFrame(
	snap airplane.Snapshot, hasSnap, autoScope bool,
	centerLat, centerLon float64,
) float64 {
	if !autoScope {
		return m.myScope.GetCurrent()
	}

	if !hasSnap {
		return miniMinScopeNm
	}

	autoFit := miniScopeRange(snap, centerLat, centerLon)
	m.myScope.Update(scope.WithCurrent(autoFit))

	return autoFit
}

// miniCenter returns the lat/lon the mini-scope centres on.
// When a snapshot with a resolved position is held, that's the
// centre; otherwise we fall back to the receiver so the view
// still draws meaningful rings on first paint (before any plane
// is selected or before the first position frame lands). The
// hasSnap argument is the per-frame snapshot-held marker, not a
// behaviour selector — it disambiguates "snap is the zero value
// because no plane is selected" from "snap holds a plane that
// genuinely sits on the equator" without forcing callers to
// inspect Snapshot's zero value.
//
//nolint:revive,nonamedreturns // flag-parameter: hasSnap is data, not control; (lat, lon) reads clearer named.
func miniCenter(snap airplane.Snapshot, hasSnap bool, receiverLat, receiverLon float64) (lat, lon float64) {
	if hasSnap && (snap.Latitude != 0 || snap.Longitude != 0) {
		return snap.Latitude, snap.Longitude
	}

	return receiverLat, receiverLon
}

// drawMiniReceiver projects the receiver onto the mini-scope.
// When the receiver coordinates are unresolved (0, 0 sentinel)
// or fall outside the current scope, the call is a no-op so the
// screen stays clean. Reuses the airportFrame projection bundle
// the airport overlay already uses so the argument fan stays
// inside revive's count gate.
func drawMiniReceiver(screen tcell.Screen, receiverLat, receiverLon float64, frame airportFrame) {
	if receiverLat == 0 && receiverLon == 0 {
		return
	}

	dLat := receiverLat - frame.centerLat
	dLon := (receiverLon - frame.centerLon) * math.Cos(frame.centerLat*degreesToRadiansRatio)
	nmY := dLat * nauticalMilePerDegree
	nmX := dLon * nauticalMilePerDegree

	if math.Sqrt(nmX*nmX+nmY*nmY) > frame.scopeRange {
		return
	}

	receiverX := frame.centerX + int(nmX*frame.xScale)
	receiverY := frame.centerY - int(nmY*frame.yScale/yMultiplier)

	screen.SetContent(receiverX, receiverY, 'X', nil,
		tcell.StyleDefault.Foreground(tcell.ColorDarkMagenta))
}

// miniScales returns the (xScale, yScale) the mini-scope uses for
// projecting nautical-mile offsets onto the inner box. Unlike the
// main radar, which assumes a roughly 2:1 (wide:tall) box where
// terminalCharacterRatio compensates for the cell aspect, the
// mini-scope can sit in a much-wider-than-tall slot. We compute
// the raw per-axis scales, then lock both to the smaller one so
// the rendered rings stay visually circular — at the cost of
// leaving a margin on the more generous axis.
//
//nolint:nonamedreturns // (xScale, yScale) reads clearer named at this signature.
func miniScales(width, height int, scopeRange float64) (xScale, yScale float64) {
	xRaw := float64(width) / (scopeRange * 2)
	yRaw := float64(height) / (scopeRange * 2) * terminalCharacterRatio

	uniform := math.Min(xRaw, yRaw)

	return uniform, uniform
}

// drawMiniPlaneAndTrail is the per-snapshot half of the Draw
// pipeline: project the lat/lon to screen coords and hand off to
// the shared trail + plane helpers. Pulled out so Draw stays
// under revive's function-length gate.
func drawMiniPlaneAndTrail(
	screen tcell.Screen, snap airplane.Snapshot, centerX, centerY int,
	xScale, yScale, centerLat, centerLon float64,
) {
	dLat := snap.Latitude - centerLat
	dLon := (snap.Longitude - centerLon) * math.Cos(centerLat*degreesToRadiansRatio)
	nmY := dLat * nauticalMilePerDegree
	nmX := dLon * nauticalMilePerDegree
	planeX := centerX + int(nmX*xScale)
	planeY := centerY - int(nmY*yScale/yMultiplier)

	drawTrail(screen, sliceTrail(snap.PositionHistory, TrailLong),
		centerX, centerY, xScale, yScale, centerLat, centerLon)

	planeStyle := tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorBlack)
	drawPlane(screen, snap, planeX, planeY, planeStyle, miniToggles(time.Now()))
}

// miniScopeRange picks a scope size that comfortably contains
// the snapshot's known trail relative to the supplied centre.
// Walks every position entry (and the current fix), takes the
// maximum distance from the centre, pads, rounds up to the next
// 5 nm step, and clamps to a 5 nm floor. Returns the floor when
// nothing resolvable is in the snapshot.
func miniScopeRange(snap airplane.Snapshot, centerLat, centerLon float64) float64 {
	maxDist := nauticalMilesFromCenter(centerLat, centerLon, snap.Latitude, snap.Longitude)

	for _, entry := range snap.PositionHistory {
		if dist := nauticalMilesFromCenter(
			centerLat, centerLon, entry.Latitude, entry.Longitude,
		); dist > maxDist {
			maxDist = dist
		}
	}

	if maxDist <= 0 {
		return miniMinScopeNm
	}

	padded := maxDist * miniScopePaddingFactor
	rounded := math.Ceil(padded/miniScopeIncrementNm) * miniScopeIncrementNm

	return math.Max(rounded, miniMinScopeNm)
}

// nauticalMilesFromCenter is the same flat-earth approximation
// the main radar uses (good for the small spans the mini-scope
// covers). Returns 0 for unresolved positions so the caller can
// treat "no data" as "use the floor scope".
func nauticalMilesFromCenter(centerLat, centerLon, lat, lon float64) float64 {
	if lat == 0 && lon == 0 {
		return 0
	}

	dLat := lat - centerLat
	dLon := (lon - centerLon) * math.Cos(centerLat*degreesToRadiansRatio)
	nmY := dLat * nauticalMilePerDegree
	nmX := dLon * nauticalMilePerDegree

	return math.Sqrt(nmX*nmX + nmY*nmY)
}

// drawMiniRings renders miniRingCount concentric rings, the
// outermost one matching scopeRange. Each ring carries a small
// "Xnm" label on its east edge so the operator can read the scale
// without inspecting the surrounding chrome.
func drawMiniRings(screen tcell.Screen, centerX, centerY int, xScale, yScale, scopeRange float64) {
	ringStyle := tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorBlack)
	step := scopeRange / miniRingCount

	for ring := step; ring <= scopeRange; ring += step {
		drawCircle(screen, centerX, centerY,
			int(ring*xScale), int(ring*yScale/2), //nolint:mnd // /2 mirrors the main radar's ring scaling.
			ringStyle,
		)

		if ring < scopeRange {
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

// drawMiniCompass paints the four cardinals at the panel edges.
// Mirrors the main radar's compass treatment so the swap from
// big to mini scope reads as the same instrument at a smaller
// scale.
func drawMiniCompass(
	screen tcell.Screen, innerX, innerY, centerX, centerY, width, height int,
) {
	style := tcell.StyleDefault.Foreground(tcell.ColorGreen)

	tview.Print(screen, "N", centerX, innerY, 1, tview.AlignLeft, tcell.ColorGreen)
	tview.Print(screen, "S", centerX, innerY+height-1, 1, tview.AlignLeft, tcell.ColorGreen)
	screen.SetContent(innerX+width-1, centerY, 'E', nil, style)
	screen.SetContent(innerX, centerY, 'W', nil, style)
}
