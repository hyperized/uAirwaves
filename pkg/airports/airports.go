// Package airports embeds a curated set of major civil airports
// for radar-scope overlay rendering.
//
// Data source: OurAirports public-domain dataset
// (https://ourairports.com/data/, dedicated CC0). Filtered to a
// hand-picked list of ~310 globally significant hubs — enough
// coverage at sane scope ranges without bloating the binary.
// Re-generate by running the helper at scripts/build_airports.sh
// against a fresh airports.csv (not checked into the repo).
package airports

// Airport is one entry in the static overlay set. Coordinates are
// in decimal degrees (WGS-84) to match the rest of the codebase.
type Airport struct {
	ICAO      string  // 4-letter ICAO identifier, e.g. "EHAM".
	IATA      string  // 3-letter IATA code, may be empty for some entries.
	Name      string  // Operator-published name.
	Country   string  // ISO 3166-1 alpha-2 country code, e.g. "NL".
	Latitude  float64 // WGS-84 latitude in decimal degrees.
	Longitude float64 // WGS-84 longitude in decimal degrees.
}

// All returns the embedded airport set. The returned slice is
// shared, not a copy — callers must treat it as read-only. The
// dataset is static (compile-time literal), so there is no
// initialisation cost and no goroutine-safety concern.
func All() []Airport {
	return airportList
}
