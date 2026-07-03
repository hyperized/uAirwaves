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
// adsb.Stats() mutating shared state. It also remembers the
// session-peak farthest distance, highest altitude, and frame
// rate so the panel can show a record alongside the current
// value for each.
type StatsTracker struct {
	lastTotal     uint64
	lastSampledAt time.Time

	farthestRecordDist     float64
	farthestRecordCallsign string
	highestRecordAlt       float64
	highestRecordCallsign  string
	framesPerSecRecord     float64
}

// NewStatsTracker returns a tracker seeded with the construction
// time. The first Sample call after construction has a tiny
// window (now - construct time), so the rate is approximate
// until the second tick.
func NewStatsTracker() *StatsTracker {
	return &StatsTracker{lastSampledAt: time.Now()}
}

// Sample returns the per-second frame rate since the previous
// call. The first call after NewStatsTracker has a tiny window
// (now - construct time), so the rate is approximate until the
// second tick. The session-peak rate is promoted in lock-step
// so callers don't have to track it separately.
func (t *StatsTracker) Sample(stats adsb.Stats) float64 {
	now := time.Now()
	elapsed := now.Sub(t.lastSampledAt).Seconds()

	var framesPerSec float64
	if elapsed > 0 {
		framesPerSec = float64(stats.TotalFrames-t.lastTotal) / elapsed
	}

	t.lastTotal = stats.TotalFrames
	t.lastSampledAt = now

	if framesPerSec > t.framesPerSecRecord {
		t.framesPerSecRecord = framesPerSec
	}

	return framesPerSec
}

// FramesPerSecRecord returns the session-peak frames-per-second
// observed by Sample. Monotonic: never decreases for the
// lifetime of the tracker.
func (t *StatsTracker) FramesPerSecRecord() float64 {
	return t.framesPerSecRecord
}

// UpdateRecords promotes the current observation to a new
// session record when it exceeds the prior peak. Records are
// monotonic: once set they only grow within a session and never
// reset back to zero.
func (t *StatsTracker) UpdateRecords(
	farthestDist float64, farthestCallsign string,
	highestAlt float64, highestCallsign string,
) {
	if farthestDist > t.farthestRecordDist && farthestCallsign != "" {
		t.farthestRecordDist = farthestDist
		t.farthestRecordCallsign = farthestCallsign
	}

	if highestAlt > t.highestRecordAlt && highestCallsign != "" {
		t.highestRecordAlt = highestAlt
		t.highestRecordCallsign = highestCallsign
	}
}

// FarthestRecord returns the session-peak distance and the
// callsign that set it. Both zero when no positioned plane has
// been observed yet.
//
//nolint:nonamedreturns // dist/callsign are clearer named here.
func (t *StatsTracker) FarthestRecord() (dist float64, callsign string) {
	return t.farthestRecordDist, t.farthestRecordCallsign
}

// HighestRecord returns the session-peak altitude and the
// callsign that set it. Both zero when no altitude-bearing plane
// has been observed yet.
//
//nolint:nonamedreturns // alt/callsign are clearer named here.
func (t *StatsTracker) HighestRecord() (alt float64, callsign string) {
	return t.highestRecordAlt, t.highestRecordCallsign
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
	FarthestRecordDist                 float64
	FarthestRecordCallsign             string
	HighestRecordAlt                   float64
	HighestRecordCallsign              string
	FramesPerSec                       float64
	FramesPerSecRecord                 float64
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

	if render.FarthestRecordDist > 0 && render.FarthestRecordCallsign != "" {
		farthest += fmt.Sprintf(
			"  [gray](%.1f nm %s)[white]",
			render.FarthestRecordDist, render.FarthestRecordCallsign,
		)
	}

	highest := "—"
	if render.HighestAlt > 0 {
		highest = fmt.Sprintf("%.0f ft  [gray]%s[white]", render.HighestAlt, render.HighestCallsign)
	}

	if render.HighestRecordAlt > 0 && render.HighestRecordCallsign != "" {
		highest += fmt.Sprintf(
			"  [gray](%.0f ft %s)[white]",
			render.HighestRecordAlt, render.HighestRecordCallsign,
		)
	}

	framesLine := fmt.Sprintf("%.1f", render.FramesPerSec)
	if render.FramesPerSecRecord > 0 {
		framesLine += fmt.Sprintf("  [gray](peak %.1f)[white]", render.FramesPerSecRecord)
	}

	return fmt.Sprintf(
		"[::b]Tracked[::-]    %d  ([gray]%d positioned[white])\n"+
			"[::b]Nearest[::-]    %s\n"+
			"[::b]Farthest[::-]   %s\n"+
			"[::b]Highest[::-]    %s\n"+
			"[::b]Frames/s[::-]   %s\n"+
			"[::b]Total[::-]      %d  ([gray]IDs %d/%d[white])",
		render.Tracked, render.Positioned,
		nearest,
		farthest,
		highest,
		framesLine,
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
	framesPerSec := tracker.Sample(frameStats)

	receiverLat, receiverLon := myLocation.GetCoordinates()
	tracked := planeList.Count()

	agg := walkPlanes(planeList.Sorted(receiverLat, receiverLon), receiverLat, receiverLon)

	tracker.UpdateRecords(agg.farthestDist, agg.farthestCallsign, agg.highestAlt, agg.highestCallsign)
	farthestRecordDist, farthestRecordCallsign := tracker.FarthestRecord()
	highestRecordAlt, highestRecordCallsign := tracker.HighestRecord()

	return StatsRender{
		Tracked:                tracked,
		Positioned:             agg.positioned,
		NearestDist:            agg.nearestDist,
		NearestCallsign:        agg.nearestCallsign,
		FarthestDist:           agg.farthestDist,
		FarthestCallsign:       agg.farthestCallsign,
		HighestAlt:             agg.highestAlt,
		HighestCallsign:        agg.highestCallsign,
		FarthestRecordDist:     farthestRecordDist,
		FarthestRecordCallsign: farthestRecordCallsign,
		HighestRecordAlt:       highestRecordAlt,
		HighestRecordCallsign:  highestRecordCallsign,
		FramesPerSec:           framesPerSec,
		FramesPerSecRecord:     tracker.FramesPerSecRecord(),
		TotalFrames:            frameStats.TotalFrames,
		RecoveredFrames:        frameStats.RecoveredFrames,
		CallsignsDecoded:       frameStats.CallsignsDecoded,
		CallsignsApplied:       frameStats.CallsignsApplied,
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
