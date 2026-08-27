package ui

import "github.com/gdamore/tcell/v2"

// Colour tags shared by the panels.
//
// tview's [gray] is #808080, which lands at 2.7:1 against a dark terminal
// background — under the 4.5:1 needed to actually read it. Every panel used it
// for secondary text, so callsigns, records and hints were all sitting below
// the legibility floor at once.
//
// DimTag is a touch blue so it belongs with a slate background instead of
// reading as an unrelated neutral, and clears 5.0:1 on slate, 10.5:1 on black.
// pkg/radar carries the same reasoning for the scope palette.
const (
	// DimTag opens secondary text: units, record values, inline hints.
	DimTag = "[#b0b8c4]"

	// ResetTag returns to primary text. Paired with DimTag.
	ResetTag = "[white]"
)

// Panel chrome. tview defaults the list secondary text and these titles to
// ColorGreen, which is #008000 and lands at 2.0:1 on a dark background — the
// same legibility problem the scope palette had.
var (
	// ColorPanelTitle labels the bordered panels. Green in keeping with the
	// original, lifted to 5.8:1.
	ColorPanelTitle = tcell.NewHexColor(0x7BD88F)

	// ColorSecondaryText is the plane list's second line: distance, altitude,
	// heading, rate. Matches DimTag so the panels agree with each other.
	ColorSecondaryText = tcell.NewHexColor(0xB0B8C4)
)

// Header bar.
//
// This was the least readable thing in the interface: a DarkGreen bar with
// ColorBlack text is 2.8:1, and because a terminal paints "black" as whatever
// its theme says — usually a dark slate, not #000000 — what actually reached
// the screen was closer to 1.4:1. The comment on FormatSourceText had the
// polarity backwards and concluded white was the unreadable one.
//
// Dark bars with light text instead: the state still reads as green, amber or
// red at a glance, nothing glares on a handheld at night, and every pair has
// contrast to spare.
var (
	ColorHeaderOKBackground       = tcell.NewHexColor(0x14532D)
	ColorHeaderOKText             = tcell.NewHexColor(0xE6F4EA)
	ColorHeaderWarningBackground  = tcell.NewHexColor(0x7A4A00)
	ColorHeaderWarningText        = tcell.NewHexColor(0xFFF1D6)
	ColorHeaderCriticalBackground = tcell.NewHexColor(0x8B1A1A)
	ColorHeaderCriticalText       = tcell.NewHexColor(0xFFE5E5)
)

// State dots sit on whichever header bar is current, so they are picked to
// clear 4.5:1 against all three backgrounds rather than just the green one.
const (
	// ConnectedTag opens the connected state dot.
	ConnectedTag = "[#9bffc7]"

	// DisconnectedTag opens the disconnected state dot.
	DisconnectedTag = "[#ffc4c4]"
)
