package radar

import (
	"fmt"
	"math"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplane"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airports"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/location"
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

// miniDrawToggles is the static toggle set the mini-scope renders
// with. Heading + trail are always on (the panel exists *to* show
// trail and heading); heat and auto-scope are inert in this view.
//
//nolint:gochecknoglobals // read-only constant struct, scoped to mini-scope rendering.
var miniDrawToggles = drawToggles{heading: true, trail: true}

// MiniView is a single-flight scope inset shown inside the flight
// details panel. It renders the receiver crosshair, a small set
// of scope rings auto-sized to the selected plane's distance,
// compass cardinals, the plane's heading line, and its position
// trail. No heat-map, no plane list iteration — the view holds at
// most one snapshot.
//
// Thread-safety: SetSnapshot/Clear (called from the UI ticker)
// take a Lock; Draw (called by tview on the main loop) takes
// RLock. Both windows are short — value copies of the snapshot.
type MiniView struct {
	*tview.Box

	myLocation *location.Location

	mu      sync.RWMutex
	snap    airplane.Snapshot
	hasSnap bool
}

// NewMiniView returns a MiniView wired to the given receiver
// location. The view starts in the "no snapshot" state and
// renders just the centre crosshair until SetSnapshot is called.
func NewMiniView(loc *location.Location) *MiniView {
	return &MiniView{
		Box:        tview.NewBox().SetBorder(false),
		myLocation: loc,
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

// Draw renders the mini-scope onto screen. Layout mirrors the
// main radar (compass, rings, crosshair, plane + heading + trail)
// but with auto-fit scope sizing keyed off the single snapshot.
func (m *MiniView) Draw(screen tcell.Screen) {
	m.DrawForSubclass(screen, m)

	innerX, innerY, width, height := m.GetInnerRect()
	if width <= 0 || height <= 0 {
		return
	}

	centerX, centerY := innerX+width/2, innerY+height/2

	m.mu.RLock()
	snap := m.snap
	hasSnap := m.hasSnap
	m.mu.RUnlock()

	centerLat, centerLon := m.myLocation.GetCoordinates()

	var scopeRange float64
	if hasSnap {
		scopeRange = miniScopeRange(snap, centerLat, centerLon)
	} else {
		scopeRange = miniMinScopeNm
	}

	xScale, yScale := miniScales(width, height, scopeRange)

	drawMiniRings(screen, centerX, centerY, xScale, yScale, scopeRange)
	drawMiniCompass(screen, innerX, innerY, centerX, centerY, width, height)
	drawAirports(screen, airports.All(), airportFrame{
		centerX: centerX, centerY: centerY,
		xScale: xScale, yScale: yScale,
		centerLat: centerLat, centerLon: centerLon,
		scopeRange: scopeRange,
	})
	screen.SetContent(centerX, centerY, 'X', nil,
		tcell.StyleDefault.Foreground(tcell.ColorDarkMagenta))

	if !hasSnap || (snap.Latitude == 0 && snap.Longitude == 0) {
		return
	}

	drawMiniPlaneAndTrail(screen, snap, centerX, centerY, xScale, yScale, centerLat, centerLon)
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

	drawTrail(screen, snap.PositionHistory,
		centerX, centerY, xScale, yScale, centerLat, centerLon)

	planeStyle := tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorBlack)
	drawPlane(screen, snap, planeX, planeY, planeStyle, miniDrawToggles)
}

// miniScopeRange picks a scope size that comfortably contains
// the snapshot's current position and trail. Walks the trail and
// the live fix, takes the max distance, pads, rounds up to the
// next 5 nm step, and clamps to a 5 nm floor. Returns the floor
// when the snapshot carries no resolvable position.
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
