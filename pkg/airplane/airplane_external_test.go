package airplane_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplane"
)

func TestNew(t *testing.T) {
	t.Parallel()

	icao := "ABCDEF"
	plane := airplane.New(icao)

	if plane.GetICAO() != "ABCDEF" {
		t.Errorf("expected ABCDEF, got %s", plane.GetICAO())
	}
}

func TestAirplane_String(t *testing.T) {
	t.Parallel()

	plane := airplane.New("ABCDEF")
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

func TestAirplane_GetSnapshot(t *testing.T) {
	t.Parallel()

	plane := airplane.New("ABCDEF")
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

	if snap.ICAO != "ABCDEF" ||
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

func TestGetters(t *testing.T) {
	t.Parallel()

	plane := airplane.New("ABCDEF")
	now := time.Now()
	plane.Update(
		airplane.WithCallsign("DLH123"),
		airplane.WithLatitude(52.5),
		airplane.WithLongitude(13.4),
		airplane.WithAltitude(30000),
		airplane.WithHeading(180),
		airplane.WithVelocity(450),
		airplane.WithVertRate(-1000),
		airplane.WithSquawk("1234"),
		airplane.WithLastUpdate(now),
	)

	if plane.GetCallsign() != "DLH123" {
		t.Error("GetCallsign failed")
	}

	if plane.GetLatitude() != 52.5 {
		t.Error("GetLatitude failed")
	}

	if plane.GetLongitude() != 13.4 {
		t.Error("GetLongitude failed")
	}

	if plane.GetAltitude() != 30000 {
		t.Error("GetAltitude failed")
	}

	if plane.GetHeading() != 180 {
		t.Error("GetHeading failed")
	}

	if plane.GetVelocity() != 450 {
		t.Error("GetVelocity failed")
	}

	if plane.GetVertRate() != -1000 {
		t.Error("GetVertRate failed")
	}

	if plane.GetSquawk() != "1234" {
		t.Error("GetSquawk failed")
	}

	if plane.GetMessageCount() != 2 {
		t.Errorf("GetMessageCount failed, got %d", plane.GetMessageCount())
	}

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

		plane := airplane.New("ABCDEF")
		plane.Update(airplane.WithCallsign("NEW"))

		if plane.GetCallsign() != "NEW" {
			t.Errorf("expected NEW, got %s", plane.GetCallsign())
		}

		plane.Update(airplane.WithCallsign(""))

		if plane.GetCallsign() != "NEW" {
			t.Errorf("expected NEW to persist on empty string, got %s", plane.GetCallsign())
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

				plane := airplane.New("ABCDEF")
				plane.Update(airplane.WithSquawk(testCase.squawk))

				if plane.GetSquawk() != testCase.squawk {
					t.Errorf("expected squawk %s, got %s", testCase.squawk, plane.GetSquawk())
				}

				if plane.GetSnapshot().Emergency != testCase.emergency {
					t.Errorf("expected emergency %v for squawk %s", testCase.emergency, testCase.squawk)
				}
			})
		}

		plane := airplane.New("ABCDEF")
		plane.Update(airplane.WithSquawk("7777"))
		plane.Update(airplane.WithSquawk(""))

		if plane.GetSquawk() != "7777" {
			t.Error("expected 7777 to persist on empty string")
		}
	})
}

func testWithLastUpdate(t *testing.T) {
	t.Helper()
	t.Run("WithLastUpdate", func(t *testing.T) {
		t.Parallel()

		plane := airplane.New("ABCDEF")
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

				plane := airplane.New("ABCDEF")
				plane.Update(airplane.WithLatitude(testCase.input))

				if plane.GetLatitude() != testCase.want {
					t.Errorf("WithLatitude(%f) = %f, want %f", testCase.input, plane.GetLatitude(), testCase.want)
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

				plane := airplane.New("ABCDEF")
				plane.Update(airplane.WithLongitude(testCase.input))

				if plane.GetLongitude() != testCase.want {
					t.Errorf("WithLongitude(%f) = %f, want %f", testCase.input, plane.GetLongitude(), testCase.want)
				}
			})
		}
	})
}

func testWithVelocity(t *testing.T) {
	t.Helper()
	t.Run("WithVelocity", func(t *testing.T) {
		t.Parallel()

		plane := airplane.New("ABCDEF")
		plane.Update(airplane.WithVelocity(100))

		if plane.GetVelocity() != 100 {
			t.Error("WithVelocity failed")
		}

		plane.Update(airplane.WithVelocity(-10))

		if plane.GetVelocity() != 100 {
			t.Error("WithVelocity should not update with negative value")
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

				plane := airplane.New("ABCDEF")
				plane.Update(airplane.WithHeading(testCase.input))

				if plane.GetHeading() != testCase.want {
					t.Errorf("WithHeading(%f) = %f, want %f", testCase.input, plane.GetHeading(), testCase.want)
				}
			})
		}
	})
}

func TestConcurrency(t *testing.T) {
	t.Parallel()

	plane := airplane.New("ABCDEF")

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
