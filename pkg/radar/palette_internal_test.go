package radar

import (
	"math"
	"testing"

	"github.com/gdamore/tcell/v2"
)

// Terminal backgrounds the scope has to stay legible on. Black is the obvious
// one; the slate is what a Nord/OneDark-style theme actually paints, and it is
// the harder case because it lifts the floor.
const (
	backgroundBlack = 0x000000
	backgroundSlate = 0x3B4252

	// WCAG AA for text. The glyphs here are single characters, so the
	// large-text allowance of 3:1 does not apply.
	minContrastRatio = 4.5
)

// relativeLuminance implements the WCAG definition for an sRGB colour.
func relativeLuminance(hex int32) float64 {
	channel := func(shift uint) float64 {
		v := float64((hex>>shift)&0xFF) / 255
		if v <= 0.04045 {
			return v / 12.92
		}

		return math.Pow((v+0.055)/1.055, 2.4)
	}

	return 0.2126*channel(16) + 0.7152*channel(8) + 0.0722*channel(0)
}

func contrastRatio(a, b int32) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}

	return (la + 0.05) / (lb + 0.05)
}

// TestPaletteContrast is the reason the palette is hex rather than tcell's
// named colours. ColorDarkBlue sat at 1.5:1 and ColorPurple at 1.1:1 against a
// dark terminal, which made the FL300-500 bands — most cruise traffic —
// effectively invisible. Anything added to the palette has to clear the bar.
func TestPaletteContrast(t *testing.T) {
	t.Parallel()

	palette := map[string]tcell.Color{
		"altitude below FL050": colorAltBelowFL050,
		"altitude below FL100": colorAltBelowFL100,
		"altitude below FL200": colorAltBelowFL200,
		"altitude below FL300": colorAltBelowFL300,
		"altitude below FL400": colorAltBelowFL400,
		"altitude below FL500": colorAltBelowFL500,
		"altitude below FL600": colorAltBelowFL600,
		"altitude above FL600": colorAltAboveFL600,
		"climbing":             colorClimbing,
		"descending":           colorDescending,
		"level":                colorLevel,
	}

	backgrounds := map[string]int32{"black": backgroundBlack, "slate": backgroundSlate}

	for name, colour := range palette {
		for bgName, bg := range backgrounds {
			if ratio := contrastRatio(colour.Hex(), bg); ratio < minContrastRatio {
				t.Errorf("%s (#%06X) on %s background: contrast %.2f:1, want at least %.1f:1",
					name, colour.Hex(), bgName, ratio, minContrastRatio)
			}
		}
	}
}

// TestPaletteBandsAreDistinguishable guards the other half of readability: a
// palette can clear the contrast bar and still be useless if two neighbouring
// altitude bands look the same. CIE76 dE over 25 is comfortably apart.
func TestPaletteBandsAreDistinguishable(t *testing.T) {
	t.Parallel()

	const minDeltaE = 25.0

	bands := []struct {
		name   string
		colour tcell.Color
	}{
		{"FL050", colorAltBelowFL050},
		{"FL100", colorAltBelowFL100},
		{"FL200", colorAltBelowFL200},
		{"FL300", colorAltBelowFL300},
		{"FL400", colorAltBelowFL400},
		{"FL500", colorAltBelowFL500},
		{"FL600", colorAltBelowFL600},
	}

	for i := range len(bands) - 1 {
		lower, upper := bands[i], bands[i+1]
		if d := deltaE(lower.colour.Hex(), upper.colour.Hex()); d < minDeltaE {
			t.Errorf("bands %s and %s differ by only dE %.1f, want at least %.1f",
				lower.name, upper.name, d, minDeltaE)
		}
	}
}

// deltaE is CIE76 over CIE Lab, which is crude but plenty to catch two bands
// that a person would read as the same colour.
func deltaE(a, b int32) float64 {
	l1, a1, b1 := toLab(a)
	l2, a2, b2 := toLab(b)

	return math.Sqrt((l1-l2)*(l1-l2) + (a1-a2)*(a1-a2) + (b1-b2)*(b1-b2))
}

func toLab(hex int32) (lightness, aAxis, bAxis float64) {
	channel := func(shift uint) float64 {
		v := float64((hex>>shift)&0xFF) / 255
		if v <= 0.04045 {
			return v / 12.92
		}

		return math.Pow((v+0.055)/1.055, 2.4)
	}

	red, green, blue := channel(16), channel(8), channel(0)

	// sRGB to CIE XYZ, D65, then normalised by the reference white.
	x := (0.4124*red + 0.3576*green + 0.1805*blue) / 0.95047
	y := 0.2126*red + 0.7152*green + 0.0722*blue
	z := (0.0193*red + 0.1192*green + 0.9505*blue) / 1.08883

	f := func(t float64) float64 {
		if t > 0.008856 {
			return math.Cbrt(t)
		}

		return 7.787*t + 16.0/116.0
	}

	fx, fy, fz := f(x), f(y), f(z)

	return 116*fy - 16, 500 * (fx - fy), 200 * (fy - fz)
}
