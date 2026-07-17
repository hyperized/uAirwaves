package ui

// These tests cover internal implementation details and may need updating during refactors.

import "testing"

// TestCardinalFromHeadingWrapsNegative pins the modulo-normalisation
// arm of cardinalFromHeading. Every public caller feeds it a
// non-negative bearing — formatHeading short-circuits negatives to a
// dash and FlightBearing normalises to [0, 360) — so the wrap-around
// branch is only reachable white-box.
func TestCardinalFromHeadingWrapsNegative(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		heading float64
		want    string
	}{
		{name: "small negative wraps to N", heading: -10, want: "N"},
		{name: "negative quarter wraps to W", heading: -90, want: "W"},
		{name: "beyond negative full circle", heading: -370, want: "N"},
		{name: "zero stays N", heading: 0, want: "N"},
		{name: "due east", heading: 90, want: "E"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := cardinalFromHeading(testCase.heading); got != testCase.want {
				t.Errorf("cardinalFromHeading(%v) = %q, want %q", testCase.heading, got, testCase.want)
			}
		})
	}
}
