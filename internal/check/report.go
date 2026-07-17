// Package check implements a non-TUI diagnostic mode for
// uAirwaves. The orchestrator runs the gps and adsb workers for a
// fixed window, then emits a JSON report and exits non-zero when
// the configured thresholds are not met.
package check

import (
	"math"

	"github.com/hyperized/uAirwaves/pkg/adsb"
	"github.com/hyperized/uAirwaves/pkg/airplane"
	"github.com/hyperized/uAirwaves/pkg/airplanes"
)

// Thresholds gates the exit code of a check run. A run "passes"
// when every threshold below is met by the observed counters.
type Thresholds struct {
	MinFrames uint64 `json:"min_frames"`
	MinPlanes int    `json:"min_planes"`
}

// DefaultThresholds returns the thresholds used when the caller
// does not override them: at least one frame and one tracked
// plane in the window.
func DefaultThresholds() Thresholds {
	return Thresholds{MinFrames: 1, MinPlanes: 1}
}

// GPSReport is the GPS slice of a check report.
type GPSReport struct {
	Fix       bool    `json:"fix"`
	Mode      string  `json:"mode"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	AltitudeM float64 `json:"altitude_m"`
}

// ADSBReport is the ADSB slice of a check report.
type ADSBReport struct {
	Frames           uint64 `json:"frames"`
	Recovered        uint64 `json:"recovered"`
	CallsignsDecoded uint64 `json:"callsigns_decoded"`
	CallsignsApplied uint64 `json:"callsigns_applied"`
	Tracked          int    `json:"tracked"`
	Positioned       int    `json:"positioned"`
}

// SourceReport names the active ingest source and surfaces the
// connection state at end-of-window. Separates "never connected"
// from "connected but produced nothing" in failure analysis.
type SourceReport struct {
	Label     string `json:"label"`
	Connected bool   `json:"connected"`
	BytesIn   uint64 `json:"bytes_in"`
}

// PlaneRef names a plane by callsign (or ICAO when no callsign)
// and a single metric: distance for nearest, altitude for
// highest. Emitted only when the underlying value exists.
type PlaneRef struct {
	Callsign   string  `json:"callsign"`
	DistanceNM float64 `json:"distance_nm,omitempty"`
	AltitudeFt float64 `json:"altitude_ft,omitempty"`
}

// ThresholdsReport mirrors Thresholds with the evaluated outcome
// attached so a consumer can tell which gate failed.
type ThresholdsReport struct {
	MinFrames uint64   `json:"min_frames"`
	MinPlanes int      `json:"min_planes"`
	Failures  []string `json:"failures"`
}

// Report is the JSON document emitted at the end of a check run.
type Report struct {
	DurationSeconds float64          `json:"duration_s"`
	Source          SourceReport     `json:"source"`
	GPS             GPSReport        `json:"gps"`
	ADSB            ADSBReport       `json:"adsb"`
	Nearest         *PlaneRef        `json:"nearest,omitempty"`
	Highest         *PlaneRef        `json:"highest,omitempty"`
	Thresholds      ThresholdsReport `json:"thresholds"`
	OK              bool             `json:"ok"`
}

// Inputs bundles the observation snapshots BuildReport reduces
// into a Report. Hoisted to a struct so the function signature
// stays readable.
type Inputs struct {
	DurationSeconds float64
	Stats           adsb.Stats
	Source          adsb.SourceInfo
	Snapshots       []airplane.Snapshot
	ReceiverLat     float64
	ReceiverLon     float64
	GPSFix          bool
	GPSMode         string
	GPSAltitudeM    float64
	Thresholds      Thresholds
}

// BuildReport reduces a set of observation inputs into the JSON
// Report and evaluates the thresholds. Pure function — no I/O.
func BuildReport(inputs Inputs) Report {
	agg := aggregate(inputs.Snapshots, inputs.ReceiverLat, inputs.ReceiverLon)

	report := Report{
		DurationSeconds: inputs.DurationSeconds,
		Source: SourceReport{
			Label:     inputs.Source.Label,
			Connected: inputs.Source.Connected,
			BytesIn:   inputs.Source.BytesIn,
		},
		GPS: GPSReport{
			Fix:       inputs.GPSFix,
			Mode:      inputs.GPSMode,
			Latitude:  inputs.ReceiverLat,
			Longitude: inputs.ReceiverLon,
			AltitudeM: inputs.GPSAltitudeM,
		},
		ADSB: ADSBReport{
			Frames:           inputs.Stats.TotalFrames,
			Recovered:        inputs.Stats.RecoveredFrames,
			CallsignsDecoded: inputs.Stats.CallsignsDecoded,
			CallsignsApplied: inputs.Stats.CallsignsApplied,
			Tracked:          len(inputs.Snapshots),
			Positioned:       agg.positioned,
		},
		Thresholds: ThresholdsReport{
			MinFrames: inputs.Thresholds.MinFrames,
			MinPlanes: inputs.Thresholds.MinPlanes,
			Failures:  []string{},
		},
	}

	if agg.nearest != nil {
		report.Nearest = agg.nearest
	}

	if agg.highest != nil {
		report.Highest = agg.highest
	}

	report.Thresholds.Failures = evaluateThresholds(inputs.Thresholds, report.ADSB)
	report.OK = len(report.Thresholds.Failures) == 0

	return report
}

type aggregateResult struct {
	positioned int
	nearest    *PlaneRef
	highest    *PlaneRef
}

func aggregate(snapshots []airplane.Snapshot, lat, lon float64) aggregateResult {
	result := aggregateResult{}

	nearestDist := math.MaxFloat64
	highestAlt := 0.0

	for _, snap := range snapshots {
		if snap.Altitude > highestAlt {
			highestAlt = snap.Altitude
			result.highest = &PlaneRef{Callsign: ident(snap), AltitudeFt: snap.Altitude}
		}

		if snap.Latitude == 0 && snap.Longitude == 0 {
			continue
		}

		distance := airplanes.HaversineDistance(lat, lon, snap.Latitude, snap.Longitude)
		if distance == math.MaxFloat64 {
			continue
		}

		result.positioned++

		if distance < nearestDist {
			nearestDist = distance
			result.nearest = &PlaneRef{Callsign: ident(snap), DistanceNM: distance}
		}
	}

	return result
}

func ident(snap airplane.Snapshot) string {
	if snap.Callsign != "" {
		return snap.Callsign
	}

	return snap.ICAO
}

func evaluateThresholds(thresholds Thresholds, observed ADSBReport) []string {
	failures := []string{}

	if observed.Frames < thresholds.MinFrames {
		failures = append(failures, "frames")
	}

	if observed.Tracked < thresholds.MinPlanes {
		failures = append(failures, "planes")
	}

	return failures
}
