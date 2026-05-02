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

func (h *heatMap) decay() {
	now := time.Now()

	h.mu.Lock()
	defer h.mu.Unlock()

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

		px := centerX + int(nmX*xScale)
		py := centerY - int(nmY*yScale)

		style := tcell.StyleDefault.Foreground(heatColor(heat)).Background(tcell.ColorBlack)

		screen.SetContent(px, py, heatRune(heat), nil, style)
	}
}

func heatRune(heat float64) rune {
	switch {
	case heat > 0.66:
		return '▓'
	case heat > 0.33:
		return '▒'
	default:
		return '░'
	}
}

func heatColor(heat float64) tcell.Color {
	switch {
	case heat > 0.66:
		return tcell.ColorAqua
	case heat > 0.33:
		return tcell.ColorTeal
	default:
		return tcell.ColorNavy
	}
}
