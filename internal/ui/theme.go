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
