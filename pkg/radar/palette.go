package radar

import (
	"sync/atomic"

	"github.com/gdamore/tcell/v2"
)

// Palette is the set of colours the scope draws with.
//
// Two of them exist because the two places this runs are very different
// terminals. Over SSH from a modern emulator there are 16 million colours and
// the palette can be tuned precisely. The uConsole's own screen is a Linux
// virtual console, which terminfo reports as *eight* colours, and there tcell
// has no choice but to snap each 24-bit value to the nearest of the few it has.
//
// Snapping loses information rather than just fidelity: #ADFF2F and #FFFF00
// both land on yellow, #00FFFF and #87CEFA both land on cyan, and the three
// vertical-rate colours all land on white — so on the device screen a climbing
// aircraft looked exactly like a descending one. Picking from the basic
// colours directly keeps every slot distinct, which matters more than the
// exact hue.
type Palette struct {
	AltBelowFL050 tcell.Color
	AltBelowFL100 tcell.Color
	AltBelowFL200 tcell.Color
	AltBelowFL300 tcell.Color
	AltBelowFL400 tcell.Color
	AltBelowFL500 tcell.Color
	AltBelowFL600 tcell.Color
	AltAboveFL600 tcell.Color

	Climbing   tcell.Color
	Descending tcell.Color
	Level      tcell.Color
}

// TrueColorPalette is the tuned palette, for terminals with 256 colours or
// more. Every entry clears WCAG 4.5:1 against both a black and a slate
// background, and adjacent altitude bands are at least dE 25 apart.
//
//nolint:mnd // every value below is an sRGB literal; the field it fills already names what it colours.
func TrueColorPalette() Palette {
	return Palette{
		AltBelowFL050: tcell.NewHexColor(0xFFFFFF), // white
		AltBelowFL100: tcell.NewHexColor(0xFFFF00), // yellow
		AltBelowFL200: tcell.NewHexColor(0xADFF2F), // green-yellow
		AltBelowFL300: tcell.NewHexColor(0x00FFFF), // aqua
		AltBelowFL400: tcell.NewHexColor(0x87CEFA), // light sky blue
		AltBelowFL500: tcell.NewHexColor(0xE9A6F0), // orchid
		AltBelowFL600: tcell.NewHexColor(0xFFA07A), // light salmon
		AltAboveFL600: tcell.NewHexColor(0xFFFFFF), // white

		Climbing:   tcell.NewHexColor(0x90EE90),
		Descending: tcell.NewHexColor(0xFFA0A0),
		Level:      tcell.NewHexColor(0xADD8E6),
	}
}

// BasicPalette is for terminals with 16 colours or fewer, the uConsole's own
// console among them. These are the named ANSI colours rather than hex, so the
// terminal supplies them from its own palette instead of tcell approximating.
// Seven of the eight base colours carry an altitude band, which is the most
// separation the hardware allows.
func BasicPalette() Palette {
	// The bright half of the sixteen, deliberately. tcell follows the W3C
	// names, so ColorGreen is the dark #008000 (2.0:1 on black) and ColorLime
	// is the bright one (15.8:1); likewise Maroon/Red, Teal/Aqua, Navy/Blue,
	// Purple/Fuchsia and Olive/Yellow. On a console the dark half is barely
	// visible, so every foreground here comes from the bright half.
	return Palette{
		AltBelowFL050: tcell.ColorWhite,   // 15
		AltBelowFL100: tcell.ColorYellow,  // 11
		AltBelowFL200: tcell.ColorLime,    // 10
		AltBelowFL300: tcell.ColorAqua,    // 14
		AltBelowFL400: tcell.ColorBlue,    // 12, the weakest at 4.1:1 — no better blue exists
		AltBelowFL500: tcell.ColorFuchsia, // 13
		AltBelowFL600: tcell.ColorRed,     // 9
		AltAboveFL600: tcell.ColorWhite,

		Climbing:   tcell.ColorLime,
		Descending: tcell.ColorRed,
		Level:      tcell.ColorAqua,
	}
}

// activePalette holds the palette in use. A pointer swap rather than mutable
// fields so the draw loop never reads a half-updated palette.
var activePalette atomic.Pointer[Palette] //nolint:gochecknoglobals // process-wide terminal capability.

// SetPalette installs the palette the scope draws with. Call it once at
// startup, after working out what the terminal can actually show.
func SetPalette(palette Palette) {
	activePalette.Store(&palette)
}

// ActivePalette returns the palette in use, defaulting to the tuned one when
// nothing has been chosen — which keeps every existing caller and test working
// without having to configure anything first.
func ActivePalette() Palette {
	if palette := activePalette.Load(); palette != nil {
		return *palette
	}

	return TrueColorPalette()
}
