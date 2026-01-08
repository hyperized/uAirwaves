package radar

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestGetVerticalRateColor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		vertRate float64
		want     tcell.Color
	}{
		{600, tcell.ColorGreen},
		{-600, tcell.ColorRed},
		{0, tcell.ColorLightBlue},
		{500, tcell.ColorLightBlue},
		{-500, tcell.ColorLightBlue},
	}

	for _, testCase := range tests {
		t.Run(testCase.want.String(), func(t *testing.T) {
			t.Parallel()

			if got := getVerticalRateColor(testCase.vertRate); got != testCase.want {
				t.Errorf("getVerticalRateColor(%f) = %v, want %v", testCase.vertRate, got, testCase.want)
			}
		})
	}
}

func TestGetVertRateSymbol(t *testing.T) {
	t.Parallel()

	tests := []struct {
		vertRate float64
		want     string
	}{
		{600, "^"},
		{200, "+"},
		{-600, "v"},
		{-200, "-"},
		{0, "="},
		{100, "="},
		{-100, "="},
	}

	for _, testCase := range tests {
		t.Run(testCase.want, func(t *testing.T) {
			t.Parallel()

			if got := getVertRateSymbol(testCase.vertRate); got != testCase.want {
				t.Errorf("getVertRateSymbol(%f) = %s, want %s", testCase.vertRate, got, testCase.want)
			}
		})
	}
}

func TestGetFlightLevelColor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		altitude float64
		want     tcell.Color
	}{
		{400, tcell.ColorWhite},
		{5000, tcell.ColorYellow},
		{15000, tcell.ColorGreen},
		{25000, tcell.ColorLightBlue},
		{35000, tcell.ColorDarkBlue},
		{45000, tcell.ColorPurple},
		{55000, tcell.ColorRed},
		{65000, tcell.ColorWhite},
	}

	for _, testCase := range tests {
		t.Run(testCase.want.String(), func(t *testing.T) {
			t.Parallel()

			if got := getFlightLevelColor(testCase.altitude); got != testCase.want {
				t.Errorf("getFlightLevelColor(%f) = %v, want %v", testCase.altitude, got, testCase.want)
			}
		})
	}
}
