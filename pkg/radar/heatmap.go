package radar

import (
	"math"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
)

const (
	heatResolution = 0.5   // nautical miles per grid cell
	heatHalfLife   = 120.0 // seconds for heat to decay to 50%
	heatAddAmount  = 0.5
	heatMinVisible = 0.04

	heatHighThreshold = 0.66
	heatLowThreshold  = 0.33
)

type heatKey struct{ x, y int }

type heatMap struct {
	cells     map[heatKey]float64
	lastDecay time.Time
	mu        sync.Mutex
}

func newHeatMap() *heatMap {
	return &heatMap{
		cells:     make(map[heatKey]float64),
		lastDecay: time.Now(),
	}
}

func (h *heatMap) add(nmX, nmY float64) {
	key := heatKey{
		x: int(math.Round(nmX / heatResolution)),
		y: int(math.Round(nmY / heatResolution)),
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	v := h.cells[key] + heatAddAmount
	if v > 1.0 {
		v = 1.0
	}

	h.cells[key] = v
}

// decay applies an exponential half-life decay to every cell
// based on the wall-clock delta since the previous call.
// time.Now() is captured *inside* the lock so two concurrent
// callers can't both sample now-outside-then-decay-inside and
// double-decay against the same lastDecay; this preserves the
// contract that decay is idempotent on a per-tick basis even if
// a future caller joins Draw on the read path.
func (h *heatMap) decay() {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(h.lastDecay).Seconds()
	h.lastDecay = now

	factor := math.Exp(-elapsed * math.Log(2) / heatHalfLife)

	for k, v := range h.cells {
		v *= factor
		if v < heatMinVisible {
			delete(h.cells, k)
		} else {
			h.cells[k] = v
		}
	}
}

func (h *heatMap) draw(screen tcell.Screen, centerX, centerY int, xScale, yScale float64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for key, heat := range h.cells {
		nmX := float64(key.x) * heatResolution
		nmY := float64(key.y) * heatResolution

		// yScale is halved to match drawScopeRings' /2 and the
		// plane drawing's yMultiplier=2; without it dots sit at 2×
		// the ring's vertical radius and clip off-screen.
		px := centerX + int(nmX*xScale)
		py := centerY - int(nmY*yScale/2)

		style := tcell.StyleDefault.Foreground(heatColor(heat)).Background(tcell.ColorBlack)

		screen.SetContent(px, py, heatRune(heat), nil, style)
	}
}

func heatRune(heat float64) rune {
	switch {
	case heat > heatHighThreshold:
		return '▓'
	case heat > heatLowThreshold:
		return '▒'
	default:
		return '░'
	}
}

func heatColor(heat float64) tcell.Color {
	switch {
	case heat > heatHighThreshold:
		return tcell.ColorAqua
	case heat > heatLowThreshold:
		return tcell.ColorTeal
	default:
		return tcell.ColorNavy
	}
}
