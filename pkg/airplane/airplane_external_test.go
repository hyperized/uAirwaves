package airplane_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplane"
)

const testICAO = "ABCDEF"

func TestNew(t *testing.T) {
	t.Parallel()

	plane := airplane.New(testICAO)

	if got := plane.GetSnapshot().ICAO; got != testICAO {
		t.Errorf("expected %s, got %s", testICAO, got)
	}
}

func TestAirplane_String(t *testing.T) {
	t.Parallel()

	plane := airplane.New(testICAO)
	plane.Update(
		airplane.WithAltitude(30000),
		airplane.WithHeading(180),
		airplane.WithVelocity(450),
		airplane.WithVertRate(-1000),
		airplane.WithLastUpdate(time.Now().Add(-10*time.Second)),
	)

	expected := "30000ft 180o 450kts -1000fpm 10s"
	if plane.String() != expected {
		t.Errorf("expected %s, got %s", expected, plane.String())
	}
}

// TestSnapshotSummaryMatchesString locks the contract that
// Snapshot.Summary returns the same byte sequence as
// Airplane.String when called against the same plane state.
// Snapshot.Summary is the lock-free path the UI list rendering
// uses; if the two ever drift, list rows would silently disagree
// with the live String() format under inspection from tests.
func TestSnapshotSummaryMatchesString(t *testing.T) {
	t.Parallel()

	plane := airplane.New(testICAO)
	plane.Update(
		airplane.WithAltitude(30000),
		airplane.WithHeading(180),
		airplane.WithVelocity(450),
		airplane.WithVertRate(-1000),
		airplane.WithLastUpdate(time.Now().Add(-10*time.Second)),
	)

	// Snapshot.Summary computes its "age" against the snapshot's
	// LastUpdate just like Airplane.String, so as long as both run
	// against the same instant they must agree byte-for-byte.
	if got, want := plane.GetSnapshot().Summary(), plane.String(); got != want {
		t.Errorf("Snapshot.Summary() = %q, want %q (must match Airplane.String)", got, want)
	}
}

func TestAirplane_GetSnapshot(t *testing.T) {
	t.Parallel()

	plane := airplane.New(testICAO)
	now := time.Now()
	plane.Update(
		airplane.WithCallsign("DLH123"),
		airplane.WithAltitude(30000),
		airplane.WithHeading(180),
		airplane.WithVelocity(450),
		airplane.WithVertRate(-1000),
		airplane.WithLatitude(52.5),
		airplane.WithLongitude(13.4),
		airplane.WithSquawk("1234"),
		airplane.WithLastUpdate(now),
	)

	snap := plane.GetSnapshot()

	if snap.ICAO != testICAO ||
		snap.Callsign != "DLH123" ||
		snap.Altitude != 30000 ||
		snap.Heading != 180 ||
		snap.Velocity != 450 ||
		snap.VertRate != -1000 ||
		snap.Latitude != 52.5 ||
		snap.Longitude != 13.4 ||
		!snap.LastUpdate.Equal(now) ||
		snap.Squawk != "1234" ||
		snap.Emergency ||
		snap.MessageCount != 2 { // New() is 1, Update() is 2
		t.Errorf("snapshot does not match airplane data: %+v", snap)
	}
}

// TestGetSnapshotWithoutHistory locks the opt-out path: WithoutHistory
// leaves PositionHistory nil (no allocation, no copy) while every other
// field is still populated. The plane list and stats panel use this to
// skip the O(trail) copy they never read.
func TestGetSnapshotWithoutHistory(t *testing.T) {
	t.Parallel()

	plane := airplane.New(testICAO)
	plane.Update(
		airplane.WithCallsign("DLH123"),
		airplane.WithAltitude(30000),
		airplane.WithPosition(52.5, 13.4),
	)

	// Default snapshot carries the trail.
	if got := len(plane.GetSnapshot().PositionHistory); got != 1 {
		t.Fatalf("default GetSnapshot: history len = %d, want 1", got)
	}

	snap := plane.GetSnapshot(airplane.WithoutHistory())

	if snap.PositionHistory != nil {
		t.Errorf("WithoutHistory: PositionHistory = %v, want nil", snap.PositionHistory)
	}

	if snap.Callsign != "DLH123" || snap.Altitude != 30000 || snap.Latitude != 52.5 {
		t.Errorf("WithoutHistory dropped non-history fields: %+v", snap)
	}
}

func TestGetLastUpdate(t *testing.T) {
	t.Parallel()

	plane := airplane.New(testICAO)
	now := time.Now()
	plane.Update(airplane.WithLastUpdate(now))

	if !plane.GetLastUpdate().Equal(now) {
		t.Error("GetLastUpdate failed")
	}
}

func TestOptions(t *testing.T) {
	t.Parallel()

	testWithCallsign(t)
	testWithSquawk(t)
	testWithLastUpdate(t)
	testWithLatitude(t)
	testWithLongitude(t)
	testWithVelocity(t)
	testWithHeading(t)
}

func testWithCallsign(t *testing.T) {
	t.Helper()
	t.Run("WithCallsign", func(t *testing.T) {
		t.Parallel()

		plane := airplane.New(testICAO)
		plane.Update(airplane.WithCallsign("NEW"))

		if got := plane.GetSnapshot().Callsign; got != "NEW" {
			t.Errorf("expected NEW, got %s", got)
		}

		plane.Update(airplane.WithCallsign(""))

		if got := plane.GetSnapshot().Callsign; got != "NEW" {
			t.Errorf("expected NEW to persist on empty string, got %s", got)
		}
	})
}

func testWithSquawk(t *testing.T) {
	t.Helper()
	t.Run("WithSquawk", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			squawk    string
			emergency bool
		}{
			{"1234", false},
			{"7500", true},
			{"7600", true},
			{"7700", true},
		}

		for _, testCase := range tests {
			t.Run(testCase.squawk, func(t *testing.T) {
				t.Parallel()

				plane := airplane.New(testICAO)
				plane.Update(airplane.WithSquawk(testCase.squawk))

				snap := plane.GetSnapshot()
				if snap.Squawk != testCase.squawk {
					t.Errorf("expected squawk %s, got %s", testCase.squawk, snap.Squawk)
				}

				if snap.Emergency != testCase.emergency {
					t.Errorf("expected emergency %v for squawk %s", testCase.emergency, testCase.squawk)
				}
			})
		}

		plane := airplane.New(testICAO)
		plane.Update(airplane.WithSquawk("7777"))
		plane.Update(airplane.WithSquawk(""))

		if got := plane.GetSnapshot().Squawk; got != "7777" {
			t.Errorf("expected 7777 to persist on empty string, got %s", got)
		}
	})
}

func testWithLastUpdate(t *testing.T) {
	t.Helper()
	t.Run("WithLastUpdate", func(t *testing.T) {
		t.Parallel()

		plane := airplane.New(testICAO)
		now := time.Now()
		plane.Update(airplane.WithLastUpdate(now))

		if !plane.GetLastUpdate().Equal(now) {
			t.Error("WithLastUpdate failed")
		}

		plane.Update(airplane.WithLastUpdate(time.Time{}))

		if !plane.GetLastUpdate().Equal(now) {
			t.Error("WithLastUpdate should not update with zero time")
		}
	})
}

func testWithLatitude(t *testing.T) {
	t.Helper()
	t.Run("WithLatitude", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			input, want float64
		}{
			{50, 50},
			{95, 90},
			{-95, -90},
		}
		for _, testCase := range tests {
			t.Run(fmt.Sprintf("%f", testCase.input), func(t *testing.T) {
				t.Parallel()

				plane := airplane.New(testICAO)
				plane.Update(airplane.WithLatitude(testCase.input))

				if got := plane.GetSnapshot().Latitude; got != testCase.want {
					t.Errorf("WithLatitude(%f) = %f, want %f", testCase.input, got, testCase.want)
				}
			})
		}
	})
}

func testWithLongitude(t *testing.T) {
	t.Helper()
	t.Run("WithLongitude", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			input, want float64
		}{
			{50, 50},
			{185, 180},
			{-185, -180},
		}
		for _, testCase := range tests {
			t.Run(fmt.Sprintf("%f", testCase.input), func(t *testing.T) {
				t.Parallel()

				plane := airplane.New(testICAO)
				plane.Update(airplane.WithLongitude(testCase.input))

				if got := plane.GetSnapshot().Longitude; got != testCase.want {
					t.Errorf("WithLongitude(%f) = %f, want %f", testCase.input, got, testCase.want)
				}
			})
		}
	})
}

func testWithVelocity(t *testing.T) {
	t.Helper()
	t.Run("WithVelocity", func(t *testing.T) {
		t.Parallel()

		plane := airplane.New(testICAO)
		plane.Update(airplane.WithVelocity(100))

		if got := plane.GetSnapshot().Velocity; got != 100 {
			t.Errorf("WithVelocity failed, got %f", got)
		}

		plane.Update(airplane.WithVelocity(-10))

		if got := plane.GetSnapshot().Velocity; got != 100 {
			t.Errorf("WithVelocity should not update with negative value, got %f", got)
		}
	})
}

func testWithHeading(t *testing.T) {
	t.Helper()
	t.Run("WithHeading", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			input, want float64
		}{
			{180, 180},
			{370, 360},
			{-10, 0},
		}
		for _, testCase := range tests {
			t.Run(fmt.Sprintf("%f", testCase.input), func(t *testing.T) {
				t.Parallel()

				plane := airplane.New(testICAO)
				plane.Update(airplane.WithHeading(testCase.input))

				if got := plane.GetSnapshot().Heading; got != testCase.want {
					t.Errorf("WithHeading(%f) = %f, want %f", testCase.input, got, testCase.want)
				}
			})
		}
	})
}

func TestConcurrency(t *testing.T) {
	t.Parallel()

	plane := airplane.New(testICAO)

	var waitGroup sync.WaitGroup
	waitGroup.Add(2)

	go func() {
		defer waitGroup.Done()

		for i := range 1000 {
			plane.Update(airplane.WithAltitude(float64(i)))
		}
	}()
	go func() {
		defer waitGroup.Done()

		for range 1000 {
			_ = plane.GetSnapshot()
		}
	}()

	waitGroup.Wait()
}
