package airplane

import (
	"testing"
	"time"
)

// TestWithPositionCapturesCurrentAltitude pins the contract that
// each appended PositionEntry remembers the plane's altitude at
// the moment the fix was sampled. The trail render uses that
// snapshot to colour the dot by flight level flown; if the entry
// carried no altitude, every trail dot would render at FL000.
func TestWithPositionCapturesCurrentAltitude(t *testing.T) {
	t.Parallel()

	plane := New("ALT001")

	plane.Update(WithAltitude(35000), WithPosition(52.0, 13.0))

	history := plane.GetSnapshot().PositionHistory
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history))
	}

	if got := history[0].Altitude; got != 35000 {
		t.Errorf("entry.Altitude = %v, want 35000 (current plane altitude at append time)", got)
	}

	// Backdate to drop past the rate gate, then sample at a new altitude.
	plane.mu.Lock()
	plane.lastPositionTime = time.Now().Add(-2 * positionHistoryInterval)
	plane.mu.Unlock()

	plane.Update(WithAltitude(38000), WithPosition(52.1, 13.1))

	history = plane.GetSnapshot().PositionHistory
	if len(history) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(history))
	}

	if got := history[1].Altitude; got != 38000 {
		t.Errorf("second entry.Altitude = %v, want 38000 (snapshot at append time)", got)
	}

	// First entry must still carry the original altitude — the
	// per-entry altitude is not a back-reference.
	if got := history[0].Altitude; got != 35000 {
		t.Errorf("first entry.Altitude was rewritten to %v; expected 35000 preserved", got)
	}
}

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

// TestWithPositionRetainsEveryRatedFix locks the new contract:
// once the radar gained a "long" trail mode, the airplane keeps
// every appended fix instead of capping at 10. We force every
// append through the rate gate by rewinding lastPositionTime
// between calls; the slice grows without truncation, ordering
// preserved.
func TestWithPositionRetainsEveryRatedFix(t *testing.T) {
	t.Parallel()

	plane := New("ABCDEF")

	const wantEntries = 25

	for index := range wantEntries {
		plane.mu.Lock()
		plane.lastPositionTime = time.Now().Add(-2 * positionHistoryInterval)
		plane.mu.Unlock()

		// Make each entry unique so the slice ordering is checkable.
		plane.Update(WithPosition(float64(index), float64(index)))
	}

	history := plane.GetSnapshot().PositionHistory
	if got := len(history); got != wantEntries {
		t.Fatalf("len(history) = %d, want %d (cap removed)", got, wantEntries)
	}

	// First entry must still be the very first append (no head drop).
	if got := history[0].Latitude; got != 0 {
		t.Errorf("history[0].Latitude = %v, want 0 (oldest fix retained)", got)
	}

	// Last entry is the final iteration index.
	if got := history[len(history)-1].Latitude; got != float64(wantEntries-1) {
		t.Errorf("history[last].Latitude = %v, want %v", got, float64(wantEntries-1))
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
