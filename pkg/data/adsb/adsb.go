package adsb

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplane"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplanes"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/service"
)

const JSONServiceAddress = "127.0.0.1:30047"
const ServiceName = "readsb.service"
const connectionTimeout = 30 * time.Second
const retryInterval = 1 * time.Second
const maxRetries = 30
const pruneThreshold = 1 * time.Minute

// JSONAircraft represents the ADSB structure from readsb
type JSONAircraft struct {
	Hex         string   `json:"hex"`          // ICAO hex code
	Type        string   `json:"type"`         // Message type
	Flight      string   `json:"flight"`       // Callsign
	AltBaro     *int     `json:"alt_baro"`     // Barometric altitude in feet
	AltGeom     *int     `json:"alt_geom"`     // Geometric altitude in feet
	GS          *float64 `json:"gs"`           // Ground speed in knots
	TAS         *float64 `json:"tas"`          // True airspeed in knots
	IAS         *int     `json:"ias"`          // Indicated airspeed
	Track       *float64 `json:"track"`        // Track/heading in degrees
	TrackRate   *float64 `json:"track_rate"`   // Rate of change of track
	Roll        *float64 `json:"roll"`         // Roll angle
	MagHeading  *float64 `json:"mag_heading"`  // Magnetic heading
	TrueHeading *float64 `json:"true_heading"` // True heading
	BaroRate    *int     `json:"baro_rate"`    // Vertical rate in ft/min
	GeomRate    *int     `json:"geom_rate"`    // Geometric vertical rate
	Squawk      string   `json:"squawk"`       // Squawk code
	Emergency   string   `json:"emergency"`    // Emergency status
	Category    string   `json:"category"`     // Aircraft category
	Lat         *float64 `json:"lat"`          // Latitude
	Lon         *float64 `json:"lon"`          // Longitude
	NIC         *int     `json:"nic"`          // Navigation Integrity Category
	RC          *int     `json:"rc"`           // Radius of Containment
	SeenPos     *float64 `json:"seen_pos"`     // Seconds since last position
	Version     *int     `json:"version"`      // ADS-B version
	NICBaro     *int     `json:"nic_baro"`     // NIC supplement for barometric altitude
	NACp        *int     `json:"nac_p"`        // Navigation Accuracy Category - Position
	NACv        *int     `json:"nac_v"`        // Navigation Accuracy Category - Velocity
	SIL         *int     `json:"sil"`          // Source Integrity Level
	SILType     string   `json:"sil_type"`     // Type of SIL
	GVA         *int     `json:"gva"`          // Geometric Vertical Accuracy
	SDA         *int     `json:"sda"`          // System Design Assurance
	Alert       *int     `json:"alert"`        // Flight status alert bit
	SPI         *int     `json:"spi"`          // Flight status SPI bit
	Messages    int      `json:"messages"`     // Total message count
	Seen        float64  `json:"seen"`         // Seconds since last message
	RSSI        *float64 `json:"rssi"`         // Signal strength in dBFS
	Dst         *float64 `json:"dst"`          // Distance to receiver (km)
	Dir         *float64 `json:"dir"`          // Direction to receiver (degrees)
	Now         *float64 `json:"now"`          // Unix timestamp
}

type ADSB struct {
	connection net.Conn
	airplanes  *airplanes.Airplanes
}

func New() *ADSB {
	return &ADSB{
		airplanes: airplanes.New(),
	}
}

func (a *ADSB) Stop(ctx context.Context) error {
	if a != nil && a.connection != nil {
		_ = a.connection.Close()
	}

	//srv := service.New(ServiceName, service.WithStop())
	//err := srv.Execute(ctx)
	//if err != nil {
	//	return err
	//}
	return nil
}

func (a *ADSB) Connect(ctx context.Context) error {
	srv := service.New(ServiceName, service.WithEnable(), service.WithStart())
	err := srv.Execute(ctx)
	if err != nil {
		return errors.Join(err, errors.New("could not start service ("+ServiceName+")"))
	}

	deadline := time.Now().Add(connectionTimeout)
	retries := 0

	for time.Now().Before(deadline) && retries < maxRetries {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		a.connection, err = net.DialTimeout("tcp", JSONServiceAddress, retryInterval)
		if err == nil {
			return nil
		}

		retries++
		if retries < maxRetries && time.Now().Before(deadline) {
			time.Sleep(retryInterval)
		}
	}

	if err != nil {
		return errors.Join(err, errors.New("could not dial to address ("+JSONServiceAddress+")"))
	}
	return nil
}

func (a *ADSB) Planes() *airplanes.Airplanes {
	return a.airplanes
}

func (a *ADSB) Stream(ctx context.Context, errChan chan error) {
	scanner := bufio.NewScanner(a.connection)

	// Set a larger buffer size for potentially large JSON messages
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			if !scanner.Scan() {
				if err := scanner.Err(); err != nil {
					errChan <- errors.Join(err, errors.New("error reading from JSON stream"))
				}
				// Connection closed or error - exit
				return
			}

			line := scanner.Text()
			if line == "" {
				continue
			}

			// Each line is a single aircraft update
			var ac JSONAircraft
			if err := json.Unmarshal([]byte(line), &ac); err != nil {
				errChan <- errors.Join(err, fmt.Errorf("could not parse JSON: %s", line[:min(50, len(line))]))
				continue
			}

			// Process the aircraft
			if err := a.processAircraft(ac); err != nil {
				errChan <- err
			}

			// Prune old planes periodically (not on every message for performance)
			a.airplanes.Prune(pruneThreshold)
		}
	}
}

func (a *ADSB) processAircraft(ac JSONAircraft) error {
	// Parse ICAO hex to uint64
	var icaoInt uint64
	if _, err := fmt.Sscanf(ac.Hex, "%x", &icaoInt); err != nil {
		return fmt.Errorf("invalid ICAO hex '%s': %w", ac.Hex, err)
	}

	i := airplane.ICAO(icaoInt)
	a.airplanes.Ensure(i)

	plane, ok := a.airplanes.Get(i)
	if !ok {
		return fmt.Errorf("failed to get airplane after ensuring: %s", ac.Hex)
	}

	// Update timestamp
	plane.Update(airplane.WithLastUpdate(time.Now().UTC()))

	// Update callsign (trim whitespace from flight field)
	if ac.Flight != "" {
		plane.Update(airplane.WithCallsign(strings.TrimSpace(ac.Flight)))
	}

	// Update altitude (prefer barometric)
	if ac.AltBaro != nil {
		plane.Update(airplane.WithAltitude(int64(*ac.AltBaro)))
	} else if ac.AltGeom != nil {
		plane.Update(airplane.WithAltitude(int64(*ac.AltGeom)))
	}

	// Update velocity (ground speed in knots)
	if ac.GS != nil {
		plane.Update(airplane.WithVelocity(*ac.GS))
	}

	// Update heading (prefer true heading, fall back to track)
	if ac.TrueHeading != nil {
		plane.Update(airplane.WithHeading(*ac.TrueHeading))
	} else if ac.MagHeading != nil {
		plane.Update(airplane.WithHeading(*ac.MagHeading))
	} else if ac.Track != nil {
		plane.Update(airplane.WithHeading(*ac.Track))
	}

	// Update vertical rate (prefer barometric)
	if ac.BaroRate != nil {
		plane.Update(airplane.WithVertRate(float64(*ac.BaroRate)))
	} else if ac.GeomRate != nil {
		plane.Update(airplane.WithVertRate(float64(*ac.GeomRate)))
	}

	// Update position
	if ac.Lat != nil {
		plane.Update(airplane.WithLatitude(*ac.Lat))
	}
	if ac.Lon != nil {
		plane.Update(airplane.WithLongitude(*ac.Lon))
	}

	// Update squawk
	if ac.Squawk != "" {
		plane.Update(airplane.WithSquawk([]byte(ac.Squawk)))
	}

	// Update signal strength (convert RSSI dBFS to uint8)
	if ac.RSSI != nil {
		// RSSI is typically negative dBFS, convert to positive scale
		// -3 dBFS is excellent, -50 dBFS is poor
		// Map to 0-255 range roughly
		signal := uint8(max(0, min(255, int((*ac.RSSI+50)*5))))
		plane.Update(airplane.WithSignal(signal))
	}

	return nil
}
