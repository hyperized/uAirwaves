package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/rivo/tview"
	"github.com/hyperized/uAirwaves/pkg/airplane"
	"github.com/hyperized/uAirwaves/pkg/airplanes"
)

const (
	// flightDetailsAltitudeFL converts altitude in feet to flight
	// level (FL = altitude/100). Mirrors the radar's altitudeMultiplier
	// of 1000 ÷ 10 — the radar drops the trailing zero, this panel
	// keeps the canonical FLxxx format aligned with what controllers
	// announce on frequency.
	flightDetailsAltitudeFL = 100
	// knotsToKmh is the ICAO knots → km/h scalar (1 kt = 1.852 km/h).
	knotsToKmh = 1.852
	// degreesToRadians and radiansToDegrees scale the geometry inside
	// FlightBearing. Hoisted as named constants because mnd flags
	// inline pi math otherwise.
	degreesToRadians = math.Pi / 180.0
	radiansToDegrees = 180.0 / math.Pi
	// fullCircleDegrees is the modulus used to normalise bearings
	// into the [0, 360) range.
	fullCircleDegrees = 360.0
	// compassSectorCount controls cardinal-direction resolution.
	// 16 yields N, NNE, NE, ENE, E, ... — finer than the radar's
	// 8-position spinner so the detail panel reads closer to the
	// controller's spoken bearing.
	compassSectorCount = 16
	compassSectorWidth = fullCircleDegrees / compassSectorCount
	// flightDetailsDashCell is the placeholder rendered for missing
	// or unresolved fields in the details panel. Hoisted as a
	// constant so the four call sites share one literal (and so
	// goconst stops flagging it).
	flightDetailsDashCell = "[gray]—[white]"
	// compassRoundingOffset is the +0.5 added before flooring a
	// scaled bearing so values straddling a sector boundary round
	// to the nearest sector rather than always-down.
	compassRoundingOffset = 0.5
)

// flightDetailsCompass16 indexes 16 compass sectors, each
// 22.5° wide, starting at N and rotating clockwise. Kept as a
// read-only table so cardinalFromHeading is a single lookup
// rather than a switch ladder.
//
//nolint:gochecknoglobals // read-only lookup table, scoped to bearing rendering.
var flightDetailsCompass16 = [compassSectorCount]string{
	"N", "NNE", "NE", "ENE",
	"E", "ESE", "SE", "SSE",
	"S", "SSW", "SW", "WSW",
	"W", "WNW", "NW", "NNW",
}

// UpdateFlightDetails rewrites the flight-details TextView from
// the supplied snapshot. Pure SetText wrapper around
// FormatFlightDetails — keeps the side effect in a one-liner and
// the formatter independently testable. A nil snapshot pointer
// (no selection) renders a hint instead of an empty panel.
func UpdateFlightDetails(
	detailsPanel *tview.TextView, snap *airplane.Snapshot, receiverLat, receiverLon float64,
) {
	if snap == nil {
		detailsPanel.SetText("[gray]Select a flight from the right-column list and press Enter.[white]")

		return
	}

	detailsPanel.SetText(FormatFlightDetails(*snap, receiverLat, receiverLon))
}

// FormatFlightDetails renders every field of an airplane snapshot
// into a multi-line tview-coloured block. Pure string transform —
// no I/O, no clocks beyond time.Since(snap.LastUpdate) which is
// already used by Snapshot.Summary() and is deterministic enough
// for golden-byte testing within a single tick.
func FormatFlightDetails(snap airplane.Snapshot, receiverLat, receiverLon float64) string {
	var builder strings.Builder

	writeIdentityBlock(&builder, snap)
	writeFlightBlock(&builder, snap)
	writePositionBlock(&builder, snap, receiverLat, receiverLon)
	writeMetaBlock(&builder, snap)

	return builder.String()
}

func writeIdentityBlock(builder *strings.Builder, snap airplane.Snapshot) {
	callsign := snap.Callsign
	if callsign == "" {
		callsign = flightDetailsDashCell
	}

	squawk := snap.Squawk
	if squawk == "" {
		squawk = flightDetailsDashCell
	}

	if snap.Emergency {
		squawk = fmt.Sprintf("[red]%s (!)[white]", snap.Squawk)
	}

	fmt.Fprintf(builder, "[::b]Callsign[::-]   %s\n", callsign)
	fmt.Fprintf(builder, "[::b]ICAO[::-]       %s\n", snap.ICAO)
	fmt.Fprintf(builder, "[::b]Squawk[::-]     %s\n", squawk)
}

func writeFlightBlock(builder *strings.Builder, snap airplane.Snapshot) {
	builder.WriteString("\n")
	fmt.Fprintf(builder, "[::b]Altitude[::-]   %s\n", formatAltitude(snap.Altitude))
	fmt.Fprintf(builder, "[::b]Heading[::-]    %s\n", formatHeading(snap.Heading))
	fmt.Fprintf(builder, "[::b]Velocity[::-]   %s\n", formatVelocity(snap.Velocity))
	fmt.Fprintf(builder, "[::b]Vert rate[::-]  %s\n", formatVertRate(snap.VertRate))
}

func writePositionBlock(builder *strings.Builder, snap airplane.Snapshot, receiverLat, receiverLon float64) {
	builder.WriteString("\n")

	if snap.Latitude == 0 && snap.Longitude == 0 {
		builder.WriteString("[::b]Position[::-]   [gray]not yet resolved[white]\n")

		return
	}

	fmt.Fprintf(builder, "[::b]Position[::-]   %.5f, %.5f\n", snap.Latitude, snap.Longitude)

	if receiverLat == 0 && receiverLon == 0 {
		builder.WriteString("[::b]Distance[::-]   [gray]no GPS fix[white]\n")
		builder.WriteString("[::b]Bearing[::-]    [gray]no GPS fix[white]\n")

		return
	}

	// Both (0,0) sentinels are handled above, so HaversineDistance
	// cannot return its MaxFloat64 marker here.
	distance := airplanes.HaversineDistance(receiverLat, receiverLon, snap.Latitude, snap.Longitude)
	fmt.Fprintf(builder, "[::b]Distance[::-]   %.1f nm\n", distance)
	fmt.Fprintf(builder, "[::b]Bearing[::-]    %s\n",
		formatBearing(FlightBearing(receiverLat, receiverLon, snap.Latitude, snap.Longitude)))
}

func writeMetaBlock(builder *strings.Builder, snap airplane.Snapshot) {
	builder.WriteString("\n")
	fmt.Fprintf(builder, "[::b]Last seen[::-]  %.0fs ago ([gray]%s[white])\n",
		time.Since(snap.LastUpdate.UTC()).Seconds(),
		snap.LastUpdate.UTC().Format(time.TimeOnly),
	)
	fmt.Fprintf(builder, "[::b]Messages[::-]   %d\n", snap.MessageCount)
}

// formatAltitude renders altitude in feet plus a flight-level
// suffix. Negative altitudes are clamped at the format string —
// the snapshot can carry an unset altitude of 0, which prints as
// "0 ft (FL000)" rather than a dash so the operator can tell
// "unknown" (no field) from "ground" (zero).
func formatAltitude(altitude float64) string {
	return fmt.Sprintf("%.0f ft (FL%03d)", altitude, int(altitude/flightDetailsAltitudeFL))
}

// formatHeading renders heading in degrees + 16-sector cardinal.
// A heading of -1 (Airplane's default sentinel for "no heading
// yet") renders as a dash so the operator doesn't read "0° (N)"
// for unset planes.
func formatHeading(heading float64) string {
	if heading < 0 {
		return flightDetailsDashCell
	}

	return fmt.Sprintf("%.0f° (%s)", heading, cardinalFromHeading(heading))
}

// formatVelocity renders velocity in kts and km/h. -1 is the
// Airplane default sentinel for "no velocity yet".
func formatVelocity(velocity float64) string {
	if velocity < 0 {
		return flightDetailsDashCell
	}

	return fmt.Sprintf("%.0f kt (%.0f km/h)", velocity, velocity*knotsToKmh)
}

// formatVertRate prefixes the sign for clarity (climbs are
// positive, descents negative). A zero vertical rate renders as
// "level" so a glance at the panel tells climb / descent / level
// at-a-glance.
func formatVertRate(vertRate float64) string {
	switch {
	case vertRate > 0:
		return fmt.Sprintf("+%.0f fpm", vertRate)
	case vertRate < 0:
		return fmt.Sprintf("%.0f fpm", vertRate)
	default:
		return "level"
	}
}

func formatBearing(bearing float64) string {
	return fmt.Sprintf("%.0f° (%s)", bearing, cardinalFromHeading(bearing))
}

// cardinalFromHeading maps a 0–360° heading onto a 16-sector
// compass label. Inputs outside the canonical range are wrapped
// modulo 360.
func cardinalFromHeading(heading float64) string {
	bearing := math.Mod(heading, fullCircleDegrees)
	if bearing < 0 {
		bearing += fullCircleDegrees
	}

	index := int(math.Floor(bearing/compassSectorWidth+compassRoundingOffset)) % compassSectorCount

	return flightDetailsCompass16[index]
}

// FlightBearing returns the great-circle initial bearing from
// (originLat, originLon) to (destLat, destLon) in degrees,
// normalised to [0, 360). Exposed so callers (and tests) can
// reuse the geometry without re-implementing the formula.
func FlightBearing(originLat, originLon, destLat, destLon float64) float64 {
	lat1 := originLat * degreesToRadians
	lat2 := destLat * degreesToRadians
	deltaLon := (destLon - originLon) * degreesToRadians

	y := math.Sin(deltaLon) * math.Cos(lat2)
	x := math.Cos(lat1)*math.Sin(lat2) - math.Sin(lat1)*math.Cos(lat2)*math.Cos(deltaLon)
	bearing := math.Atan2(y, x) * radiansToDegrees

	return math.Mod(bearing+fullCircleDegrees, fullCircleDegrees)
}
