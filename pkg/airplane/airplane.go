package airplane

import (
	"fmt"
	"sync"
	"time"
)

const (
	minLatitude  = -90
	maxLatitude  = 90
	minLongitude = -180
	maxLongitude = 180
	minHeading   = 0
	maxHeading   = 360
	minVelocity  = 0

	defaultMessageCount    = 1
	defaultHeading         = -1
	defaultVelocity        = -1
	defaultVertRate        = 0
	maxPositionHistory     = 10
	positionHistoryInterval = 10 * time.Second

	squawkHijacking        = "7500"
	squawkRadioFailure     = "7600"
	squawkGeneralEmergency = "7700"
)

// PositionEntry holds a single historical lat/lon fix.
type PositionEntry struct {
	Latitude, Longitude float64
}

// Option is a function that modifies an Airplane.
type Option func(*Airplane)

// Airplane represents a single airplane.
type Airplane struct {
	icao, callsign, squawk                                     string
	altitude, heading, velocity, latitude, longitude, vertRate float64
	lastUpdate                                                 time.Time
	lastPositionTime                                           time.Time
	emergency                                                  bool
	messageCount                                               int64
	positionHistory                                            []PositionEntry
	mu                                                         sync.RWMutex
}

// New initializes a new Airplane.
func New(icao string) *Airplane {
	return &Airplane{
		icao:         icao,
		messageCount: defaultMessageCount,
		lastUpdate:   time.Now(),
		heading:      defaultHeading,
		velocity:     defaultVelocity,
		vertRate:     defaultVertRate,
	}
}

// String returns a formatted string representation of the plane.
func (a *Airplane) String() string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return fmt.Sprintf("%.0fft %.0fo %.0fkts %.0ffpm %.0fs",
		a.altitude,
		a.heading,
		a.velocity,
		a.vertRate,
		time.Since(a.lastUpdate.UTC()).Seconds(),
	)
}

// Snapshot holds a point-in-time copy of airplane data for lock-free reads.
type Snapshot struct {
	ICAO            string
	Callsign        string
	Altitude        float64
	Heading         float64
	Velocity        float64
	VertRate        float64
	Latitude        float64
	Longitude       float64
	LastUpdate      time.Time
	Squawk          string
	Emergency       bool
	MessageCount    int64
	PositionHistory []PositionEntry
}

// GetSnapshot returns all fields in a single lock acquisition.
func (a *Airplane) GetSnapshot() Snapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()

	history := make([]PositionEntry, len(a.positionHistory))
	copy(history, a.positionHistory)

	return Snapshot{
		ICAO:            a.icao,
		Callsign:        a.callsign,
		Altitude:        a.altitude,
		Heading:         a.heading,
		Velocity:        a.velocity,
		VertRate:        a.vertRate,
		Latitude:        a.latitude,
		Longitude:       a.longitude,
		LastUpdate:      a.lastUpdate,
		Squawk:          a.squawk,
		Emergency:       a.emergency,
		MessageCount:    a.messageCount,
		PositionHistory: history,
	}
}

// GetLastUpdate returns the last time the plane was updated.
func (a *Airplane) GetLastUpdate() time.Time {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.lastUpdate
}

// GetICAO returns the ICAO code of the plane.
func (a *Airplane) GetICAO() string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.icao
}

// GetCallsign returns the callsign of the plane.
func (a *Airplane) GetCallsign() string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.callsign
}

// GetLatitude returns the latitude of the plane.
func (a *Airplane) GetLatitude() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.latitude
}

// GetLongitude returns the longitude of the plane.
func (a *Airplane) GetLongitude() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.longitude
}

// GetAltitude returns the altitude of the plane.
func (a *Airplane) GetAltitude() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.altitude
}

// GetHeading returns the heading of the plane.
func (a *Airplane) GetHeading() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.heading
}

// GetVelocity returns the velocity of the plane.
func (a *Airplane) GetVelocity() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.velocity
}

// GetVertRate returns the vertical rate of the plane.
func (a *Airplane) GetVertRate() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.vertRate
}

// GetSquawk returns the squawk code of the plane.
func (a *Airplane) GetSquawk() string {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.squawk
}

// GetMessageCount returns the number of messages received from the plane.
func (a *Airplane) GetMessageCount() int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.messageCount
}

// Update updates the airplane with new data.
func (a *Airplane) Update(opts ...Option) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.messageCount++

	for _, opt := range opts {
		opt(a)
	}
}

// WithCallsign returns an option to update the callsign.
func WithCallsign(c string) Option {
	return func(a *Airplane) {
		if c != "" {
			a.callsign = c
		}
	}
}

// WithSquawk returns an option to update the squawk.
func WithSquawk(squawk string) Option {
	return func(airplane *Airplane) {
		if squawk != "" {
			airplane.squawk = squawk
			airplane.emergency = squawk == squawkHijacking || squawk == squawkRadioFailure || squawk == squawkGeneralEmergency
		}
	}
}

// WithLastUpdate returns an option to update the timestamp.
func WithLastUpdate(t time.Time) Option {
	return func(a *Airplane) {
		if !t.IsZero() {
			a.lastUpdate = t
		}
	}
}

// WithAltitude returns an option to update the altitude.
func WithAltitude(altitude float64) Option {
	return func(a *Airplane) {
		a.altitude = altitude
	}
}

// WithLatitude clamps latitude to valid range [-90, 90].
func WithLatitude(latitude float64) Option {
	return func(a *Airplane) {
		a.latitude = max(min(latitude, maxLatitude), minLatitude)
	}
}

// WithLongitude clamps longitude to valid range [-180, 180].
func WithLongitude(longitude float64) Option {
	return func(a *Airplane) {
		a.longitude = max(min(longitude, maxLongitude), minLongitude)
	}
}

// WithVelocity returns an option to update the velocity.
func WithVelocity(v float64) Option {
	return func(a *Airplane) {
		if v > minVelocity {
			a.velocity = v
		}
	}
}

// WithHeading clamps heading to valid range [0, 360].
func WithHeading(heading float64) Option {
	return func(a *Airplane) {
		a.heading = max(min(heading, maxHeading), minHeading)
	}
}

// WithVertRate returns an option to update the vertical rate.
func WithVertRate(vertRate float64) Option {
	return func(a *Airplane) {
		a.vertRate = vertRate
	}
}

// WithPosition updates both latitude and longitude and appends the fix to position history
// at most once per positionHistoryInterval to avoid filling history with near-identical entries.
func WithPosition(latitude, longitude float64) Option {
	return func(a *Airplane) {
		a.latitude = max(min(latitude, maxLatitude), minLatitude)
		a.longitude = max(min(longitude, maxLongitude), minLongitude)

		if time.Since(a.lastPositionTime) < positionHistoryInterval {
			return
		}

		a.lastPositionTime = time.Now()

		entry := PositionEntry{Latitude: a.latitude, Longitude: a.longitude}
		a.positionHistory = append(a.positionHistory, entry)

		if len(a.positionHistory) > maxPositionHistory {
			a.positionHistory = a.positionHistory[len(a.positionHistory)-maxPositionHistory:]
		}
	}
}
