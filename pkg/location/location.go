// Package location holds the receiver's thread-safe current position: a
// GPS fix from gpsd, or an ADS-B-derived self-locate fallback when GPS is
// unavailable. State is set through functional options and read back
// through small locked accessors.
package location

import (
	"fmt"
	"sync"
	"time"
)

const (
	defaultLatitude  = 0.0
	defaultLongitude = 0.0
	defaultAltitude  = 0.0
)

// Source identifies where a Location's coordinates came from so
// the UI can distinguish a precise GPS fix from a coarse ADS-B
// self-locate estimate. The zero value is SourceNone.
type Source int

const (
	// SourceNone marks an uninitialised location: no fix from any
	// source yet. It is the cold-start default.
	SourceNone Source = iota
	// SourceGPS marks coordinates delivered by gpsd — precise to a
	// handful of metres.
	SourceGPS
	// SourceInferred marks coordinates derived by the self-locate
	// fallback (horizon-circle intersection over ADS-B fixes),
	// accurate only to tens of nautical miles. Rendered as an
	// explicit estimate, never as GPS coordinates.
	SourceInferred
)

// Location represents the receiver's coordinates, mode, altitude
// and the provenance (Source) of the current fix.
type Location struct {
	latitude, longitude, altitude float64
	confidenceRadiusNm            float64
	lastUpdated                   time.Time
	mode                          fix
	source                        Source
	mu                            sync.RWMutex
}

// Option is a function that modifies a Location.
type Option func(*Location)

// New initializes a new Location.
func New(opts ...Option) *Location {
	location := &Location{
		latitude:    defaultLatitude,
		longitude:   defaultLongitude,
		altitude:    defaultAltitude,
		mode:        unknown,
		lastUpdated: time.Now(),
	}

	for _, opt := range opts {
		opt(location)
	}

	return location
}

// GetCoordinates returns the current latitude and longitude.
func (l *Location) GetCoordinates() (float64, float64) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return l.latitude, l.longitude
}

// Altitude returns the current altitude in metres.
func (l *Location) Altitude() float64 {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return l.altitude
}

// Mode returns the human-readable fix mode (e.g. "3D fix").
// Returns the empty string when no fix has been reported.
func (l *Location) Mode() string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return fixMode[l.mode]
}

// HasFix reports whether the receiver currently has a 2D or 3D
// GPS fix. Returns false for "no fix" and uninitialised state.
// Deliberately GPS-only: it gates gpsd-facing logic, so a
// SourceInferred estimate must not report a fix here.
func (l *Location) HasFix() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return l.mode == twoD || l.mode == threeD
}

// Source returns the provenance of the current coordinates.
func (l *Location) Source() Source {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return l.source
}

// ConfidenceRadiusNm returns the self-locate confidence radius in
// nautical miles, or 0 when it does not apply (a GPS fix or no fix
// at all).
func (l *Location) ConfidenceRadiusNm() float64 {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return l.confidenceRadiusNm
}

// Update applies the provided options to an existing Location.
func (l *Location) Update(opts ...Option) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for _, opt := range opts {
		opt(l)
	}

	l.lastUpdated = time.Now()
}

// String returns a self-describing, source-aware header line. A
// GPS (or not-yet-sourced) fix renders at full precision with its
// mode; a self-locate estimate renders at reduced precision with
// an explicit "inferred" qualifier so a tens-of-nm guess is never
// mistaken for GPS coordinates.
func (l *Location) String() string {
	l.mu.RLock()
	defer l.mu.RUnlock()

	if l.source == SourceInferred {
		return l.inferredString()
	}

	return l.gpsString()
}

// gpsString renders the GPS / no-source form at full precision.
// Caller must hold at least the read lock. An empty mode (cold
// start, gpsd mode 0) renders as "no fix" so the header reads as
// "GPS present, not yet locked" rather than trailing a blank.
func (l *Location) gpsString() string {
	mode := fixMode[l.mode]
	if mode == "" {
		mode = fixMode[noFix]
	}

	return fmt.Sprintf(
		"GPS lat %.9f, lon %.9f %.4fm %s",
		l.latitude, l.longitude, l.altitude, mode,
	)
}

// inferredString renders the self-locate form: reduced precision
// (two decimals ≈ 0.6 nm grid, honest against a ±5–30 nm
// estimate), no altitude (self-locate derives none), and the ±
// confidence radius when known. Caller must hold at least the read
// lock.
func (l *Location) inferredString() string {
	if l.confidenceRadiusNm == 0 {
		return fmt.Sprintf("EST lat %.2f, lon %.2f (inferred)", l.latitude, l.longitude)
	}

	return fmt.Sprintf(
		"EST lat %.2f, lon %.2f (inferred ±%.0f nm)",
		l.latitude, l.longitude, l.confidenceRadiusNm,
	)
}

// WithLatitude sets the latitude of the location.
func WithLatitude(lat float64) Option {
	return func(l *Location) {
		l.latitude = lat
	}
}

// WithLongitude sets the longitude of the location.
func WithLongitude(lon float64) Option {
	return func(l *Location) {
		l.longitude = lon
	}
}

// WithAltitude sets the altitude of the location.
func WithAltitude(alt float64) Option {
	return func(l *Location) {
		l.altitude = alt
	}
}

// WithMode sets the mode of the location.
func WithMode(m int) Option {
	return func(l *Location) {
		l.mode = fix(m)
	}
}

// WithSource sets the provenance of the location's coordinates.
func WithSource(source Source) Option {
	return func(l *Location) {
		l.source = source
	}
}

// WithConfidenceRadiusNm sets the self-locate confidence radius in
// nautical miles. Pass 0 when it does not apply (a GPS fix clears
// any stale inferred radius this way).
func WithConfidenceRadiusNm(radiusNm float64) Option {
	return func(l *Location) {
		l.confidenceRadiusNm = radiusNm
	}
}
