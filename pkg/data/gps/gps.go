package gps

import (
	"context"
	"errors"
	"sync"

	"github.com/stratoberry/go-gpsd"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/service"
)

type GPS struct {
	gpsAddress, serviceName string
	mu                      sync.RWMutex
}

// Option defines the function signature for configuring a GPS instance
type Option func(*GPS)

// New creates a new GPS instance with default values and applies provided options
func New(opts ...Option) *GPS {
	g := &GPS{
		gpsAddress:  "127.0.0.1:2947",
		serviceName: "gpsd.service",
	}

	for _, opt := range opts {
		opt(g)
	}

	return g
}

// WithGpsAddress sets the address for the gpsd connection
func WithGpsAddress(address string) Option {
	return func(g *GPS) {
		g.gpsAddress = address
	}
}

// WithServiceName sets the systemd service name to manage
func WithServiceName(name string) Option {
	return func(g *GPS) {
		g.serviceName = name
	}
}

func (g *GPS) Stop(ctx context.Context) error {
	return service.New(g.serviceName, service.WithStop()).Execute(ctx)
}

// Watch starts the GPS monitoring process
func (g *GPS) Watch(ctx context.Context, location *Location, errChan chan<- error) {
	err := service.New(g.serviceName, service.WithEnable(), service.WithStart()).Execute(ctx)
	if err != nil {
		errChan <- errors.Join(err, errors.New("failed to start service ("+g.serviceName+")"))
		return
	}

	gps, err := gpsd.Dial(g.gpsAddress)
	if err != nil {
		errChan <- errors.Join(err, errors.New("failed to connect to address ("+g.gpsAddress+")"))
		return
	}
	defer gps.Close()

	// Read the Time-Position-Velocity report
	gps.AddFilter("TPV", func(r interface{}) {
		if tpvReport, ok := r.(*gpsd.TPVReport); ok {
			g.mu.Lock()
			location.Update(
				WithMode(tpvReport.Mode),
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
