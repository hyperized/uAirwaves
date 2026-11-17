package airplane

import (
	"math"
	"time"
)

// Source says how a reported value reached the receiver, which decides how
// far it can be trusted. The zero value is the untrusted one, so a caller
// that forgets to set it gets the cautious behaviour.
type Source uint8

const (
	// SourceAddressParity is a reply whose address is overlaid on its
	// parity: DF 0, 4, 5, 16, 20 and 21. The receiver gets the address back
	// as the CRC residual, so it cannot tell a real reply from noise that
	// happens to leave the residual of an aircraft it already tracks, and
	// every field in such a frame may be random.
	SourceAddressParity Source = iota

	// SourceExtendedSquitter is a DF 17 or DF 18 extended squitter. The
	// address is sent in clear and the CRC must come out at zero, so a
	// frame that passes is the aircraft's own.
	SourceExtendedSquitter
)

const (
	// maxVerticalRateFpm bounds how fast an address-parity altitude may
	// move away from the current one and still be taken on its own.
	// Airliners sustain up to about 6,000 ft/min; the fastest change between
	// extended squitters in the 20-minute capture behind this rule was
	// 4,764 ft/min. Faster aircraft are not lost: their extended squitters
	// are always accepted, and two replies that agree under
	// confirmVerticalRateFpm confirm each other.
	maxVerticalRateFpm = 6000

	// confirmVerticalRateFpm is the wider rate two address-parity replies
	// may imply between each other and still confirm each other. It is the
	// ceiling readsb applies when it knows no vertical rate, and covers an
	// emergency descent. A second reply is independent evidence, which is
	// why it may be judged more loosely than a single one; replies from
	// one radar dwell arrive milliseconds apart and agree at any rate.
	confirmVerticalRateFpm = 12500

	// altitudeSlackFt is added to the rate envelope so two reports taken a
	// moment apart, or quantised to different 25 ft steps, never disagree.
	// It is readsb's figure for the change it accepts without question.
	altitudeSlackFt = 300

	// altitudeStaleAfter is how old the current altitude may be and still
	// vouch for a new address-parity one. Past it the rate envelope grows
	// wide enough to admit noise, so an old value judges nothing and a
	// new one has to be confirmed by a second reply. Fifteen seconds is
	// the stale interval dump1090-fa and readsb give barometric altitude.
	altitudeStaleAfter = 15 * time.Second

	// squitterPriority is how long an extended-squitter altitude keeps
	// address-parity replies from overruling it by confirmation. While
	// the aircraft's own CRC-checked altitude is this fresh, two agreeing
	// phantoms cannot replace it; they can only land inside the envelope.
	squitterPriority = 15 * time.Second

	// altitudeConfirmWindow is how long a held address-parity altitude
	// waits for a second reply to agree with it. A radar dwell brings
	// several replies within milliseconds and the next sweep comes within
	// four to twelve seconds; longer windows only give two random values
	// more time to land near each other.
	altitudeConfirmWindow = 10 * time.Second

	// identityConfirmWindow is the same for a squawk or a callsign. Those
	// must match exactly, which noise does once in thousands of tries, so
	// the window can cover a slow en-route radar's next sweep with room
	// to spare.
	identityConfirmWindow = 30 * time.Second
)

// altitudeTrust is what an Airplane remembers to judge the next
// address-parity altitude. It lives inside the Airplane, so it is guarded by
// the same lock and released with it when the aircraft is pruned.
type altitudeTrust struct {
	acceptedAt    time.Time
	squitterAt    time.Time
	candidateAt   time.Time
	candidate     float64
	haveAccepted  bool
	haveSquitter  bool
	haveCandidate bool
}

// heldValue is an address-parity squawk or callsign waiting for a second
// reply that says the same.
type heldValue struct {
	heardAt time.Time
	value   string
	have    bool
}

// confirmedBy reports whether value, reported at, matches the held one
// within identityConfirmWindow.
func (h heldValue) confirmedBy(value string, heardAt time.Time) bool {
	return h.have && h.value == value && elapsed(h.heardAt, heardAt) <= identityConfirmWindow
}

// elapsed is the time from earlier to later, never negative. Frames can
// arrive slightly out of order across a reconnect, and a negative gap would
// shrink the envelope below the slack.
func elapsed(earlier, later time.Time) time.Duration {
	return max(later.Sub(earlier), 0)
}

// altitudeEnvelope is how far an altitude may move in gap at rateFpm.
func altitudeEnvelope(gap time.Duration, rateFpm float64) float64 {
	return altitudeSlackFt + rateFpm*gap.Minutes()
}

// WithReportedAltitude returns an option that offers a decoded barometric
// altitude, in feet, reported at the given time. The time should come from
// the frame, not the wall clock, so a replay is judged the way it was heard.
//
// An extended-squitter altitude is always applied. An address-parity
// altitude is applied when it lies within the rate envelope of an altitude
// accepted less than fifteen seconds earlier, or when it agrees with an
// earlier address-parity altitude held within the last ten seconds and the
// aircraft has sent no extended-squitter altitude in the last fifteen.
// Otherwise it is held as the candidate a later reply can confirm, and the
// altitude the aircraft shows does not change. The aircraft's first
// altitude from address-parity replies alone therefore takes two of them.
func WithReportedAltitude(feet float64, src Source, heardAt time.Time) Option {
	return func(plane *Airplane) {
		if src == SourceExtendedSquitter {
			plane.acceptAltitude(feet, heardAt)
			plane.altTrust.squitterAt, plane.altTrust.haveSquitter = heardAt, true

			return
		}

		if plane.plausibleAltitude(feet, heardAt) || plane.confirmsAltitude(feet, heardAt) {
			plane.acceptAltitude(feet, heardAt)

			return
		}

		plane.altTrust.candidate = feet
		plane.altTrust.candidateAt = heardAt
		plane.altTrust.haveCandidate = true
	}
}

// acceptAltitude applies feet and forgets any held candidate, which the
// accepted value has either confirmed or superseded.
func (a *Airplane) acceptAltitude(feet float64, heardAt time.Time) {
	a.altitude = feet
	a.altitudeAt = heardAt
	a.altTrust.acceptedAt = heardAt
	a.altTrust.haveAccepted = true
	a.altTrust.haveCandidate = false
}

// plausibleAltitude reports whether feet is within reach of the current
// altitude, given how long ago that was accepted.
func (a *Airplane) plausibleAltitude(feet float64, heardAt time.Time) bool {
	if !a.altTrust.haveAccepted {
		return false
	}

	gap := elapsed(a.altTrust.acceptedAt, heardAt)

	return gap <= altitudeStaleAfter && math.Abs(feet-a.altitude) <= altitudeEnvelope(gap, maxVerticalRateFpm)
}

// confirmsAltitude reports whether feet agrees with the held candidate
// closely enough, and soon enough, to count as a second sighting.
func (a *Airplane) confirmsAltitude(feet float64, heardAt time.Time) bool {
	trust := &a.altTrust
	if trust.haveSquitter && elapsed(trust.squitterAt, heardAt) <= squitterPriority {
		return false
	}

	if !trust.haveCandidate {
		return false
	}

	gap := elapsed(trust.candidateAt, heardAt)

	return gap <= altitudeConfirmWindow &&
		math.Abs(feet-trust.candidate) <= altitudeEnvelope(gap, confirmVerticalRateFpm)
}

// WithReportedSquawk returns an option that offers a Mode A code reported at
// the given time. An extended-squitter squawk is applied at once. An
// address-parity squawk is applied when it repeats the current one or when a
// second address-parity reply within thirty seconds carries the same code;
// otherwise it is held. That includes the emergency codes: a single noise
// frame must not declare an emergency. An empty squawk is ignored.
func WithReportedSquawk(squawk string, src Source, heardAt time.Time) Option {
	return func(plane *Airplane) {
		if squawk != "" && settle(plane.squawk, &plane.heldSquawk, squawk, src, heardAt) {
			plane.setSquawk(squawk)
		}
	}
}

// WithReportedCallsign returns an option that offers a callsign reported at
// the given time, under the same rule as WithReportedSquawk: an extended
// squitter's identification is applied at once, while a callsign decoded
// from a Comm-B reply needs a second reply that agrees unless it repeats the
// current one. An empty callsign is ignored.
func WithReportedCallsign(callsign string, src Source, heardAt time.Time) Option {
	return func(plane *Airplane) {
		if callsign != "" && settle(plane.callsign, &plane.heldCallsign, callsign, src, heardAt) {
			plane.callsign = callsign
		}
	}
}

// settle reports whether an identity value should replace current, and
// updates held to match: cleared when the value goes through, holding it
// otherwise.
func settle(current string, held *heldValue, value string, src Source, heardAt time.Time) bool {
	if src == SourceExtendedSquitter || value == current || held.confirmedBy(value, heardAt) {
		*held = heldValue{}

		return true
	}

	*held = heldValue{heardAt: heardAt, value: value, have: true}

	return false
}
