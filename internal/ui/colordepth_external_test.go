package ui_test

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/hyperized/uAirwaves/internal/ui"
	"github.com/hyperized/uAirwaves/pkg/radar"
)

// TestTerminalColorDepth pins the classification, and in particular the two
// cases that matter on this hardware: a plain Linux console reports eight
// colours, and linux-16color reports sixteen. The ordering of the checks is
// load-bearing — "linux-16color" contains "linux".
//
// Not parallel: t.Setenv.
func TestTerminalColorDepth(t *testing.T) {
	tests := []struct {
		name      string
		term      string
		colorterm string
		want      int
	}{
		{"uConsole console", "linux", "", 8},
		{"console with 16 colours", "linux-16color", "", 16},
		{"ghostty over ssh", "xterm-ghostty", "truecolor", 1 << 24},
		{"24bit spelling", "xterm", "24bit", 1 << 24},
		{"256 colour terminal", "xterm-256color", "", 256},
		{"tmux", "screen-256color", "", 256},
		{"unset", "", "", 8},
		{"dumb", "dumb", "", 8},
		{"serial console", "vt220", "", 8},
		{"unknown but modern", "xterm-kitty", "", 256},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("TERM", testCase.term)
			t.Setenv("COLORTERM", testCase.colorterm)
			t.Setenv("UAIRWAVES_COLORS", "")

			if got := ui.TerminalColorDepth(); got != testCase.want {
				t.Errorf("TERM=%q COLORTERM=%q: got %d colours, want %d",
					testCase.term, testCase.colorterm, got, testCase.want)
			}
		})
	}
}

// TestTerminalColorDepthOverride covers the escape hatch, which exists because
// TERM over SSH describes the terminal you are sitting at, not the screen on
// the device.
func TestTerminalColorDepthOverride(t *testing.T) {
	t.Setenv("TERM", "linux")
	t.Setenv("COLORTERM", "")
	t.Setenv("UAIRWAVES_COLORS", "256")

	if got := ui.TerminalColorDepth(); got != 256 {
		t.Errorf("override ignored: got %d, want 256", got)
	}

	t.Setenv("UAIRWAVES_COLORS", "nonsense")

	if got := ui.TerminalColorDepth(); got != 8 {
		t.Errorf("bad override should fall through to TERM: got %d, want 8", got)
	}
}

// TestSelectThemeForTerminal checks the wiring: the right theme *and* the right
// scope palette, since the scope is a separate package and easy to forget.
//
// Not parallel: mutates process-wide theme state.
func TestSelectThemeForTerminal(t *testing.T) {
	original := ui.ActiveTheme()
	originalPalette := radar.ActivePalette()

	t.Cleanup(func() {
		ui.SetTheme(original)
		radar.SetPalette(originalPalette)
	})

	t.Setenv("COLORTERM", "")
	t.Setenv("UAIRWAVES_COLORS", "")

	t.Setenv("TERM", "linux")

	if depth := ui.SelectThemeForTerminal(); depth != 8 {
		t.Fatalf("linux console depth = %d, want 8", depth)
	}

	if got := ui.ActiveTheme(); got != ui.BasicTheme() {
		t.Error("linux console should select the basic theme")
	}

	if got := radar.ActivePalette(); got != radar.BasicPalette() {
		t.Error("linux console should select the basic scope palette")
	}

	t.Setenv("TERM", "xterm-256color")

	if depth := ui.SelectThemeForTerminal(); depth != 256 {
		t.Fatalf("256 colour depth = %d, want 256", depth)
	}

	if got := ui.ActiveTheme(); got != ui.TrueColorTheme() {
		t.Error("a 256 colour terminal should select the tuned theme")
	}

	if got := radar.ActivePalette(); got != radar.TrueColorPalette() {
		t.Error("a 256 colour terminal should select the tuned scope palette")
	}
}

// TestBasicThemeSlotsAreDistinct is the counterpart to the scope's version.
// Contrast cannot be asserted — the terminal owns what "lime" looks like — but
// distinctness can, and that is what carries the meaning.
func TestBasicThemeSlotsAreDistinct(t *testing.T) {
	t.Parallel()

	basic := ui.BasicTheme()

	assertColorsDistinct(t, "notification background", map[string]tcell.Color{
		caseError:   basic.NotifyErrorBackground,
		caseWarning: basic.NotifyWarningBackground,
		caseInfo:    basic.NotifyInfoBackground,
		caseDebug:   basic.NotifyDebugBackground,
	})

	assertColorsDistinct(t, "header text", map[string]tcell.Color{
		"ok":         basic.HeaderOKText,
		caseWarning:  basic.HeaderWarningText,
		caseCritical: basic.HeaderCriticalText,
	})

	if basic.ConnectedTag == basic.DisconnectedTag {
		t.Error("connected and disconnected dots are the same colour")
	}
}

// TestBasicThemeDotsAreVisibleOnEveryHeader is the reason the basic header uses
// a plain background. On a coloured "battery critical" bar a red disconnected
// dot vanishes into it, and that is exactly when you want to see it.
func TestBasicThemeDotsAreVisibleOnEveryHeader(t *testing.T) {
	t.Parallel()

	basic := ui.BasicTheme()

	backgrounds := map[string]tcell.Color{
		"ok":         basic.HeaderOKBackground,
		caseWarning:  basic.HeaderWarningBackground,
		caseCritical: basic.HeaderCriticalBackground,
	}

	// The tags carry the dot colours; compare them by name against the
	// backgrounds they are drawn on.
	dots := map[string]tcell.Color{
		"connected":    tcell.ColorLime,
		"disconnected": tcell.ColorRed,
	}

	for dotName, dot := range dots {
		for bgName, background := range backgrounds {
			if dot == background {
				t.Errorf("the %s dot is the same colour as the %s header background",
					dotName, bgName)
			}
		}
	}
}

// TestBasicThemeUsesNamedColors guards the mechanism. A hex value here would be
// approximated by tcell on a low-colour terminal, which is the whole problem a
// basic theme exists to avoid.
func TestBasicThemeUsesNamedColors(t *testing.T) {
	t.Parallel()

	basic := ui.BasicTheme()

	colours := map[string]tcell.Color{
		"PanelTitle":               basic.PanelTitle,
		"SecondaryText":            basic.SecondaryText,
		"HeaderOKBackground":       basic.HeaderOKBackground,
		"HeaderOKText":             basic.HeaderOKText,
		"HeaderWarningBackground":  basic.HeaderWarningBackground,
		"HeaderWarningText":        basic.HeaderWarningText,
		"HeaderCriticalBackground": basic.HeaderCriticalBackground,
		"HeaderCriticalText":       basic.HeaderCriticalText,
		"NotifyErrorBackground":    basic.NotifyErrorBackground,
		"NotifyErrorText":          basic.NotifyErrorText,
		"NotifyWarningBackground":  basic.NotifyWarningBackground,
		"NotifyWarningText":        basic.NotifyWarningText,
		"NotifyInfoBackground":     basic.NotifyInfoBackground,
		"NotifyInfoText":           basic.NotifyInfoText,
		"NotifyDebugBackground":    basic.NotifyDebugBackground,
		"NotifyDebugText":          basic.NotifyDebugText,
	}

	for name, colour := range colours {
		if colour&tcell.ColorIsRGB != 0 {
			t.Errorf("%s is an RGB colour; the basic theme must use named colours "+
				"so the terminal resolves them instead of tcell approximating", name)
		}
	}

	for name, tag := range map[string]string{
		"DimTag":          basic.DimTag,
		"ConnectedTag":    basic.ConnectedTag,
		"DisconnectedTag": basic.DisconnectedTag,
	} {
		if len(tag) > 2 && tag[1] == '#' {
			t.Errorf("%s is a hex tag (%s); the basic theme must use colour names", name, tag)
		}
	}
}

func assertColorsDistinct(t *testing.T, what string, colours map[string]tcell.Color) {
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
