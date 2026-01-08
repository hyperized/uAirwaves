package gps

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/stratoberry/go-gpsd"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/location"
)

var errDial = errors.New("failed to dial gpsd service")

// Session defines the interface for a gpsd session.
type Session interface {
	AddFilter(filterType string, filter gpsd.Filter)
	Watch() chan bool
	Close() error
}

// GPS represents a GPS daemon connection.
type GPS struct {
	gpsAddress, serviceName, protocol string
	session                           Session
	dial                              func(string) (Session, error)
}

// Option defines the function signature for configuring a GPS instance.
type Option func(*GPS)

// New creates a new GPS instance with default values and applies provided options.
func New(opts ...Option) *GPS {
	gps := &GPS{
		gpsAddress:  "127.0.0.1:2947",
		serviceName: "gpsd.service",
		protocol:    "tcp",
		dial: func(address string) (Session, error) {
			s, err := gpsd.Dial(address)
			if err != nil {
				return nil, fmt.Errorf("failed to dial gpsd: %w", err)
			}

			return s, nil
		},
	}

	for _, opt := range opts {
		opt(gps)
	}

	return gps
}

// WithGpsAddress sets the address for the gpsd connection.
func WithGpsAddress(address string) Option {
	return func(g *GPS) {
		g.gpsAddress = address
	}
}

// WithServiceName sets the systemd service name to manage.
func WithServiceName(name string) Option {
	return func(g *GPS) {
		g.serviceName = name
	}
}

// WithProtocol sets the protocol to use for the gpsd connection.
func WithProtocol(protocol string) Option {
	return func(g *GPS) {
		g.protocol = protocol
	}
}

// Watch starts the GPS monitoring process.
func (g *GPS) Watch(ctx context.Context, myLocation *location.Location) error {
	err := g.connect()
	if err != nil {
		return err
	}

	defer g.disconnect()

	// Read the Time-Position-Velocity report
	g.session.AddFilter("TPV", func(t any) {
		if tpvReport, ok := t.(*gpsd.TPVReport); ok {
			myLocation.Update(
				location.WithMode(int(tpvReport.Mode)),
				location.WithLatitude(tpvReport.Lat),
				location.WithLongitude(tpvReport.Lon),
				location.WithAltitude(tpvReport.Alt),
			)
		}
	})

	done := g.session.Watch()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return nil
	}
}

func (g *GPS) connect() error {
	var err error

	g.session, err = g.dial(g.gpsAddress)
	if err != nil {
		return errors.Join(err, errDial)
	}

	return nil
}

func (g *GPS) disconnect() {
	slog.Info("Disconnecting from GPS daemon", slog.Any("error", g.session.Close()))
}
