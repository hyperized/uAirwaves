package ui_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/hyperized/uAirwaves/internal/ui"
	"github.com/hyperized/uAirwaves/pkg/airplane"
	"github.com/rivo/tview"
)

func TestFormatFlightDetailsCompleteSnapshot(t *testing.T) {
	t.Parallel()

	snap := airplane.Snapshot{
		ICAO:         "484755",
		Callsign:     "KLM1234",
		Altitude:     38000,
		Heading:      90,
		Velocity:     460,
		VertRate:     1200,
		Latitude:     52.3,
		Longitude:    4.78,
		LastUpdate:   time.Now().Add(-5 * time.Second).UTC(),
		Squawk:       "1000",
		MessageCount: 42,
		PositionHistory: []airplane.PositionEntry{
			{Latitude: 52.1, Longitude: 4.5},
			{Latitude: 52.2, Longitude: 4.6},
		},
	}

	out := ui.FormatFlightDetails(snap, 52.0, 4.0)

	mustContain(t, out,
		"KLM1234",
		"484755",
		"38000 ft (FL380)",
		"90° (E)",
		"460 kt (852 km/h)",
		"+1200 fpm",
		"52.30000, 4.78000",
		"Bearing",
		"Messages[::-]   42",
	)

	if strings.Contains(out, "Position history") {
		t.Error("position history list was removed; renderer should no longer emit it")
	}
}

func TestFormatFlightDetailsEmergencyHighlight(t *testing.T) {
	t.Parallel()

	snap := airplane.Snapshot{
		ICAO:       "AAAAAA",
		Squawk:     "7700",
		Emergency:  true,
		LastUpdate: time.Now().UTC(),
	}

	out := ui.FormatFlightDetails(snap, 0, 0)

	if !strings.Contains(out, "[red]7700 (!)"+ui.ResetTag()) {
		t.Errorf("emergency squawk not highlighted in output:\n%s", out)
	}
}

func TestFormatFlightDetailsUnsetFieldsRenderDashes(t *testing.T) {
	t.Parallel()

	snap := airplane.Snapshot{
		ICAO:       "BEEF00",
		Heading:    -1,
		Velocity:   -1,
		LastUpdate: time.Now().UTC(),
	}

	out := ui.FormatFlightDetails(snap, 52, 4)

	for _, want := range []string{
		"Callsign[::-]   " + ui.DimTag() + "—" + ui.ResetTag(),
		"Squawk[::-]     " + ui.DimTag() + "—" + ui.ResetTag(),
		"Heading[::-]    " + ui.DimTag() + "—" + ui.ResetTag(),
		"Velocity[::-]   " + ui.DimTag() + "—" + ui.ResetTag(),
		"Position[::-]   " + ui.DimTag() + "not yet resolved" + ui.ResetTag(),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestFormatFlightDetailsLevelVertRate(t *testing.T) {
	t.Parallel()

	snap := airplane.Snapshot{
		ICAO:       "C0DE01",
		Latitude:   52,
		Longitude:  4,
		LastUpdate: time.Now().UTC(),
	}

	out := ui.FormatFlightDetails(snap, 0, 0)

	if !strings.Contains(out, "Vert rate[::-]  level") {
		t.Errorf("zero vertical rate should render as 'level', got:\n%s", out)
	}
}

func TestFormatFlightDetailsNegativeVertRate(t *testing.T) {
	t.Parallel()

	snap := airplane.Snapshot{
		ICAO:       "C0DE02",
		VertRate:   -1500,
		LastUpdate: time.Now().UTC(),
	}

	out := ui.FormatFlightDetails(snap, 0, 0)

	if !strings.Contains(out, "-1500 fpm") {
		t.Errorf("descent should render with leading sign, got:\n%s", out)
	}
}

func TestFormatFlightDetailsNoGPSFixHidesDistance(t *testing.T) {
	t.Parallel()

	snap := airplane.Snapshot{
		ICAO:       "C0DE04",
		Latitude:   52.3,
		Longitude:  4.78,
		LastUpdate: time.Now().UTC(),
	}

	// (0,0) receiver simulates no GPS fix yet.
	out := ui.FormatFlightDetails(snap, 0, 0)

	if !strings.Contains(out, "Distance[::-]   "+ui.DimTag()+"no GPS fix"+ui.ResetTag()) {
		t.Errorf("expected 'no GPS fix' hint when receiver position unknown, got:\n%s", out)
	}
}

func TestFlightBearingCardinal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		oLat, oLon        float64
		dLat, dLon        float64
		wantBearingApprox float64
	}{
		{"north", 0, 0, 1, 0, 0},
		{"east", 0, 0, 0, 1, 90},
		{"south", 1, 0, 0, 0, 180},
		{"west", 0, 1, 0, 0, 270},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := ui.FlightBearing(testCase.oLat, testCase.oLon, testCase.dLat, testCase.dLon)

			diff := math.Abs(got - testCase.wantBearingApprox)
			if diff > 1 && diff < 359 {
				t.Errorf("FlightBearing %s = %f, want ~%f", testCase.name, got, testCase.wantBearingApprox)
			}
		})
	}
}

func TestUpdateFlightDetailsNilSnapshotRendersHint(t *testing.T) {
	t.Parallel()

	view := tview.NewTextView()

	ui.UpdateFlightDetails(view, nil, 0, 0)

	if got := view.GetText(false); !strings.Contains(got, "Select a flight") {
		t.Errorf("nil snapshot should render hint, got %q", got)
	}
}

func TestUpdateFlightDetailsAppliesSnapshot(t *testing.T) {
	t.Parallel()

	view := tview.NewTextView()

	snap := airplane.Snapshot{
		ICAO:       "484755",
		Callsign:   "KLM1",
		LastUpdate: time.Now().UTC(),
	}

	ui.UpdateFlightDetails(view, &snap, 0, 0)

	if got := view.GetText(false); !strings.Contains(got, "KLM1") || !strings.Contains(got, "484755") {
		t.Errorf("snapshot fields missing from view text:\n%s", got)
	}
}

func mustContain(t *testing.T, body string, needles ...string) {
	t.Helper()

	for _, needle := range needles {
		if !strings.Contains(body, needle) {
			t.Errorf("body missing %q\nbody:\n%s", needle, body)
		}
	}
}
