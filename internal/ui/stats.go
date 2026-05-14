package ui

import (
	"fmt"
	"math"
	"time"

	"lab.hyperized.net/hyperized/uAirwaves/pkg/adsb"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplane"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplanes"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/location"
)

// StatsTracker keeps the prior tick's frame counters so the
// stats panel can derive a per-second rate without each call to
// adsb.Stats() mutating shared state.
type StatsTracker struct {
	lastTotal     uint64
	lastRecovered uint64
	lastSampledAt time.Time
}

// NewStatsTracker returns a tracker seeded with the construction
// time. The first Sample call after construction has a tiny
// window (now - construct time), so the rate is approximate
// until the second tick.
func NewStatsTracker() *StatsTracker {
	return &StatsTracker{lastSampledAt: time.Now()}
}

// Sample returns the per-second frame and recovery rate since
// the previous call, alongside the cumulative totals. The first
// call after NewStatsTracker has a tiny window (now - construct
// time), so the rate is approximate until the second tick.
//
//nolint:nonamedreturns // (frames/s, recovered/s) reads clearer named at this signature.
func (t *StatsTracker) Sample(stats adsb.Stats) (framesPerSec, recoveredPerSec float64) {
	now := time.Now()
	elapsed := now.Sub(t.lastSampledAt).Seconds()

	if elapsed > 0 {
		framesPerSec = float64(stats.TotalFrames-t.lastTotal) / elapsed
		recoveredPerSec = float64(stats.RecoveredFrames-t.lastRecovered) / elapsed
	}

	t.lastTotal = stats.TotalFrames
	t.lastRecovered = stats.RecoveredFrames
	t.lastSampledAt = now

	return framesPerSec, recoveredPerSec
}

// DisplayIdent picks the callsign when present, falling back to
// the bare ICAO so the stats panel always names something.
func DisplayIdent(snap airplane.Snapshot) string {
	if snap.Callsign != "" {
		return snap.Callsign
	}

	return snap.ICAO
}

// StatsRender packages the aggregated values FormatStatsText
// renders. Hoisted to a struct so the formatter signature stays
// readable as fields accumulate.
type StatsRender struct {
	Tracked, Positioned                int
	NearestDist, FarthestDist          float64
	NearestCallsign, FarthestCallsign  string
	HighestAlt                         float64
	HighestCallsign                    string
	FramesPerSec, RecoveredPerSec      float64
	TotalFrames, RecoveredFrames       uint64
	CallsignsDecoded, CallsignsApplied uint64
}

// FormatStatsText renders a StatsRender into the multi-line
// tview-coloured layout the stats panel displays. Pure string
// transform — no side effects, no I/O — so a golden-byte test
// pins the output and catches any future format drift.
func FormatStatsText(render StatsRender) string {
	nearest := "—"
	if render.Positioned > 0 {
		nearest = fmt.Sprintf("%.1f nm  [gray]%s[white]", render.NearestDist, render.NearestCallsign)
	}

	farthest := "—"
	if render.Positioned > 0 {
		farthest = fmt.Sprintf("%.1f nm  [gray]%s[white]", render.FarthestDist, render.FarthestCallsign)
	}

	highest := "—"
	if render.HighestAlt > 0 {
		highest = fmt.Sprintf("%.0f ft  [gray]%s[white]", render.HighestAlt, render.HighestCallsign)
	}

	return fmt.Sprintf(
		"[::b]Tracked[::-]    %d  ([gray]%d positioned[white])\n"+
			"[::b]Nearest[::-]    %s\n"+
			"[::b]Farthest[::-]   %s\n"+
			"[::b]Highest[::-]    %s\n"+
			"[::b]Frames/s[::-]   %.1f  ([gray]rec %.2f[white])\n"+
			"[::b]Total[::-]      %d  ([gray]IDs %d/%d[white])",
		render.Tracked, render.Positioned,
		nearest,
		farthest,
		highest,
		render.FramesPerSec, render.RecoveredPerSec,
		render.TotalFrames, render.CallsignsApplied, render.CallsignsDecoded,
	)
}

// AggregateStats walks the plane list once and returns a
// StatsRender suitable for FormatStatsText. Aircraft without
// resolved positions (lat/lon = 0) are excluded from the
// distance and altitude reductions; their absence is normal in
// the first few seconds after a position-less squitter.
//
// Separating aggregation from rendering keeps both halves
// independently testable: aggregation has well-defined output
// for a given plane list, rendering has well-defined output for
// a given StatsRender.
func AggregateStats(
	stream *adsb.ADSB,
	tracker *StatsTracker,
	myLocation *location.Location,
	planeList *airplanes.Airplanes,
) StatsRender {
	frameStats := stream.Stats()
	framesPerSec, recoveredPerSec := tracker.Sample(frameStats)

	receiverLat, receiverLon := myLocation.GetCoordinates()
	tracked := planeList.Count()

	agg := walkPlanes(planeList.Sorted(receiverLat, receiverLon), receiverLat, receiverLon)

	return StatsRender{
		Tracked:          tracked,
		Positioned:       agg.positioned,
		NearestDist:      agg.nearestDist,
		NearestCallsign:  agg.nearestCallsign,
		FarthestDist:     agg.farthestDist,
		FarthestCallsign: agg.farthestCallsign,
		HighestAlt:       agg.highestAlt,
		HighestCallsign:  agg.highestCallsign,
		FramesPerSec:     framesPerSec,
		RecoveredPerSec:  recoveredPerSec,
		TotalFrames:      frameStats.TotalFrames,
		RecoveredFrames:  frameStats.RecoveredFrames,
		CallsignsDecoded: frameStats.CallsignsDecoded,
		CallsignsApplied: frameStats.CallsignsApplied,
	}
}

// planeAggregate is the per-tick reduction over the sorted
// plane list. Hoisted out of AggregateStats so the walking
// helper has a tight, testable signature.
type planeAggregate struct {
	positioned                        int
	nearestDist                       float64
	farthestDist                      float64
	nearestCallsign, farthestCallsign string
	highestAlt                        float64
	highestCallsign                   string
}

func walkPlanes(planeList []airplane.Snapshot, receiverLat, receiverLon float64) planeAggregate {
	agg := planeAggregate{nearestDist: math.MaxFloat64}

	for _, snap := range planeList {
		if snap.Latitude == 0 && snap.Longitude == 0 {
			continue
		}

		distance := airplanes.HaversineDistance(receiverLat, receiverLon, snap.Latitude, snap.Longitude)
		if distance == math.MaxFloat64 {
			continue
		}

		agg.positioned++

		if distance < agg.nearestDist {
			agg.nearestDist = distance
			agg.nearestCallsign = DisplayIdent(snap)
		}

		if distance > agg.farthestDist {
			agg.farthestDist = distance
			agg.farthestCallsign = DisplayIdent(snap)
		}

		if snap.Altitude > agg.highestAlt {
			agg.highestAlt = snap.Altitude
			agg.highestCallsign = DisplayIdent(snap)
		}
	}

	return agg
}
