package airplane

import (
	"testing"
	"time"
)

// TestWithPositionAppendsAndRateLimits exercises WithPosition's
// three observable behaviours:
//
//   - first call always appends, regardless of lastPositionTime
//     (zero value is "long ago" by definition);
//   - back-to-back calls inside positionHistoryInterval update
//     lat/lon but do NOT extend the history slice (the rate limit
//     keeps near-identical entries from filling the trail);
//   - once the slice fills past maxPositionHistory, the oldest
//     entries are dropped on the next append.
//
// We manipulate lastPositionTime directly via the package-private
// field — that is exactly what this internal-only test file is for.
func TestWithPositionAppendsAndRateLimits(t *testing.T) {
	t.Parallel()

	plane := New("ABCDEF")

	// First fix: history empty, lastPositionTime zero — must append.
	plane.Update(WithPosition(52.0, 13.0))

	if got := len(plane.GetSnapshot().PositionHistory); got != 1 {
		t.Fatalf("after first WithPosition: history len = %d, want 1", got)
	}

	// Second fix inside the rate window: lat/lon update, no append.
	plane.Update(WithPosition(52.1, 13.1))

	snap := plane.GetSnapshot()

	if got := len(snap.PositionHistory); got != 1 {
		t.Errorf("rate-limited WithPosition: history len = %d, want 1", got)
	}

	if snap.Latitude != 52.1 || snap.Longitude != 13.1 {
		t.Errorf("rate-limited WithPosition should still update lat/lon, got (%v, %v)",
			snap.Latitude, snap.Longitude)
	}

	// Backdate the gate so the next call appends again.
	plane.mu.Lock()
	plane.lastPositionTime = time.Now().Add(-2 * positionHistoryInterval)
	plane.mu.Unlock()

	plane.Update(WithPosition(52.2, 13.2))

	if got := len(plane.GetSnapshot().PositionHistory); got != 2 {
		t.Errorf("post-rate-window append: history len = %d, want 2", got)
	}
}

// TestWithPositionTruncatesToMaxHistory exercises the cap branch:
// once the slice grows past maxPositionHistory, the oldest entry
// is dropped. We force every append through the rate gate by
// rewinding lastPositionTime between calls.
func TestWithPositionTruncatesToMaxHistory(t *testing.T) {
	t.Parallel()

	plane := New("ABCDEF")

	for index := range maxPositionHistory + 3 {
		plane.mu.Lock()
		plane.lastPositionTime = time.Now().Add(-2 * positionHistoryInterval)
		plane.mu.Unlock()

		// Make each entry unique so the slice ordering is checkable.
		plane.Update(WithPosition(float64(index), float64(index)))
	}

	history := plane.GetSnapshot().PositionHistory
	if got := len(history); got != maxPositionHistory {
		t.Fatalf("len(history) = %d, want %d (truncation cap)", got, maxPositionHistory)
	}

	// The oldest three entries must have been dropped: the first
	// remaining entry is index 3, the last is index maxPositionHistory+2.
	const dropped = 3

	if got := history[0].Latitude; got != float64(dropped) {
		t.Errorf("history[0].Latitude = %v, want %v (oldest dropped)", got, float64(dropped))
	}

	if got := history[len(history)-1].Latitude; got != float64(maxPositionHistory+2) {
		t.Errorf("history[last].Latitude = %v, want %v", got, float64(maxPositionHistory+2))
	}
}

// TestWithPositionClampsExtremes locks the clamp behaviour for
// out-of-range latitudes and longitudes. Inputs beyond ±90 /
// ±180 must snap to the bound before going into the snapshot or
// the history slice — a corrupt CPR decode shouldn't be able to
// move the airplane to a non-Earth coordinate.
func TestWithPositionClampsExtremes(t *testing.T) {
	t.Parallel()

	plane := New("ABCDEF")
	plane.Update(WithPosition(120.0, 200.0))

	snap := plane.GetSnapshot()
	if snap.Latitude != maxLatitude || snap.Longitude != maxLongitude {
		t.Errorf("over-max clamp: lat=%v lon=%v, want (%v, %v)",
			snap.Latitude, snap.Longitude, float64(maxLatitude), float64(maxLongitude))
	}

	plane2 := New("ABCDEF")
	plane2.Update(WithPosition(-120.0, -200.0))

	snap2 := plane2.GetSnapshot()
	if snap2.Latitude != minLatitude || snap2.Longitude != minLongitude {
		t.Errorf("under-min clamp: lat=%v lon=%v, want (%v, %v)",
			snap2.Latitude, snap2.Longitude, float64(minLatitude), float64(minLongitude))
	}
}

// TestWithSquawkEmergencyTransitions locks in the C1 fix: the
// emergency flag must be recomputed on every non-empty squawk
// update so a transition out of an emergency code (e.g. 7700 to
// 1200) clears the flag instead of staying stuck on true.
func TestWithSquawkEmergencyTransitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		sequence  []string
		wantFinal bool
	}{
		{name: "stays false for non-emergency", sequence: []string{"1200"}, wantFinal: false},
		{name: "trips true on hijack", sequence: []string{"7500"}, wantFinal: true},
		{name: "trips true on radio failure", sequence: []string{"7600"}, wantFinal: true},
		{name: "trips true on general emergency", sequence: []string{"7700"}, wantFinal: true},
		{name: "clears 7500 -> 1200", sequence: []string{"7500", "1200"}, wantFinal: false},
		{name: "clears 7600 -> 1200", sequence: []string{"7600", "1200"}, wantFinal: false},
		{name: "clears 7700 -> 1200", sequence: []string{"7700", "1200"}, wantFinal: false},
		{name: "clears 7700 -> 7000", sequence: []string{"7700", "7000"}, wantFinal: false},
		{name: "re-trips 1200 -> 7700", sequence: []string{"1200", "7700"}, wantFinal: true},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			plane := New("ABCDEF")
			for _, squawk := range testCase.sequence {
				plane.Update(WithSquawk(squawk))
			}

			if got := plane.GetSnapshot().Emergency; got != testCase.wantFinal {
				t.Errorf("after %v: emergency = %v, want %v", testCase.sequence, got, testCase.wantFinal)
			}
		})
	}
}
