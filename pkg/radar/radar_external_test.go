package radar_test

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplane"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplanes"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/location"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/radar"
)

func TestNew(t *testing.T) {
	t.Parallel()

	planes := airplanes.New()
	loc := location.New()
	view := radar.New(planes, loc)

	if view == nil {
		t.Fatal("expected New() to return a non-nil View")
	}

	if view.GetScopeRange() != 20 { // default scope min is 20
		t.Errorf("expected default scope range 20, got %f", view.GetScopeRange())
	}

	if !view.GetHeadingIndicatorEnabled() {
		t.Error("expected heading indicator to be enabled by default")
	}

	if !view.GetAutoScopeEnabled() {
		t.Error("expected auto scope to be enabled by default")
	}
}

func TestView_SetScopeRange(t *testing.T) {
	t.Parallel()

	view := radar.New(airplanes.New(), location.New())
	view.SetScopeRange(100)

	if view.GetScopeRange() != 100 {
		t.Errorf("expected scope range 100, got %f", view.GetScopeRange())
	}
}

func TestView_IncrementDecrementScope(t *testing.T) {
	t.Parallel()

	view := radar.New(airplanes.New(), location.New())
	initialRange := view.GetScopeRange()

	view.IncrementScope()

	if view.GetScopeRange() <= initialRange {
		t.Errorf("expected scope range to increase, got %f", view.GetScopeRange())
	}

	increasedRange := view.GetScopeRange()
	view.DecrementScope()

	if view.GetScopeRange() >= increasedRange {
		t.Errorf("expected scope range to decrease, got %f", view.GetScopeRange())
	}
}

func TestView_Toggles(t *testing.T) {
	t.Parallel()

	view := radar.New(airplanes.New(), location.New())

	initialHeading := view.GetHeadingIndicatorEnabled()
	view.ToggleHeadingIndicator()

	if view.GetHeadingIndicatorEnabled() == initialHeading {
		t.Error("ToggleHeadingIndicator() failed to change state")
	}

	initialAutoScope := view.GetAutoScopeEnabled()
	view.ToggleAutoScope()

	if view.GetAutoScopeEnabled() == initialAutoScope {
		t.Error("ToggleAutoScope() failed to change state")
	}
}

func TestView_Draw(t *testing.T) {
	t.Parallel()

	planes := airplanes.New()
	loc := location.New(location.WithLatitude(52.0), location.WithLongitude(13.0))
	view := radar.New(planes, loc)

	// Add a plane within scope
	planes.Ensure("PLANE1")
	plane, _ := planes.Get("PLANE1")
	// Roughly 5nm away
	plane.Update(
		airplane.WithLatitude(52.05),
		airplane.WithLongitude(13.05),
		airplane.WithAltitude(35000),
		airplane.WithVertRate(1000),
		airplane.WithHeading(90),
	)

	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}

	view.SetRect(0, 0, 80, 24)
	view.Draw(screen)

	// Verify something was drawn
	// The center point 'X' should be around (40, 12)
	mainChar, _, _, _ := screen.GetContent(40, 12) //nolint:staticcheck,dogsled
	if mainChar != 'X' {
		t.Errorf("expected center character X, got %c", mainChar)
	}
}

func TestView_Draw_EdgeCases(t *testing.T) {
	t.Parallel()

	planes := airplanes.New()
	loc := location.New(location.WithLatitude(52.0), location.WithLongitude(13.0))
	view := radar.New(planes, loc)

	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}

	view.SetRect(0, 0, 80, 24)

	t.Run("plane outside scope increases range", func(t *testing.T) {
		t.Parallel()

		planes.Ensure("OUTSIDE")
		plane, _ := planes.Get("OUTSIDE")
		// Roughly 50nm away, while default scope is 20
		plane.Update(
			airplane.WithLatitude(53.0),
			airplane.WithLongitude(13.0),
		)

		initialRange := view.GetScopeRange()
		view.Draw(screen)

		if view.GetScopeRange() <= initialRange {
			t.Errorf("expected autoScope to increase range, stayed at %f", view.GetScopeRange())
		}
	})

	t.Run("autoScope disabled does not increase range", func(t *testing.T) {
		t.Parallel()

		view.ToggleAutoScope() // Disable autoScope
		planes.Ensure("FAR")
		plane, _ := planes.Get("FAR")
		plane.Update(
			airplane.WithLatitude(55.0),
			airplane.WithLongitude(13.0),
		)

		initialRange := view.GetScopeRange()
		view.Draw(screen)

		if view.GetScopeRange() != initialRange {
			t.Errorf("expected range to stay %f, got %f", initialRange, view.GetScopeRange())
		}

		view.ToggleAutoScope() // Re-enable for other tests
	})

	t.Run("no planes resets scope", func(t *testing.T) {
		t.Parallel()

		// Create a new view to test reset behavior
		newPlanes := airplanes.New()
		newView := radar.New(newPlanes, loc)
		newView.SetScopeRange(100)
		newView.SetRect(0, 0, 80, 24)

		newView.Draw(screen)

		if newView.GetScopeRange() != 20 { // default min is 20
			t.Errorf("expected scope range to reset to 20, got %f", newView.GetScopeRange())
		}

		newView.ToggleAutoScope()
		newView.SetScopeRange(100)
		newView.Draw(screen)

		if newView.GetScopeRange() != 100 {
			t.Errorf("expected range to stay at 100 with autoScope disabled, got %f", newView.GetScopeRange())
		}
	})

	t.Run("plane without location skipped", func(t *testing.T) {
		t.Parallel()

		planes.Ensure("NOLOC")
		plane, _ := planes.Get("NOLOC")
		plane.Update(airplane.WithLatitude(0), airplane.WithLongitude(0))

		// Should not panic or error
		view.Draw(screen)
	})

	t.Run("plane with callsign", func(t *testing.T) {
		t.Parallel()

		testPlanes := airplanes.New()
		testView := radar.New(testPlanes, loc)
		testView.SetRect(0, 0, 80, 24)

		testPlanes.Ensure("ICAO456")
		plane, _ := testPlanes.Get("ICAO456")
		plane.Update(
			airplane.WithCallsign("TEST456"),
			airplane.WithLatitude(52.01),
			airplane.WithLongitude(13.01),
		)

		testView.Draw(screen)
	})
}
