package location_test

import (
	"fmt"
	"testing"

	"github.com/hyperized/uAirwaves/pkg/location"
)

const (
	fix3D     = "3D fix"
	noFixMode = "no fix"
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

	expectedString := "GPS lat 52.500000000, lon 13.400000000 100.5000m 3D fix"
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

	expectedString := "GPS lat 10.000000000, lon 20.000000000 30.0000m 2D fix"
	if got := loc.String(); got != expectedString {
		t.Errorf("expected string %q, got %q", expectedString, got)
	}
}

func TestAltitudeAndMode(t *testing.T) {
	t.Parallel()

	loc := location.New(
		location.WithLatitude(1.0),
		location.WithLongitude(2.0),
		location.WithAltitude(123.5),
		location.WithMode(3),
	)

	if got := loc.Altitude(); got != 123.5 {
		t.Errorf("Altitude() = %f, want 123.5", got)
	}

	if got := loc.Mode(); got != fix3D {
		t.Errorf("Mode() = %q, want %q", got, fix3D)
	}
}

func TestHasFix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode int
		want bool
	}{
		{"unknown", 0, false},
		{"no fix", 1, false},
		{"2D fix", 2, true},
		{"3D fix", 3, true},
		{"out of range", 4, false},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			loc := location.New(location.WithMode(testCase.mode))
			if got := loc.HasFix(); got != testCase.want {
				t.Errorf("HasFix() = %t, want %t", got, testCase.want)
			}
		})
	}
}

func TestFixModes(t *testing.T) {
	t.Parallel()

	// The GPS/no-source form substitutes an empty mode (unknown or
	// out-of-range) with "no fix" so the cold-start header reads as
	// "GPS present, not yet locked" instead of trailing a blank.
	tests := []struct {
		mode int
		want string
	}{
		{0, noFixMode},
		{1, noFixMode},
		{2, "2D fix"},
		{3, fix3D},
		{4, noFixMode}, // Out of range: not in the map, so it renders as "no fix".
	}

	for _, testCase := range tests {
		t.Run(fmt.Sprintf("mode %d", testCase.mode), func(t *testing.T) {
			t.Parallel()

			loc := location.New(location.WithMode(testCase.mode))
			got := loc.String()

			expected := "GPS lat 0.000000000, lon 0.000000000 0.0000m " + testCase.want
			if got != expected {
				t.Errorf("String() = %q, want %q", got, expected)
			}
		})
	}
}

// TestSourceAndConfidenceRadius pins the new provenance accessors:
// the cold-start defaults, and the values set through the matching
// options.
func TestSourceAndConfidenceRadius(t *testing.T) {
	t.Parallel()

	defaults := location.New()
	if got := defaults.Source(); got != location.SourceNone {
		t.Errorf("default Source() = %d, want SourceNone (%d)", got, location.SourceNone)
	}

	if got := defaults.ConfidenceRadiusNm(); got != 0 {
		t.Errorf("default ConfidenceRadiusNm() = %f, want 0", got)
	}

	loc := location.New(
		location.WithSource(location.SourceInferred),
		location.WithConfidenceRadiusNm(18.0),
	)

	if got := loc.Source(); got != location.SourceInferred {
		t.Errorf("Source() = %d, want SourceInferred (%d)", got, location.SourceInferred)
	}

	if got := loc.ConfidenceRadiusNm(); got != 18.0 {
		t.Errorf("ConfidenceRadiusNm() = %f, want 18.0", got)
	}
}

// TestSpreadNm pins the second self-locate figure: the cold-start
// default, and the value set through its option. It sits beside
// the confidence radius rather than replacing it. One is the
// bound the horizon model guarantees, the other how far the
// estimate moves when it is worked out from a fifth of the
// observations.
func TestSpreadNm(t *testing.T) {
	t.Parallel()

	defaults := location.New()
	if got := defaults.SpreadNm(); got != 0 {
		t.Errorf("default SpreadNm() = %f, want 0", got)
	}

	loc := location.New(
		location.WithSource(location.SourceInferred),
		location.WithConfidenceRadiusNm(97.0),
		location.WithSpreadNm(13.0),
	)

	if got := loc.SpreadNm(); got != 13.0 {
		t.Errorf("SpreadNm() = %f, want 13.0", got)
	}

	if got := loc.ConfidenceRadiusNm(); got != 97.0 {
		t.Errorf("ConfidenceRadiusNm() = %f, want 97.0 (the spread must not overwrite it)", got)
	}
}

// TestStringBySource pins the exact header line for every source:
// full-precision GPS, the cold-start unset form, and the reduced-
// precision inferred estimate with and without a confidence radius.
func TestStringBySource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts []location.Option
		want string
	}{
		{
			name: "gps fix",
			opts: []location.Option{
				location.WithSource(location.SourceGPS),
				location.WithLatitude(52.5),
				location.WithLongitude(13.4),
				location.WithAltitude(100.5),
				location.WithMode(3),
			},
			want: "GPS lat 52.500000000, lon 13.400000000 100.5000m 3D fix",
		},
		{
			name: "unset cold start",
			opts: nil,
			want: "GPS lat 0.000000000, lon 0.000000000 0.0000m no fix",
		},
		{
			name: "inferred with radius",
			opts: []location.Option{
				location.WithSource(location.SourceInferred),
				location.WithLatitude(52.31),
				location.WithLongitude(4.92),
				location.WithConfidenceRadiusNm(18.0),
			},
			want: "EST lat 52.31, lon 4.92 (inferred ±18 nm)",
		},
		{
			name: "inferred without radius",
			opts: []location.Option{
				location.WithSource(location.SourceInferred),
				location.WithLatitude(52.31),
				location.WithLongitude(4.92),
			},
			want: "EST lat 52.31, lon 4.92 (inferred)",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			loc := location.New(testCase.opts...)
			if got := loc.String(); got != testCase.want {
				t.Errorf("String() = %q, want %q", got, testCase.want)
			}
		})
	}
}
