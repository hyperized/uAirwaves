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

	tuned := TrueColorPalette()
	palette := map[string]tcell.Color{
		"altitude below FL050": tuned.AltBelowFL050,
		"altitude below FL100": tuned.AltBelowFL100,
		"altitude below FL200": tuned.AltBelowFL200,
		"altitude below FL300": tuned.AltBelowFL300,
		"altitude below FL400": tuned.AltBelowFL400,
		"altitude below FL500": tuned.AltBelowFL500,
		"altitude below FL600": tuned.AltBelowFL600,
		"altitude above FL600": tuned.AltAboveFL600,
		"climbing":             tuned.Climbing,
		"descending":           tuned.Descending,
		"level":                tuned.Level,
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

	tuned := TrueColorPalette()
	bands := []struct {
		name   string
		colour tcell.Color
	}{
		{"FL050", tuned.AltBelowFL050},
		{"FL100", tuned.AltBelowFL100},
		{"FL200", tuned.AltBelowFL200},
		{"FL300", tuned.AltBelowFL300},
		{"FL400", tuned.AltBelowFL400},
		{"FL500", tuned.AltBelowFL500},
		{"FL600", tuned.AltBelowFL600},
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

// TestBasicPaletteSlotsAreDistinct is the test that would have caught the
// regression that prompted all this. The tuned palette looks right over SSH and
// is then approximated down to the eight colours a Linux console has, where
// several slots land on the same value: the three vertical-rate colours all
// became white, so climbing and descending aircraft were indistinguishable on
// the device screen.
//
// Contrast cannot be asserted here — the terminal owns what "green" looks like
// — but distinctness can, and distinctness is what carries the meaning.
func TestBasicPaletteSlotsAreDistinct(t *testing.T) {
	t.Parallel()

	basic := BasicPalette()

	// FL050 and above-FL600 share white deliberately: traffic above FL600 is
	// rare enough that spending one of eight colours on it costs more than it
	// returns.
	bands := map[string]tcell.Color{
		"FL050": basic.AltBelowFL050,
		"FL100": basic.AltBelowFL100,
		"FL200": basic.AltBelowFL200,
		"FL300": basic.AltBelowFL300,
		"FL400": basic.AltBelowFL400,
		"FL500": basic.AltBelowFL500,
		"FL600": basic.AltBelowFL600,
	}

	assertDistinct(t, "altitude band", bands)

	assertDistinct(t, "vertical rate", map[string]tcell.Color{
		"climbing":   basic.Climbing,
		"descending": basic.Descending,
		"level":      basic.Level,
	})
}

func assertDistinct(t *testing.T, what string, colours map[string]tcell.Color) {
	t.Helper()

	seen := make(map[tcell.Color]string, len(colours))

	for name, colour := range colours {
		if previous, clash := seen[colour]; clash {
			t.Errorf("%s %q and %q are both %v — they cannot be told apart",
				what, previous, name, colour)

			continue
		}

		seen[colour] = name
	}
}

// TestBasicPaletteUsesNamedColors guards the mechanism rather than the values.
// A hex colour here would be approximated by tcell on a low-colour terminal,
// which is the whole problem; a named colour is resolved by the terminal from
// its own palette instead.
func TestBasicPaletteUsesNamedColors(t *testing.T) {
	t.Parallel()

	basic := BasicPalette()
	for name, colour := range map[string]tcell.Color{
		"AltBelowFL050": basic.AltBelowFL050,
		"AltBelowFL100": basic.AltBelowFL100,
		"AltBelowFL200": basic.AltBelowFL200,
		"AltBelowFL300": basic.AltBelowFL300,
		"AltBelowFL400": basic.AltBelowFL400,
		"AltBelowFL500": basic.AltBelowFL500,
		"AltBelowFL600": basic.AltBelowFL600,
		"AltAboveFL600": basic.AltAboveFL600,
		"Climbing":      basic.Climbing,
		"Descending":    basic.Descending,
		"Level":         basic.Level,
	} {
		if colour&tcell.ColorIsRGB != 0 {
			t.Errorf("%s is an RGB colour; the basic palette must use named colours "+
				"so the terminal resolves them instead of tcell approximating", name)
		}

		if colour.Hex() > 0xFFFFFF || colour < 0 {
			t.Errorf("%s is not a valid colour: %v", name, colour)
		}
	}
}

// TestSetPaletteRoundTrip covers the swap itself, including the default: an
// unset palette has to return the tuned one, or every caller that never
// configures anything (all the existing tests, and the check-mode path) would
// draw with zero-valued colours.
//
// Not parallel: mutates process-wide palette state.
func TestSetPaletteRoundTrip(t *testing.T) {
	original := activePalette.Load()
	t.Cleanup(func() { activePalette.Store(original) })

	activePalette.Store(nil)

	if got := ActivePalette(); got != TrueColorPalette() {
		t.Error("an unset palette must default to the tuned one")
	}

	SetPalette(BasicPalette())

	if got := ActivePalette(); got != BasicPalette() {
		t.Error("SetPalette did not take effect")
	}

	SetPalette(TrueColorPalette())

	if got := ActivePalette(); got != TrueColorPalette() {
		t.Error("SetPalette did not switch back")
	}
}

// TestGetFlightLevelColorFollowsPalette proves the lookup actually reads the
// active palette rather than a captured copy — the bug that would silently
// undo all of this.
func TestGetFlightLevelColorFollowsPalette(t *testing.T) {
	original := activePalette.Load()
	t.Cleanup(func() { activePalette.Store(original) })

	SetPalette(BasicPalette())

	if got := getFlightLevelColor(35000); got != BasicPalette().AltBelowFL400 {
		t.Errorf("altitude colour = %v, want the basic palette entry %v",
			got, BasicPalette().AltBelowFL400)
	}

	if got := getVerticalRateColor(600); got != BasicPalette().Climbing {
		t.Errorf("vertical rate colour = %v, want the basic palette entry %v",
			got, BasicPalette().Climbing)
	}
}
