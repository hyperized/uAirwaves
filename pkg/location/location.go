package location

import (
	"fmt"
	"sync"
	"time"
)

type Location struct {
	latitude, longitude, altitude float64
	lastUpdated                   time.Time
	mode                          int
	mu                            sync.RWMutex
}

type Option func(*Location)

func New(opts ...Option) *Location {
	l := &Location{
		latitude:    0.0,
		longitude:   0.0,
		altitude:    0.0,
		mode:        0,
		lastUpdated: time.Now(),
	}

	for _, opt := range opts {
		opt(l)
	}

	return l
}

// GetCoordinates returns the current latitude and longitude
func (l *Location) GetCoordinates() (latitude, longitude float64) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.latitude, l.longitude
}

// Update applies the provided options to an existing Location
func (l *Location) Update(opts ...Option) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, opt := range opts {
		opt(l)
	}
	l.lastUpdated = time.Now()
}

func (l *Location) String() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return fmt.Sprintf("lat %.9f, lon %.9f %.4fm - mode %d", l.latitude, l.longitude, l.altitude, l.mode)
}

// WithLatitude sets the latitude of the location
func WithLatitude(lat float64) Option {
	return func(l *Location) {
		l.latitude = lat
	}
}

// WithLongitude sets the longitude of the location
func WithLongitude(lon float64) Option {
	return func(l *Location) {
		l.longitude = lon
	}
}

// WithAltitude sets the altitude of the location
func WithAltitude(alt float64) Option {
	return func(l *Location) {
		l.altitude = alt
	}
}

// WithMode sets the mode of the location
func WithMode(mode int) Option {
	return func(l *Location) {
		l.mode = mode
	}
}
