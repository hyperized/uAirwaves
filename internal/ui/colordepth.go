package ui

import (
	"os"
	"strconv"
	"strings"

	"github.com/hyperized/uAirwaves/pkg/radar"
)

// Colour depths worth distinguishing. Anything at or below basicColorCeiling
// gets the named-colour theme; above it, the tuned one.
const (
	basicColorCeiling = 16
	paletteColors256  = 256
	trueColorColors   = 1 << 24
	fallbackColors    = 8
)

// TerminalColorDepth reports roughly how many colours the terminal can show.
//
// It reads the same signals tcell does, so the answer agrees with what tcell
// will actually emit. The number does not need to be exact — the only question
// is which side of basicColorCeiling it falls on.
//
// UAIRWAVES_COLORS overrides it, which is useful over SSH into a session whose
// TERM does not describe the screen you are looking at.
func TerminalColorDepth() int {
	if override := os.Getenv("UAIRWAVES_COLORS"); override != "" {
		if parsed, err := strconv.Atoi(override); err == nil && parsed > 0 {
			return parsed
		}
	}

	term := strings.ToLower(os.Getenv("TERM"))
	colorterm := strings.ToLower(os.Getenv("COLORTERM"))

	switch {
	case strings.Contains(colorterm, "truecolor"), strings.Contains(colorterm, "24bit"):
		return trueColorColors
	case strings.Contains(term, "256color"), strings.Contains(term, "direct"):
		return paletteColors256

	// linux-16color and friends. Checked before the bare-terminal cases below
	// so "linux-16color" does not get caught by the "linux" arm.
	case strings.Contains(term, "-16color"):
		return basicColorCeiling

	// The uConsole's own screen lands here: a Linux virtual console, which
	// terminfo describes as eight colours.
	case term == "", term == "dumb", term == "linux", term == "ansi",
		strings.HasPrefix(term, "vt"),
		strings.Contains(term, "-8color"), strings.Contains(term, "-mono"):
		return fallbackColors

	// xterm, screen, tmux and the rest handle 256 even when TERM does not
	// say so. Guessing high here is the safer error: a terminal that really
	// has 16 will approximate, which is where this started, whereas guessing
	// low would throw away the tuned palette on a capable terminal.
	default:
		return paletteColors256
	}
}

// SelectThemeForTerminal picks the theme and scope palette that match what the
// terminal can show, and returns the depth it decided on so the caller can say
// so. Call it before building any panel: the widgets take their colours once,
// at construction.
func SelectThemeForTerminal() int {
	depth := TerminalColorDepth()

	if depth <= basicColorCeiling {
		SetTheme(BasicTheme())
		radar.SetPalette(radar.BasicPalette())

		return depth
	}

	SetTheme(TrueColorTheme())
	radar.SetPalette(radar.TrueColorPalette())

	return depth
}
