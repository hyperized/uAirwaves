package airplanes

import (
	"math"
	"sort"
	"sync"
	"time"

	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplane"
)

type airplaneMap map[string]*airplane.Airplane

// List is a thread-safe slice of airplanes.
type List []*airplane.Airplane

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

// Sorted returns a thread-safe snapshot of planes sorted by:
// 1. Distance to receiver (ascending)
// 2. Last update (descending)
// 3. ICAO (ascending).
func (l *Airplanes) Sorted(receiverLat, receiverLon float64) List {
	l.mu.RLock()
	defer l.mu.RUnlock()

	result := make(List, 0, len(l.planes))
	for _, p := range l.planes {
		result = append(result, p)
	}

	sort.Slice(result, func(i, j int) bool {
		snapI := result[i].GetSnapshot()
		snapJ := result[j].GetSnapshot()

		// 2. Distance to receiver (ascending - closer first)
		distI := HaversineDistance(receiverLat, receiverLon, snapI.Latitude, snapI.Longitude)
		distJ := HaversineDistance(receiverLat, receiverLon, snapJ.Latitude, snapJ.Longitude)

		if distI != distJ {
			return distI < distJ
		}

		// 3. Last update (descending - more recent first)
		if !snapI.LastUpdate.Equal(snapJ.LastUpdate) {
			return snapI.LastUpdate.After(snapJ.LastUpdate)
		}

		// 4. icao (ascending)
		return snapI.ICAO < snapJ.ICAO
	})

	return result
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
