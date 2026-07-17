// Package airplane models the state of a single tracked aircraft. State is
// mutated through functional options and read through point-in-time
// snapshots taken under one lock, so callers never hold a lock while
// rendering. Position fixes accumulate in a capped history trail used for
// radar trail drawing.
package airplane

import (
	"fmt"
	"sync"
	"time"
)

const (
	minLatitude  = -90
	maxLatitude  = 90
	minLongitude = -180
	maxLongitude = 180
	minHeading   = 0
	maxHeading   = 360
	minVelocity  = 0

	defaultMessageCount     = 1
	defaultHeading          = -1
	defaultVelocity         = -1
	defaultVertRate         = 0
	positionHistoryInterval = 10 * time.Second

	// maxPositionHistory caps the trail at 256 fixes: at one fix per
	// positionHistoryInterval that is ~43 min of history, ~6 KiB per plane.
	maxPositionHistory = 256

	squawkHijacking        = "7500"
	squawkRadioFailure     = "7600"
	squawkGeneralEmergency = "7700"
)

// PositionEntry holds a single historical lat/lon fix. Altitude
// captures the barometric altitude (feet) the plane was at when
// the fix was sampled so the radar's trail render can colour each
// dot by the flight level flown at that point.
type PositionEntry struct {
	Latitude, Longitude, Altitude float64
}

// Option is a function that modifies an Airplane.
type Option func(*Airplane)

// Airplane represents a single airplane.
type Airplane struct {
	icao, callsign, squawk                                     string
	altitude, heading, velocity, latitude, longitude, vertRate float64
	lastUpdate                                                 time.Time
	lastPositionTime                                           time.Time
	emergency                                                  bool
	messageCount                                               int64
	positionHistory                                            []PositionEntry
	mu                                                         sync.RWMutex
}

// New initializes a new Airplane.
func New(icao string) *Airplane {
	return &Airplane{
		icao:         icao,
		messageCount: defaultMessageCount,
		lastUpdate:   time.Now(),
		heading:      defaultHeading,
		velocity:     defaultVelocity,
		vertRate:     defaultVertRate,
	}
}

// String returns a formatted string representation of the plane.
func (a *Airplane) String() string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return fmt.Sprintf("%.0fft %.0fo %.0fkts %.0ffpm %.0fs",
		a.altitude,
		a.heading,
		a.velocity,
		a.vertRate,
		time.Since(a.lastUpdate.UTC()).Seconds(),
	)
}

// Snapshot holds a point-in-time copy of airplane data for lock-free reads.
type Snapshot struct {
	ICAO            string
	Callsign        string
	Altitude        float64
	Heading         float64
	Velocity        float64
	VertRate        float64
	Latitude        float64
	Longitude       float64
	LastUpdate      time.Time
	Squawk          string
	Emergency       bool
	MessageCount    int64
	PositionHistory []PositionEntry
}

// SnapshotOption tunes what GetSnapshot copies out of the airplane.
type SnapshotOption func(*snapshotConfig)

type snapshotConfig struct {
	includeHistory bool
}

// WithoutHistory leaves Snapshot.PositionHistory nil, skipping the
// O(history) copy for callers that never read the trail (the plane
// list and stats panel). GetSnapshot copies the trail by default.
func WithoutHistory() SnapshotOption {
	return func(c *snapshotConfig) {
		c.includeHistory = false
	}
}

// GetSnapshot returns all fields in a single lock acquisition. The
// position history is deep-copied so the snapshot stays isolated from
// later in-place trail mutation; pass WithoutHistory to skip that copy.
func (a *Airplane) GetSnapshot(opts ...SnapshotOption) Snapshot {
	cfg := snapshotConfig{includeHistory: true}
	for _, opt := range opts {
		opt(&cfg)
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	var history []PositionEntry
	if cfg.includeHistory {
		history = make([]PositionEntry, len(a.positionHistory))
		copy(history, a.positionHistory)
	}

	return Snapshot{
		ICAO:            a.icao,
		Callsign:        a.callsign,
		Altitude:        a.altitude,
		Heading:         a.heading,
		Velocity:        a.velocity,
		VertRate:        a.vertRate,
		Latitude:        a.latitude,
		Longitude:       a.longitude,
		LastUpdate:      a.lastUpdate,
		Squawk:          a.squawk,
		Emergency:       a.emergency,
		MessageCount:    a.messageCount,
		PositionHistory: history,
	}
}

// Summary returns the same one-line "alt heading velocity vrate
// age" string as Airplane.String, computed against the snapshot
// values so callers that already hold a Snapshot don't have to
// round-trip through the live airplane (and reacquire its lock)
// to format a list entry. The two outputs are byte-identical for
// a snapshot taken from the same airplane.
func (s Snapshot) Summary() string {
	return fmt.Sprintf("%.0fft %.0fo %.0fkts %.0ffpm %.0fs",
		s.Altitude,
		s.Heading,
		s.Velocity,
		s.VertRate,
		time.Since(s.LastUpdate.UTC()).Seconds(),
	)
}

// GetLastUpdate returns the last time the plane was updated.
// Kept as a per-field accessor because two hot paths
// (airplanes.Prune and pkg/adsb CPR resolution) read just this
// field per plane per frame; using GetSnapshot for that would
// allocate and copy the entire snapshot purely to discard
// everything but lastUpdate. All other fields go through
// GetSnapshot — see CLAUDE.md's "Snapshots for lock-free reads".
func (a *Airplane) GetLastUpdate() time.Time {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.lastUpdate
}

// Update updates the airplane with new data.
func (a *Airplane) Update(opts ...Option) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.messageCount++

	for _, opt := range opts {
		opt(a)
	}
}

// WithCallsign returns an option to update the callsign.
func WithCallsign(c string) Option {
	return func(a *Airplane) {
		if c != "" {
			a.callsign = c
		}
	}
}

// WithSquawk returns an option to update the squawk. The emergency
// flag is recomputed on every non-empty update so a plane that
// transitions out of an emergency squawk (e.g. 7700 → 1200) clears
// the flag instead of staying stuck in the previous state.
func WithSquawk(squawk string) Option {
	return func(airplane *Airplane) {
		if squawk != "" {
			airplane.squawk = squawk
			airplane.emergency = squawk == squawkHijacking ||
				squawk == squawkRadioFailure ||
				squawk == squawkGeneralEmergency
		}
	}
}

// WithLastUpdate returns an option to update the timestamp.
func WithLastUpdate(t time.Time) Option {
	return func(a *Airplane) {
		if !t.IsZero() {
			a.lastUpdate = t
		}
	}
}

// WithAltitude returns an option to update the altitude.
func WithAltitude(altitude float64) Option {
	return func(a *Airplane) {
		a.altitude = altitude
	}
}

// WithLatitude clamps latitude to valid range [-90, 90].
func WithLatitude(latitude float64) Option {
	return func(a *Airplane) {
		a.latitude = max(min(latitude, maxLatitude), minLatitude)
	}
}

// WithLongitude clamps longitude to valid range [-180, 180].
func WithLongitude(longitude float64) Option {
	return func(a *Airplane) {
		a.longitude = max(min(longitude, maxLongitude), minLongitude)
	}
}

// WithVelocity returns an option to update the velocity.
func WithVelocity(v float64) Option {
	return func(a *Airplane) {
		if v > minVelocity {
			a.velocity = v
		}
	}
}

// WithHeading clamps heading to valid range [0, 360].
func WithHeading(heading float64) Option {
	return func(a *Airplane) {
		a.heading = max(min(heading, maxHeading), minHeading)
	}
}

// WithVertRate returns an option to update the vertical rate.
func WithVertRate(vertRate float64) Option {
	return func(plane *Airplane) {
		plane.vertRate = vertRate
	}
}

// WithPosition updates both latitude and longitude and appends
// the fix to position history at most once per
// positionHistoryInterval to avoid filling history with
// near-identical entries. The current plane.altitude is captured
// into the entry so trail rendering can colour each dot by the
// flight level flown at that fix. WithAltitude is conventionally
// applied earlier in the same Update call (see pkg/adsb/handle.go),
// so the captured altitude is the one that arrived in the same
// ADSB frame as the position.
func WithPosition(latitude, longitude float64) Option {
	return func(plane *Airplane) {
		plane.latitude = max(min(latitude, maxLatitude), minLatitude)
		plane.longitude = max(min(longitude, maxLongitude), minLongitude)

		if time.Since(plane.lastPositionTime) < positionHistoryInterval {
			return
		}

		plane.lastPositionTime = time.Now()

		entry := PositionEntry{
			Latitude:  plane.latitude,
			Longitude: plane.longitude,
			Altitude:  plane.altitude,
		}

		if len(plane.positionHistory) < maxPositionHistory {
			plane.positionHistory = append(plane.positionHistory, entry)

			return
		}

		// At cap: shift the window down one slot in place and store the
		// newest fix. GetSnapshot copied the old backing array, so prior
		// snapshots stay isolated from this reuse.
		copy(plane.positionHistory, plane.positionHistory[1:])
		plane.positionHistory[len(plane.positionHistory)-1] = entry
	}
}
