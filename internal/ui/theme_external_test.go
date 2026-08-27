package ui_test

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/hyperized/uAirwaves/internal/ui"
)

// Panel backgrounds the dim text has to survive. A terminal paints its own
// "black", and on the common dark themes that is a slate rather than #000000,
// which lifts the floor and makes low-contrast text worse, not better.
const (
	themeBackgroundBlack = 0x000000
	themeBackgroundSlate = 0x3B4252

	themeMinContrast = 4.5
)

func themeLuminance(hex int32) float64 {
	channel := func(shift uint) float64 {
		v := float64((hex>>shift)&0xFF) / 255
		if v <= 0.04045 {
			return v / 12.92
		}

		return math.Pow((v+0.055)/1.055, 2.4)
	}

	return 0.2126*channel(16) + 0.7152*channel(8) + 0.0722*channel(0)
}

func themeContrast(a, b int32) float64 {
	la, lb := themeLuminance(a), themeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}

	return (la + 0.05) / (lb + 0.05)
}

// hexFromTag pulls the colour out of a tview tag like "[#b0b8c4]".
func hexFromTag(t *testing.T, tag string) int32 {
	t.Helper()

	trimmed := strings.TrimSuffix(strings.TrimPrefix(tag, "[#"), "]")

	value, err := strconv.ParseInt(trimmed, 16, 32)
	if err != nil {
		t.Fatalf("tag %q is not a [#rrggbb] colour: %v", tag, err)
	}

	return int32(value)
}

// TestHeaderContrast is why the header is hex rather than tcell names. It ran
// as ColorDarkGreen with ColorBlack text, which is 2.8:1 on paper — and closer
// to 1.4:1 in practice, because the terminal substitutes its own dark slate for
// "black". It was the least legible element in the interface.
func TestHeaderContrast(t *testing.T) {
	t.Parallel()

	pairs := []struct {
		name             string
		background, text tcell.Color
	}{
		{"ok", ui.ColorHeaderOKBackground, ui.ColorHeaderOKText},
		{"warning", ui.ColorHeaderWarningBackground, ui.ColorHeaderWarningText},
		{"critical", ui.ColorHeaderCriticalBackground, ui.ColorHeaderCriticalText},
	}

	for _, pair := range pairs {
		ratio := themeContrast(pair.background.Hex(), pair.text.Hex())
		if ratio < themeMinContrast {
			t.Errorf("header %s: #%06X on #%06X is %.2f:1, want at least %.1f:1",
				pair.name, pair.text.Hex(), pair.background.Hex(), ratio, themeMinContrast)
		}
	}
}

// TestStateDotsReadableOnEveryHeader covers the case that is easy to miss: the
// dots sit on a bar whose colour changes with battery state, so a dot that
// reads well on the green bar can vanish on the red one.
func TestStateDotsReadableOnEveryHeader(t *testing.T) {
	t.Parallel()

	dots := map[string]int32{
		"connected":    hexFromTag(t, ui.ConnectedTag),
		"disconnected": hexFromTag(t, ui.DisconnectedTag),
	}

	backgrounds := map[string]tcell.Color{
		"ok":       ui.ColorHeaderOKBackground,
		"warning":  ui.ColorHeaderWarningBackground,
		"critical": ui.ColorHeaderCriticalBackground,
	}

	for dotName, dot := range dots {
		for bgName, background := range backgrounds {
			ratio := themeContrast(dot, background.Hex())
			if ratio < themeMinContrast {
				t.Errorf("%s dot on the %s header: %.2f:1, want at least %.1f:1",
					dotName, bgName, ratio, themeMinContrast)
			}
		}
	}
}

// TestPanelTextContrast covers the panel bodies rather than the header: the
// dim secondary text and the list's second line, both of which inherited
// tview defaults that sat below the floor.
func TestPanelTextContrast(t *testing.T) {
	t.Parallel()

	colours := map[string]int32{
		"DimTag":             hexFromTag(t, ui.DimTag),
		"ColorSecondaryText": ui.ColorSecondaryText.Hex(),
		"ColorPanelTitle":    ui.ColorPanelTitle.Hex(),
	}

	backgrounds := map[string]int32{"black": themeBackgroundBlack, "slate": themeBackgroundSlate}

	for name, colour := range colours {
		for bgName, background := range backgrounds {
			ratio := themeContrast(colour, background)
			if ratio < themeMinContrast {
				t.Errorf("%s (#%06X) on %s: %.2f:1, want at least %.1f:1",
					name, colour, bgName, ratio, themeMinContrast)
			}
		}
	}
}
