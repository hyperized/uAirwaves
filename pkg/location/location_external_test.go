package location_test

import (
	"fmt"
	"testing"

	"lab.hyperized.net/hyperized/uAirwaves/pkg/location"
)

func TestNew(t *testing.T) {
	t.Parallel()

	loc := location.New(
		location.WithLatitude(52.5),
		location.WithLongitude(13.4),
		location.WithAltitude(100.5),
		location.WithMode(3),
	)

	lat, lon := loc.GetCoordinates()
	if lat != 52.5 {
		t.Errorf("expected latitude 52.5, got %f", lat)
	}

	if lon != 13.4 {
		t.Errorf("expected longitude 13.4, got %f", lon)
	}

	expectedString := "lat 52.500000000, lon 13.400000000 100.5000m 3D fix"
	if got := loc.String(); got != expectedString {
		t.Errorf("expected string %q, got %q", expectedString, got)
	}
}

func TestUpdate(t *testing.T) {
	t.Parallel()

	loc := location.New()
	loc.Update(
		location.WithLatitude(10.0),
		location.WithLongitude(20.0),
		location.WithAltitude(30.0),
		location.WithMode(2),
	)

	lat, lon := loc.GetCoordinates()
	if lat != 10.0 {
		t.Errorf("expected latitude 10.0, got %f", lat)
	}

	if lon != 20.0 {
		t.Errorf("expected longitude 20.0, got %f", lon)
	}

	expectedString := "lat 10.000000000, lon 20.000000000 30.0000m 2D fix"
	if got := loc.String(); got != expectedString {
		t.Errorf("expected string %q, got %q", expectedString, got)
	}
}

func TestFixModes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mode int
		want string
	}{
		{0, ""},
		{1, "no fix"},
		{2, "2D fix"},
		{3, "3D fix"},
		{4, ""}, // Out of range should return empty string if map doesn't have it
	}

	for _, testCase := range tests {
		t.Run(fmt.Sprintf("mode %d", testCase.mode), func(t *testing.T) {
			t.Parallel()

			loc := location.New(location.WithMode(testCase.mode))
			got := loc.String()

			expected := "lat 0.000000000, lon 0.000000000 0.0000m " + testCase.want
			if got != expected {
				t.Errorf("String() = %q, want %q", got, expected)
			}
		})
	}
}
