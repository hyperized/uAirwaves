package airplane

import (
	"fmt"
	"sync"
	"time"
)

type ICAO uint64

type Option func(*Airplane)

type Airplane struct {
	icao                                                       ICAO
	callsign                                                   string
	altitude, heading, velocity, latitude, longitude, vertRate float64
	signal                                                     uint8
	lastUpdate                                                 time.Time
	squawk                                                     []byte
	messageCount                                               int64
	mu                                                         sync.RWMutex
}

func New(icao ICAO) *Airplane {
	return &Airplane{
		icao:         icao,
		messageCount: 1,
		lastUpdate:   time.Now(),
		heading:      -1,
		velocity:     -1,
		vertRate:     0,
	}
}

// String returns a formatted string representation of the plane
func (a *Airplane) String() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return fmt.Sprintf("%.0fft h%.0f(%s) %.0fkts %.0ffpm lu%s", a.altitude, a.heading, getHeadingArrow(a.heading), a.velocity, a.vertRate, a.lastUpdate.UTC().Format(time.TimeOnly))
}

// Snapshot holds a point-in-time copy of airplane data for lock-free reads
type Snapshot struct {
	ICAO         string
	Callsign     string
	Altitude     float64
	Heading      float64
	Velocity     float64
	VertRate     float64
	Latitude     float64
	Longitude    float64
	Signal       uint8
	LastUpdate   time.Time
	Squawk       []byte
	MessageCount int64
}

// GetSnapshot returns all fields in a single lock acquisition
func (a *Airplane) GetSnapshot() Snapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return Snapshot{
		ICAO:         fmt.Sprintf("%06X", a.icao),
		Callsign:     a.callsign,
		Altitude:     a.altitude,
		Heading:      a.heading,
		Velocity:     a.velocity,
		VertRate:     a.vertRate,
		Latitude:     a.latitude,
		Longitude:    a.longitude,
		Signal:       a.signal,
		LastUpdate:   a.lastUpdate,
		Squawk:       a.squawk,
		MessageCount: a.messageCount,
	}
}

func (a *Airplane) GetLastUpdate() time.Time {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastUpdate
}

func (a *Airplane) GetICAO() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return fmt.Sprintf("%06X", a.icao)
}

func (a *Airplane) GetICAORaw() ICAO {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.icao
}

func (a *Airplane) GetCallsign() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.callsign
}

func (a *Airplane) GetLatitude() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.latitude
}

func (a *Airplane) GetLongitude() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.longitude
}

func (a *Airplane) GetAltitude() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.altitude
}

func (a *Airplane) GetHeading() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.heading
}

func (a *Airplane) GetVelocity() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.velocity
}

func (a *Airplane) GetVertRate() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.vertRate
}

func (a *Airplane) GetSquawk() []byte {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.squawk
}

func (a *Airplane) GetSignal() uint8 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.signal
}

func (a *Airplane) GetMessageCount() int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.messageCount
}

func (a *Airplane) Update(opts ...Option) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messageCount++
	for _, opt := range opts {
		opt(a)
	}
}

// WithCallsign returns an option to update the callsign
func WithCallsign(c string) Option {
	return func(a *Airplane) {
		if c != "" {
			a.callsign = c
		}
	}
}

// WithSquawk returns an option to update the squawk
func WithSquawk(s []byte) Option {
	return func(a *Airplane) {
		if s != nil {
			a.squawk = s
		}
	}
}

// WithLastUpdate returns an option to update the timestamp
func WithLastUpdate(t time.Time) Option {
	return func(a *Airplane) {
		if !t.IsZero() {
			a.lastUpdate = t
		}
	}
}

func WithAltitude(altitude int64) Option {
	return func(a *Airplane) {
		if altitude > 0 {
			a.altitude = float64(altitude)
		}
	}
}

// WithLatitude clamps latitude to valid range [-90, 90]
func WithLatitude(latitude float64) Option {
	return func(a *Airplane) {
		a.latitude = max(min(latitude, 90), -90)
	}
}

// WithLongitude clamps longitude to valid range [-180, 180]
func WithLongitude(longitude float64) Option {
	return func(a *Airplane) {
		a.longitude = max(min(longitude, 180), -180)
	}
}

func WithVelocity(v float64) Option {
	return func(a *Airplane) {
		if v > 0 {
			a.velocity = v
		}
	}
}

func WithHeading(heading float64) Option {
	return func(a *Airplane) {
		a.heading = max(min(heading, 360), 0)
	}
}

func WithVertRate(vertRate float64) Option {
	return func(a *Airplane) {
		// Vertical rate can be negative (descending)
		a.vertRate = vertRate
	}
}

func WithSignal(signal uint8) Option {
	return func(a *Airplane) {
		if signal > 0 {
			a.signal = signal
		}
	}
}

// getHeadingArrow returns an ASCII arrow character based on heading
// Uses 8 cardinal/intercardinal directions with simple ASCII characters
func getHeadingArrow(heading float64) string {
	if heading < 0 || heading > 360 {
		return "?" // Invalid heading
	}

	// Normalize heading to 0-360 and determine direction
	// Each segment is 45 degrees (360/8)
	switch {
	case heading >= 337.5 || heading < 22.5:
		return "^" // North
	case heading >= 22.5 && heading < 67.5:
		return "/" // Northeast
	case heading >= 67.5 && heading < 112.5:
		return ">" // East
	case heading >= 112.5 && heading < 157.5:
		return "\\" // Southeast
	case heading >= 157.5 && heading < 202.5:
		return "v" // South
	case heading >= 202.5 && heading < 247.5:
		return "/" // Southwest
	case heading >= 247.5 && heading < 292.5:
		return "<" // West
	case heading >= 292.5 && heading < 337.5:
		return "\\" // Northwest
	default:
		return "?"
	}
}
