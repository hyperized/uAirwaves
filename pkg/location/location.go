package location

import (
	"fmt"
	"sync"
	"time"
)

const (
	defaultLatitude  = 0.0
	defaultLongitude = 0.0
	defaultAltitude  = 0.0
)

// Location represents GPS coordinates, mode and altitude.
type Location struct {
	latitude, longitude, altitude float64
	lastUpdated                   time.Time
	mode                          fix
	mu                            sync.RWMutex
}

// Option is a function that modifies a Location.
type Option func(*Location)

// New initializes a new Location.
func New(opts ...Option) *Location {
	location := &Location{
		latitude:    defaultLatitude,
		longitude:   defaultLongitude,
		altitude:    defaultAltitude,
		mode:        unknown,
		lastUpdated: time.Now(),
	}

	for _, opt := range opts {
		opt(location)
	}

	return location
}

// GetCoordinates returns the current latitude and longitude.
func (l *Location) GetCoordinates() (float64, float64) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return l.latitude, l.longitude
}

// Altitude returns the current altitude in metres.
func (l *Location) Altitude() float64 {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return l.altitude
}

// Mode returns the human-readable fix mode (e.g. "3D fix").
// Returns the empty string when no fix has been reported.
func (l *Location) Mode() string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return fixMode[l.mode]
}

// HasFix reports whether the receiver currently has a 2D or 3D
// GPS fix. Returns false for "no fix" and uninitialised state.
func (l *Location) HasFix() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return l.mode == twoD || l.mode == threeD
}

// Update applies the provided options to an existing Location.
func (l *Location) Update(opts ...Option) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, opt := range opts {
		opt(l)
	}

	l.lastUpdated = time.Now()
}

// String returns a human-readable representation of the location.
func (l *Location) String() string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return fmt.Sprintf(
		"lat %.9f, lon %.9f %.4fm %s",
		l.latitude, l.longitude, l.altitude, fixMode[l.mode],
	)
}

// WithLatitude sets the latitude of the location.
func WithLatitude(lat float64) Option {
	return func(l *Location) {
		l.latitude = lat
	}
}

// WithLongitude sets the longitude of the location.
func WithLongitude(lon float64) Option {
	return func(l *Location) {
		l.longitude = lon
	}
}

// WithAltitude sets the altitude of the location.
func WithAltitude(alt float64) Option {
	return func(l *Location) {
		l.altitude = alt
	}
}

// WithMode sets the mode of the location.
func WithMode(m int) Option {
	return func(l *Location) {
		l.mode = fix(m)
	}
}
