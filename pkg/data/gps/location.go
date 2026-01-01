package gps

import "fmt"

type Location struct {
	Latitude, Longitude, Altitude float64
}

// Option defines the function signature for configuring a Location
type Option func(*Location)

// NewLocation creates a new Location with the provided options.
// It defaults to 0.0 for all fields.
func NewLocation(opts ...Option) *Location {
	l := &Location{
		Latitude:  0.0,
		Longitude: 0.0,
		Altitude:  0.0,
	}

	for _, opt := range opts {
		opt(l)
	}

	return l
}

func (l *Location) String() string {
	return fmt.Sprintf("%f,%f ^%f", l.Latitude, l.Longitude, l.Altitude)
}

// WithLatitude sets the Latitude of the location
func WithLatitude(lat float64) Option {
	return func(l *Location) {
		l.Latitude = lat
	}
}

// WithLongitude sets the Longitude of the location
func WithLongitude(lon float64) Option {
	return func(l *Location) {
		l.Longitude = lon
	}
}

// WithAltitude sets the Altitude of the location
func WithAltitude(alt float64) Option {
	return func(l *Location) {
		l.Altitude = alt
	}
}

// WithCoordinates is a convenience option to set both Lat and Lon at once
func WithCoordinates(lat, lon float64) Option {
	return func(l *Location) {
		l.Latitude = lat
		l.Longitude = lon
	}
}
