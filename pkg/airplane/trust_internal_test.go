package airplane

import (
	"testing"
	"time"
)

func TestElapsedNeverNegative(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 11, 17, 17, 0, 0, 0, time.UTC)

	for _, testCase := range []struct {
		name           string
		earlier, later time.Time
		want           time.Duration
	}{
		{name: "forward", earlier: base, later: base.Add(3 * time.Second), want: 3 * time.Second},
		{name: "same instant", earlier: base, later: base, want: 0},
		{name: "backwards clamps to zero", earlier: base.Add(time.Second), later: base, want: 0},
		{name: "both zero", want: 0},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := elapsed(testCase.earlier, testCase.later); got != testCase.want {
				t.Errorf("elapsed = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestAltitudeEnvelope(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		gap  time.Duration
		rate float64
		want float64
	}{
		{name: "no time is the slack", gap: 0, rate: maxVerticalRateFpm, want: altitudeSlackFt},
		{name: "ten seconds at the single-reply rate", gap: 10 * time.Second, rate: maxVerticalRateFpm, want: 1300},
		{name: "widest single-reply envelope", gap: altitudeStaleAfter, rate: maxVerticalRateFpm, want: 1800},
		{name: "widest confirmation envelope", gap: altitudeConfirmWindow, rate: confirmVerticalRateFpm, want: 2383.33},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := altitudeEnvelope(testCase.gap, testCase.rate)
			if diff := got - testCase.want; diff > 0.01 || diff < -0.01 {
				t.Errorf("altitudeEnvelope = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestHeldValueConfirmedBy(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 11, 17, 17, 0, 0, 0, time.UTC)
	held := heldValue{heardAt: base, value: "7700", have: true}

	for _, testCase := range []struct {
		name  string
		held  heldValue
		value string
		at    time.Time
		want  bool
	}{
		{name: "nothing held", held: heldValue{}, value: "7700", at: base, want: false},
		{
			name: "same value inside the window", held: held, value: "7700",
			at: base.Add(identityConfirmWindow), want: true,
		},
		{
			name: "same value after the window", held: held, value: "7700",
			at: base.Add(identityConfirmWindow + 1), want: false,
		},
		{name: "different value", held: held, value: "7600", at: base.Add(time.Second), want: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := testCase.held.confirmedBy(testCase.value, testCase.at); got != testCase.want {
				t.Errorf("confirmedBy = %v, want %v", got, testCase.want)
			}
		})
	}
}

// TestCandidateReleasedOnAccept checks the held candidate does not outlive
// the altitude that supersedes it, so a later reply cannot be "confirmed" by
// a value that was already replaced.
func TestCandidateReleasedOnAccept(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 11, 17, 17, 0, 0, 0, time.UTC)
	plane := New("ABCDEF")

	plane.Update(WithReportedAltitude(600, SourceAddressParity, base))

	if !plane.altTrust.haveCandidate {
		t.Fatal("parity altitude was not held")
	}

	plane.Update(WithReportedAltitude(37000, SourceExtendedSquitter, base.Add(time.Second)))

	if plane.altTrust.haveCandidate {
		t.Error("candidate survived an accepted altitude")
	}
}
