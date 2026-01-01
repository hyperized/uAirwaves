package gps

import (
	"context"
	"errors"
	"sync"

	"github.com/stratoberry/go-gpsd"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/service"
)

type GPS struct {
	GpsAddress  string
	ServiceName string
	mu          sync.RWMutex
	Location    *Location
}

// GPSOption defines the function signature for configuring a GPS instance
type GPSOption func(*GPS)

// New creates a new GPS instance with default values and applies provided options
func New(opts ...GPSOption) *GPS {
	g := &GPS{
		GpsAddress:  "127.0.0.1:2947",
		ServiceName: "gpsd.service",
		Location:    &Location{},
	}

	for _, opt := range opts {
		opt(g)
	}

	return g
}

// WithGpsAddress sets the address for the gpsd connection
func WithGpsAddress(address string) GPSOption {
	return func(g *GPS) {
		g.GpsAddress = address
	}
}

// WithServiceName sets the systemd service name to manage
func WithServiceName(name string) GPSOption {
	return func(g *GPS) {
		g.ServiceName = name
	}
}

// Watch starts the GPS monitoring process
func (g *GPS) Watch(ctx context.Context, errChan chan<- error) {
	n := service.New(g.ServiceName, service.WithEnable(), service.WithStart())
	err := n.Execute(ctx)
	if err != nil {
		errChan <- errors.Join(err, errors.New("failed to start service ("+g.ServiceName+")"))
		return
	}

	gps, err := gpsd.Dial(g.GpsAddress)
	if err != nil {
		errChan <- errors.Join(err, errors.New("failed to connect to address ("+g.GpsAddress+")"))
		return
	}
	defer gps.Close()

	// Only grab the Time-Position-Velocity report
	gps.AddFilter("TPV", func(r interface{}) {
		if tpvReport, ok := r.(*gpsd.TPVReport); ok {
			g.mu.Lock()
			// Using the New function for Location we created earlier
			g.Location = NewLocation(
				WithLatitude(tpvReport.Lat),
				WithLongitude(tpvReport.Lon),
				WithAltitude(tpvReport.Alt),
			)
			g.mu.Unlock()
		}
	})

	done := gps.Watch()

	// Wait for done channel or context cancellation
	select {
	case <-done:
		errChan <- nil
		return
	case <-ctx.Done():
		errChan <- ctx.Err()
		return
	}
}

// GetLocation returns a thread-safe copy of the current location
func (g *GPS) GetLocation() *Location {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.Location == nil {
		return &Location{}
	}
	return &Location{
		Latitude:  g.Location.Latitude,
		Longitude: g.Location.Longitude,
		Altitude:  g.Location.Altitude,
	}
}
