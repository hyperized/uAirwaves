package gps

import (
	"fmt"
	"sync"
	"time"

	"github.com/stratoberry/go-gpsd"
)

type Location struct {
	latitude, longitude, altitude float64
	lastUpdated                   time.Time
	mode                          gpsd.Mode
	mu                            sync.RWMutex
}

type LocationOption func(*Location)

func NewLocation(opts ...LocationOption) *Location {
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

// GetCoordinates returns the current latitude, longitude, altitude, and mode values
func (l *Location) GetCoordinates() (latitude, longitude float64) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.latitude, l.longitude
}

// Update applies the provided options to an existing Location
func (l *Location) Update(opts ...LocationOption) {
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
	return fmt.Sprintf("%.9flat, %.9flon %.4fm - mode %d %dms", l.latitude, l.longitude, l.altitude, l.mode, time.Since(l.lastUpdated.UTC()).Milliseconds())
}

// WithLatitude sets the latitude of the location
func WithLatitude(lat float64) LocationOption {
	return func(l *Location) {
		l.latitude = lat
	}
}

// WithLongitude sets the longitude of the location
func WithLongitude(lon float64) LocationOption {
	return func(l *Location) {
		l.longitude = lon
	}
}

// WithAltitude sets the altitude of the location
func WithAltitude(alt float64) LocationOption {
	return func(l *Location) {
		l.altitude = alt
	}
}

// WithMode sets the mode of the location
func WithMode(mode gpsd.Mode) LocationOption {
	return func(l *Location) {
		l.mode = mode
	}
}
