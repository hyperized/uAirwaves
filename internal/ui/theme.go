package ui

import (
	"sync/atomic"

	"github.com/gdamore/tcell/v2"
)

// Theme is every colour the panels draw with.
//
// There are two of them because the two places this runs are very different
// terminals. Over SSH from a modern emulator there are 16 million colours and
// the values can be tuned to a contrast ratio. The uConsole's own screen is a
// Linux virtual console, which terminfo reports as eight colours, and there
// tcell has to approximate each 24-bit value to the nearest it has.
//
// Approximation loses meaning, not just fidelity: the tuned dim grey, the panel
// titles and the primary text all collapse onto white, and the green "battery
// fine" header bar lands on grey. Picking named colours directly lets the
// terminal resolve them from its own palette and keeps the slots apart.
//
// pkg/radar carries the same split for the scope.
type Theme struct {
	DimTag          string
	ResetTag        string
	ConnectedTag    string
	DisconnectedTag string

	PanelTitle    tcell.Color
	SecondaryText tcell.Color

	HeaderOKBackground       tcell.Color
	HeaderOKText             tcell.Color
	HeaderWarningBackground  tcell.Color
	HeaderWarningText        tcell.Color
	HeaderCriticalBackground tcell.Color
	HeaderCriticalText       tcell.Color

	NotifyErrorBackground   tcell.Color
	NotifyErrorText         tcell.Color
	NotifyWarningBackground tcell.Color
	NotifyWarningText       tcell.Color
	NotifyInfoBackground    tcell.Color
	NotifyInfoText          tcell.Color
	NotifyDebugBackground   tcell.Color
	NotifyDebugText         tcell.Color
}

// TrueColorTheme is the tuned theme, for terminals with 256 colours or more.
// Every text-on-background pair clears WCAG 4.5:1, and the notification bars
// are at least dE 25 apart so severity stays readable at a glance.
func TrueColorTheme() Theme {
	return Theme{
		DimTag:          "[#b0b8c4]",
		ResetTag:        "[white]",
		ConnectedTag:    "[#9bffc7]",
		DisconnectedTag: "[#ffc4c4]",

		PanelTitle:    tcell.NewHexColor(0x7BD88F),
		SecondaryText: tcell.NewHexColor(0xB0B8C4),

		HeaderOKBackground:       tcell.NewHexColor(0x14532D),
		HeaderOKText:             tcell.NewHexColor(0xE6F4EA),
		HeaderWarningBackground:  tcell.NewHexColor(0x7A4A00),
		HeaderWarningText:        tcell.NewHexColor(0xFFF1D6),
		HeaderCriticalBackground: tcell.NewHexColor(0x8B1A1A),
		HeaderCriticalText:       tcell.NewHexColor(0xFFE5E5),

		NotifyErrorBackground:   tcell.NewHexColor(0x8B1A1A),
		NotifyErrorText:         tcell.NewHexColor(0xFFE5E5),
		NotifyWarningBackground: tcell.NewHexColor(0x7A4A00),
		NotifyWarningText:       tcell.NewHexColor(0xFFF1D6),
		NotifyInfoBackground:    tcell.NewHexColor(0x0F3D7A),
		NotifyInfoText:          tcell.NewHexColor(0xE3F0FF),
		NotifyDebugBackground:   tcell.NewHexColor(0x33383F),
		NotifyDebugText:         tcell.NewHexColor(0xD5DAE1),
	}
}

// BasicTheme is for terminals with 16 colours or fewer, the uConsole console
// among them.
//
// Foregrounds come from the bright half of the sixteen. tcell follows the W3C
// names, so ColorGreen is the dark #008000 at 2.0:1 on black while ColorLime is
// the bright one at 15.8:1 — the dark half is barely visible on a console.
//
// The header keeps a plain background and signals battery state through text
// colour instead of a coloured bar. That is not just taste: the connected and
// disconnected dots sit on the header, and on a red "battery critical" bar a
// red dot disappears entirely. A plain background keeps both dots legible in
// every battery state.
func BasicTheme() Theme {
	return Theme{
		DimTag:          "[aqua]",
		ResetTag:        "[white]",
		ConnectedTag:    "[lime]",
		DisconnectedTag: "[red]",

		PanelTitle:    tcell.ColorLime,
		SecondaryText: tcell.ColorAqua,

		HeaderOKBackground:       tcell.ColorBlack,
		HeaderOKText:             tcell.ColorLime,
		HeaderWarningBackground:  tcell.ColorBlack,
		HeaderWarningText:        tcell.ColorYellow,
		HeaderCriticalBackground: tcell.ColorBlack,
		HeaderCriticalText:       tcell.ColorRed,

		// Notification bars carry no dots, so they can keep coloured
		// backgrounds — which is what makes severity obvious at a glance.
		NotifyErrorBackground:   tcell.ColorMaroon,
		NotifyErrorText:         tcell.ColorWhite,
		NotifyWarningBackground: tcell.ColorOlive,
		NotifyWarningText:       tcell.ColorWhite,
		NotifyInfoBackground:    tcell.ColorNavy,
		NotifyInfoText:          tcell.ColorWhite,
		NotifyDebugBackground:   tcell.ColorBlack,
		NotifyDebugText:         tcell.ColorSilver,
	}
}

// activeTheme holds the theme in use. A pointer swap rather than mutable
// fields, so a render never reads a half-updated theme.
var activeTheme atomic.Pointer[Theme] //nolint:gochecknoglobals // process-wide terminal capability.

// SetTheme installs the theme the panels draw with. Call it once at startup,
// after working out what the terminal can actually show.
func SetTheme(theme Theme) {
	activeTheme.Store(&theme)
}

// ActiveTheme returns the theme in use, defaulting to the tuned one when
// nothing has been chosen — so every caller and test works unconfigured.
func ActiveTheme() Theme {
	if theme := activeTheme.Load(); theme != nil {
		return *theme
	}

	return TrueColorTheme()
}

// Accessors, so the format strings that build panel text stay readable.

// DimTag opens secondary text: units, record values, inline hints.
func DimTag() string { return ActiveTheme().DimTag }

// ResetTag returns to primary text. Paired with DimTag.
func ResetTag() string { return ActiveTheme().ResetTag }

// ConnectedTag opens the connected state dot.
func ConnectedTag() string { return ActiveTheme().ConnectedTag }

// DisconnectedTag opens the disconnected state dot.
func DisconnectedTag() string { return ActiveTheme().DisconnectedTag }

// ColorPanelTitle labels the bordered panels.
func ColorPanelTitle() tcell.Color { return ActiveTheme().PanelTitle }

// ColorSecondaryText is the plane list's second line: distance, altitude,
// heading, rate. tview defaults it to TertiaryTextColor, which is #008000.
func ColorSecondaryText() tcell.Color { return ActiveTheme().SecondaryText }
