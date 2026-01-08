package adsb

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplane"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplanes"
)

const (
	empty              = ""
	jsonServiceNetwork = "tcp"
	jsonServiceAddress = "127.0.0.1:30047"
	buffer             = 64 * 1024
	minBufferSize      = 0
	maxBufferSize      = 1024 * 1024 // the maximum size of the buffer that may be allocated during scanning.

	defaultPruneThreshold = 1 * time.Minute
	defaultPruneFrequency = 5 * time.Second
)

var (
	errJSONStream              = errors.New("error reading from JSON stream")
	errJSONParse               = errors.New("error parsing JSON")
	errParseBarometricAltitude = errors.New("error parsing barometric altitude")
	errProcessAircraft         = errors.New("error processing aircraft")
	errDial                    = errors.New("error dialing JSON service")
	errNoAirplane              = errors.New("no airplane found")
)

// JSONAircraft represents the ADSB structure from readsb.
type JSONAircraft struct {
	Hex                    string   `json:"hex"`          // icao hex code
	Type                   string   `json:"type"`         // Message type
	Flight                 string   `json:"flight"`       // Callsign
	BarometricAltitude     AltBaro  `json:"alt_baro"`     // Barometric altitude in feet
	GeometricAltitude      *int     `json:"alt_geom"`     // Geometric altitude in feet
	GS                     *float64 `json:"gs"`           // Ground speed in knots
	TAS                    *float64 `json:"tas"`          // True airspeed in knots
	IAS                    *int     `json:"ias"`          // Indicated airspeed
	Track                  *float64 `json:"track"`        // Track/heading in degrees
	TrackRate              *float64 `json:"track_rate"`   // Rate of change of track
	Roll                   *float64 `json:"roll"`         // Roll angle
	MagHeading             *float64 `json:"mag_heading"`  // Magnetic heading
	TrueHeading            *float64 `json:"true_heading"` // True heading
	BarometricVerticalRate *int     `json:"baro_rate"`    // Vertical rate in ft/min
	GeometricVerticalRate  *int     `json:"geom_rate"`    // Geometric vertical rate
	Squawk                 string   `json:"squawk"`       // Squawk code
	Emergency              string   `json:"emergency"`    // Emergency status
	Category               string   `json:"category"`     // Aircraft category
	Lat                    *float64 `json:"lat"`          // latitude
	Lon                    *float64 `json:"lon"`          // longitude
	NIC                    *int     `json:"nic"`          // Navigation Integrity Category
	RC                     *int     `json:"rc"`           // Radius of Containment
	SeenPos                *float64 `json:"seen_pos"`     // Seconds since last position
	Version                *int     `json:"version"`      // ADS-B version
	NicSupplement          *int     `json:"nic_baro"`     // NIC supplement for barometric altitude
	NACp                   *int     `json:"nac_p"`        // Navigation Accuracy Category - Position
	NACv                   *int     `json:"nac_v"`        // Navigation Accuracy Category - Velocity
	SIL                    *int     `json:"sil"`          // Source Integrity Level
	SILType                string   `json:"sil_type"`     // Type of SIL
	GVA                    *int     `json:"gva"`          // Geometric Vertical Accuracy
	SDA                    *int     `json:"sda"`          // System Design Assurance
	Alert                  *int     `json:"alert"`        // Flight status alert bit
	SPI                    *int     `json:"spi"`          // Flight status SPI bit
	Messages               int      `json:"messages"`     // Total message count
	Seen                   float64  `json:"seen"`         // Seconds since last message
	RSSI                   *float64 `json:"rssi"`         // Signal strength in dBFS
	Dst                    *float64 `json:"dst"`          // Distance to receiver (km)
	Dir                    *float64 `json:"dir"`          // Direction to receiver (degrees)
	Now                    *float64 `json:"now"`          // Unix timestamp
}

// AltBaro represents the barometric altitude, which can be either a number or the string "ground".
type AltBaro float64

// UnmarshalJSON implements the json.Unmarshaler interface.
func (a *AltBaro) UnmarshalJSON(input []byte) error {
	var str string
	if err := json.Unmarshal(input, &str); err == nil {
		if str == "ground" {
			*a = 0

			return nil
		}
		// If str is a string but not "ground", try to parse it as a float
		f, err := strconv.ParseFloat(str, 64)
		if err != nil {
			return errors.Join(err, errParseBarometricAltitude)
		}

		*a = AltBaro(f)

		return nil
	}

	var f float64
	if err := json.Unmarshal(input, &f); err == nil {
		*a = AltBaro(f)

		return nil
	}

	return errJSONParse
}

// ADSB represents a connection to the ADSB service.
type ADSB struct {
	connection     net.Conn
	address        string
	pruneFrequency time.Duration
	pruneThreshold time.Duration
}

// Option is a function that modifies an ADSB instance.
type Option func(*ADSB)

// New initializes a new ADSB connection.
func New(opts ...Option) *ADSB {
	adsb := &ADSB{
		address:        jsonServiceAddress,
		pruneFrequency: defaultPruneFrequency,
		pruneThreshold: defaultPruneThreshold,
	}

	for _, opt := range opts {
		opt(adsb)
	}

	return adsb
}

// WithAddress sets the address for the ADSB connection.
func WithAddress(address string) Option {
	return func(a *ADSB) {
		a.address = address
	}
}

// WithPruneFrequency sets the frequency at which the airplanes list is pruned.
func WithPruneFrequency(frequency time.Duration) Option {
	return func(a *ADSB) {
		a.pruneFrequency = frequency
	}
}

// WithPruneThreshold sets the threshold for pruning old airplanes.
func WithPruneThreshold(threshold time.Duration) Option {
	return func(a *ADSB) {
		a.pruneThreshold = threshold
	}
}

// Stream grabs the ADSB messages from the JSON service and updates the airplanes list.
func (a *ADSB) Stream(ctx context.Context, planes *airplanes.Airplanes) error {
	err := a.connect(ctx)
	if err != nil {
		return err
	}
	defer a.disconnect()

	ticker := time.NewTicker(a.pruneFrequency)
	defer ticker.Stop()

	lines := make(chan string)
	errs := make(chan error, 1)

	go a.scan(ctx, lines, errs)

	for {
		select {
		case line, ok := <-lines:
			if !ok {
				return a.handleScanEnd(errs)
			}

			if err := a.handleLine(line, planes); err != nil {
				return err
			}
		case <-ticker.C:
			planes.Prune(a.pruneThreshold)
		case <-ctx.Done():
			return nil
		}
	}
}

func (a *ADSB) scan(ctx context.Context, lines chan<- string, errs chan<- error) {
	scanner := bufio.NewScanner(a.connection)
	buf := make([]byte, minBufferSize, buffer)
	scanner.Buffer(buf, maxBufferSize)

	for scanner.Scan() {
		select {
		case lines <- scanner.Text():
		case <-ctx.Done():
			return
		}
	}

	if err := scanner.Err(); err != nil {
		errs <- errors.Join(err, errJSONStream)
	}

	close(lines)
}

func (a *ADSB) handleScanEnd(errs <-chan error) error {
	select {
	case err := <-errs:
		return err
	default:
		return nil
	}
}

func (a *ADSB) handleLine(line string, planes *airplanes.Airplanes) error {
	if line == "" {
		return nil
	}

	var aircraft JSONAircraft
	if err := json.Unmarshal([]byte(line), &aircraft); err != nil {
		return errors.Join(err, errJSONParse)
	}

	if err := updatePlaneData(aircraft, planes); err != nil {
		return errors.Join(err, errProcessAircraft)
	}

	return nil
}

// connect establishes a connection to the JSON service.
func (a *ADSB) connect(ctx context.Context) error {
	var err error

	dialer := &net.Dialer{}

	a.connection, err = dialer.DialContext(ctx, jsonServiceNetwork, a.address)
	if err != nil {
		return errors.Join(err, errDial)
	}

	return nil
}

func (a *ADSB) disconnect() {
	slog.Info("Disconnecting from ADSB service", slog.Any("error", a.connection.Close()))
}

// MaxBufferSize returns the maximum buffer size allowed for scanning.
func MaxBufferSize() int {
	return maxBufferSize
}

// ErrJSONStream returns the error for JSON stream reading.
func ErrJSONStream() error {
	return errJSONStream
}

// ErrJSONParse returns the error for JSON parsing.
func ErrJSONParse() error {
	return errJSONParse
}

// ErrProcessAircraft returns the error for aircraft processing.
func ErrProcessAircraft() error {
	return errProcessAircraft
}

// ProcessAircraft updates the airplane list with the given JSON data.
func ProcessAircraft(aircraft JSONAircraft, planes *airplanes.Airplanes) error {
	return updatePlaneData(aircraft, planes)
}

func updatePlaneData(aircraft JSONAircraft, planes *airplanes.Airplanes) error {
	if planes == nil {
		return errNoAirplane
	}

	planes.Ensure(aircraft.Hex)
	plane, _ := planes.Get(aircraft.Hex)

	// Update timestamp
	plane.Update(airplane.WithLastUpdate(time.Now().UTC()))

	// Update callsign (trim whitespace from flight field)
	if aircraft.Flight != empty {
		plane.Update(airplane.WithCallsign(strings.TrimSpace(aircraft.Flight)))
	}

	// Update altitude (prefer barometric)
	if aircraft.BarometricAltitude != 0 {
		plane.Update(airplane.WithAltitude(float64(aircraft.BarometricAltitude)))
	} else if aircraft.GeometricAltitude != nil {
		plane.Update(airplane.WithAltitude(float64(*aircraft.GeometricAltitude)))
	}

	// Update velocity (ground speed in knots)
	if aircraft.GS != nil {
		plane.Update(airplane.WithVelocity(*aircraft.GS))
	}

	// Update heading (prefer true heading, fall back to track)
	switch { //nolint:revive
	case aircraft.TrueHeading != nil:
		plane.Update(airplane.WithHeading(*aircraft.TrueHeading))
	case aircraft.MagHeading != nil:
		plane.Update(airplane.WithHeading(*aircraft.MagHeading))
	case aircraft.Track != nil:
		plane.Update(airplane.WithHeading(*aircraft.Track))
	}

	// Update vertical rate (prefer barometric)
	if aircraft.BarometricVerticalRate != nil {
		plane.Update(airplane.WithVertRate(float64(*aircraft.BarometricVerticalRate)))
	} else if aircraft.GeometricVerticalRate != nil {
		plane.Update(airplane.WithVertRate(float64(*aircraft.GeometricVerticalRate)))
	}

	// Update position
	if aircraft.Lat != nil {
		plane.Update(airplane.WithLatitude(*aircraft.Lat))
	}

	if aircraft.Lon != nil {
		plane.Update(airplane.WithLongitude(*aircraft.Lon))
	}

	// Update squawk
	if aircraft.Squawk != empty {
		plane.Update(airplane.WithSquawk(aircraft.Squawk))
	}

	return nil
}
