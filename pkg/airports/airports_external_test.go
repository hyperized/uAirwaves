package airports_test

import (
	"testing"

	"lab.hyperized.net/hyperized/uAirwaves/pkg/airports"
)

// TestAllNonEmpty pins the contract that the embedded dataset is
// not empty. A broken codegen step (e.g. a generate.sh regression
// that filters out everything) would otherwise ship a silent
// no-op overlay.
func TestAllNonEmpty(t *testing.T) {
	t.Parallel()

	if got := len(airports.All()); got == 0 {
		t.Fatal("airports.All() returned an empty slice; data.go did not seed any entries")
	}
}

// TestAllFieldsLookSane catches generator bugs that would
// otherwise smuggle malformed entries through: blank ICAO, name,
// or coordinates anchored at (0, 0) — none of which are valid
// for a major commercial airport. Even a single bad row would
// produce a misplaced overlay marker, hard to spot at a glance.
func TestAllFieldsLookSane(t *testing.T) {
	t.Parallel()

	for _, airport := range airports.All() {
		if airport.ICAO == "" {
			t.Errorf("entry has empty ICAO: %+v", airport)
		}

		if airport.Name == "" {
			t.Errorf("%s has empty Name", airport.ICAO)
		}

		if airport.Latitude == 0 && airport.Longitude == 0 {
			t.Errorf("%s sits at (0, 0) — almost certainly a generator error", airport.ICAO)
		}

		if airport.Latitude < -90 || airport.Latitude > 90 {
			t.Errorf("%s has out-of-range Latitude %v", airport.ICAO, airport.Latitude)
		}

		if airport.Longitude < -180 || airport.Longitude > 180 {
			t.Errorf("%s has out-of-range Longitude %v", airport.ICAO, airport.Longitude)
		}
	}
}

// TestKnownAnchorAirports cross-checks a handful of well-known
// hubs against approximate ground-truth coordinates so a future
// CSV swap or generator regression that re-maps codes shows up
// as a clear failure.
func TestKnownAnchorAirports(t *testing.T) {
	t.Parallel()

	tests := []struct {
		icao        string
		wantLatMin  float64
		wantLatMax  float64
		wantLonMin  float64
		wantLonMax  float64
		wantCountry string
	}{
		{"EHAM", 52.2, 52.4, 4.6, 4.9, "NL"},     // Amsterdam Schiphol
		{"KJFK", 40.5, 40.7, -73.9, -73.7, "US"}, // JFK
		{"EGLL", 51.4, 51.6, -0.6, -0.3, "GB"},   // Heathrow
		{"RJTT", 35.5, 35.7, 139.6, 139.9, "JP"}, // Tokyo Haneda
	}

	byICAO := map[string]airports.Airport{}
	for _, airport := range airports.All() {
		byICAO[airport.ICAO] = airport
	}

	for _, testCase := range tests {
		t.Run(testCase.icao, func(t *testing.T) {
			t.Parallel()

			airport, ok := byICAO[testCase.icao]
			if !ok {
				t.Fatalf("%s missing from dataset", testCase.icao)
			}

			if airport.Country != testCase.wantCountry {
				t.Errorf("%s country = %q, want %q", testCase.icao, airport.Country, testCase.wantCountry)
			}

			if airport.Latitude < testCase.wantLatMin || airport.Latitude > testCase.wantLatMax {
				t.Errorf("%s lat = %v, want in [%v, %v]",
					testCase.icao, airport.Latitude, testCase.wantLatMin, testCase.wantLatMax)
			}

			if airport.Longitude < testCase.wantLonMin || airport.Longitude > testCase.wantLonMax {
				t.Errorf("%s lon = %v, want in [%v, %v]",
					testCase.icao, airport.Longitude, testCase.wantLonMin, testCase.wantLonMax)
			}
		})
	}
}
