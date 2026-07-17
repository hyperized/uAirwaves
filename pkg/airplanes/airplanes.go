// Package airplanes is the thread-safe collection of tracked aircraft,
// keyed by ICAO. It handles upsert and staleness pruning, and sorts the
// fleet by Haversine distance to the receiver, then recency, then ICAO —
// with an unknown (0,0) position sorting last via the MaxFloat64 sentinel.
package airplanes

import (
	"cmp"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/hyperized/uAirwaves/pkg/airplane"
)

type airplaneMap map[string]*airplane.Airplane

// List is a thread-safe slice of snapshot-level airplane data.
// Snapshots are point-in-time value copies — safe to iterate
// without holding any package lock.
type List []airplane.Snapshot

// Airplanes represents a thread-safe list of airplanes.
type Airplanes struct {
	mu     sync.RWMutex
	planes airplaneMap
}

// New initializes a new thread-safe airplanes list.
func New() *Airplanes {
	return &Airplanes{
		planes: make(airplaneMap),
	}
}

// Ensure adds the plane if it doesn't already exist.
func (l *Airplanes) Ensure(i string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, exists := l.planes[i]; !exists {
		l.planes[i] = airplane.New(i)
	}
}

// Get retrieves a plane by its ICAO.
func (l *Airplanes) Get(i string) (*airplane.Airplane, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	p, ok := l.planes[i]

	return p, ok
}

// Prune removes planes that haven't been updated in the given timeframe.
func (l *Airplanes) Prune(threshold time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := time.Now().UTC().Add(-threshold)

	for icao, plane := range l.planes {
		if plane.GetLastUpdate().Before(cutoff) {
			delete(l.planes, icao)
		}
	}
}

// SortOption tunes what Sorted copies into each snapshot.
type SortOption func(*sortConfig)

type sortConfig struct {
	trails bool
}

// WithTrails keeps each snapshot's position history. Sorted omits it
// by default because two of its three consumers (plane list, stats)
// never read the trail; only the radar draw opts in.
func WithTrails() SortOption {
	return func(c *sortConfig) {
		c.trails = true
	}
}

// Sorted returns a thread-safe slice of plane snapshots sorted
// by:
//
//  1. Distance to receiver (ascending)
//  2. Last update (descending)
//  3. ICAO (ascending)
//
// Each plane's snapshot is taken once during the read pass, so
// the comparator works on value copies and the per-tick total is
// one RLock per *Airplane (not three: previous shape took a
// snapshot pair per comparator call). Callers consume snapshots
// directly — no re-acquire on the hot path. Position history is
// dropped from the snapshots unless WithTrails is passed.
func (l *Airplanes) Sorted(receiverLat, receiverLon float64, opts ...SortOption) List {
	cfg := sortConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}

	var snapOpts []airplane.SnapshotOption
	if !cfg.trails {
		snapOpts = append(snapOpts, airplane.WithoutHistory())
	}

	l.mu.RLock()

	snapshots := make(List, 0, len(l.planes))
	for _, p := range l.planes {
		snapshots = append(snapshots, p.GetSnapshot(snapOpts...))
	}

	l.mu.RUnlock()

	slices.SortFunc(snapshots, func(left, right airplane.Snapshot) int {
		// 1. Distance to receiver (ascending - closer first)
		distL := HaversineDistance(receiverLat, receiverLon, left.Latitude, left.Longitude)
		distR := HaversineDistance(receiverLat, receiverLon, right.Latitude, right.Longitude)

		if cmpDist := cmp.Compare(distL, distR); cmpDist != 0 {
			return cmpDist
		}

		// 2. Last update (descending - more recent first)
		if !left.LastUpdate.Equal(right.LastUpdate) {
			return right.LastUpdate.Compare(left.LastUpdate)
		}

		// 3. ICAO (ascending)
		return cmp.Compare(left.ICAO, right.ICAO)
	})

	return snapshots
}

// HaversineDistance calculates the distance in nautical miles between two coordinates.
func HaversineDistance(lat1, lon1, lat2, lon2 float64) float64 {
	// If either position is unknown (0,0), treat as infinitely far
	if (lat1 == 0 && lon1 == 0) || (lat2 == 0 && lon2 == 0) {
		return math.MaxFloat64
	}

	const (
		earthRadiusNM     = 3440.065 // Earth radius in nautical miles
		halfCircleDegrees = 180
	)

	lat1Rad := lat1 * math.Pi / halfCircleDegrees
	lat2Rad := lat2 * math.Pi / halfCircleDegrees
	deltaLat := (lat2 - lat1) * math.Pi / halfCircleDegrees
	deltaLon := (lon2 - lon1) * math.Pi / halfCircleDegrees

	a := math.Sin(deltaLat/2)*math.Sin(deltaLat/2) +
		math.Cos(lat1Rad)*math.Cos(lat2Rad)*math.Sin(deltaLon/2)*math.Sin(deltaLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return earthRadiusNM * c
}

// Count returns the number of planes in the list.
func (l *Airplanes) Count() int {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return len(l.planes)
}
