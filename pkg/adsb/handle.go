package adsb

import (
	"fmt"
	"strings"
	"time"

	"github.com/hyperized/demod1090/demod"
	"github.com/hyperized/modes"
	"github.com/hyperized/uAirwaves/pkg/airplane"
	"github.com/hyperized/uAirwaves/pkg/airplanes"
)

// validCallsign reports whether s looks like a real Mode S
// identification. The modes decoder maps unassigned 6-bit values
// to '#' and pads short callsigns with spaces (stripped before
// it returns). Two failure modes for noise frames:
//
//   - Random 6-bit values produce a few '#' chars per 8-char
//     field; pure noise typically lands at ≥2 '#'.
//   - Random values can also resolve to "space" (6-bit 32),
//     which trim eats — leaving 1- or 2-char strings that look
//     valid character-wise but are too short to be a real ID.
//
// Real ICAO identifications run 3-8 chars (DO-260B §2.2.3.2.5
// flight-ID range; 7 visible chars + alphabet padding). We
// require at least 3 chars and at most 1 '#' so partially-
// corrupted real callsigns still register, while both noise
// failure modes get rejected.
func validCallsign(callsign string) bool {
	const (
		minChars        = 3
		maxPlaceholders = 1
	)

	return len(callsign) >= minChars && strings.Count(callsign, "#") <= maxPlaceholders
}

// handleFrame routes a freshly-demodulated frame through the
// modes decoder and folds the result into the live airplanes
// list. ICAO classification and the address-parity phantom gate
// are delegated to icaofilter.Filter.Admit — see the package doc
// over there for the why.
func (a *ADSB) handleFrame(frame demod.Frame, planes *airplanes.Airplanes) {
	a.totalFrames.Add(1)

	if frame.Errors > 0 {
		a.recoveredFrames.Add(1)
	}

	mFrame := modes.Frame(frame.Bytes)

	icao, admitted := a.icaoFilter.Admit(mFrame, frame.CRC, frame.WallTime)
	if !admitted {
		return
	}

	icaoStr := fmt.Sprintf("%06X", uint32(icao))

	// Only a frame that carries its address in clear may start a track. An
	// address-parity frame can be noise that recovers a trusted address, and
	// the filter's trust window and the prune threshold are separate clocks,
	// so trust alone does not prove the aircraft is still in the list.
	if announcesAddress(mFrame.DF()) {
		planes.Ensure(icaoStr)
	}

	plane, found := planes.Get(icaoStr)
	if !found {
		return
	}

	a.applyFrame(plane, mFrame, icao, frame)
}

// announcesAddress reports whether df sends the aircraft address in clear
// under a CRC that must come out at zero: the all-call reply and the two
// extended squitters. Everything else icaofilter admits is address-parity.
//
//nolint:exhaustive // every other DF is address-parity or rejected upstream.
func announcesAddress(df modes.DownlinkFormat) bool {
	switch df {
	case modes.DFAllCallReply, modes.DFExtendedSquitter, modes.DFNonTransponderES:
		return true
	default:
		return false
	}
}

// applyFrame folds a single frame into the per-airplane state.
// Per-DF dispatch keeps the spec-specific bits in modes; this
// file only knows how to translate a typed message into
// airplane.Update options.
//
// Each apply* helper returns its options instead of calling
// plane.Update directly; applyFrame issues a single Update per
// frame so messageCount tracks frames 1:1 (not the previous
// shape, where one frame could bump the counter up to four times
// via separate altitude / position / velocity / callsign Updates
// — inflating the per-frame display and forcing four write-lock
// pairs on the hot path).
//
//nolint:exhaustive // DFs without a per-DF aggregator (DF 0/11/16/19/24-31) are message-count-only.
func (a *ADSB) applyFrame(plane *airplane.Airplane, frame modes.Frame, icao modes.ICAO, src demod.Frame) {
	opts := []airplane.Option{airplane.WithLastUpdate(src.WallTime)}

	heardAt := src.WallTime

	switch frame.DF() {
	case modes.DFSurveillanceAlt:
		opts = append(opts, applySurveillanceAltitude(frame, icao, heardAt)...)
	case modes.DFSurveillanceID:
		opts = append(opts, applySurveillanceIdentity(frame, icao, heardAt)...)
	case modes.DFExtendedSquitter, modes.DFNonTransponderES:
		opts = append(opts, a.applyExtendedSquitter(plane, frame, icao, heardAt)...)
	case modes.DFCommBAltitude:
		opts = append(opts, applyCommBAltitude(frame, icao, heardAt)...)
	case modes.DFCommBIdentity:
		opts = append(opts, applyCommBIdentity(frame, icao, heardAt)...)
	default:
		// DF 0/11/16/19/24-31: message-count-only.
	}

	plane.Update(opts...)
}

// The four address-parity helpers below offer what they decode as
// airplane.SourceAddressParity: the frame may be noise that recovered a
// tracked address, so pkg/airplane holds a value until it is plausible or
// confirmed. See WithReportedAltitude and the README's "Phantom suppression".

func applySurveillanceAltitude(frame modes.Frame, icao modes.ICAO, heardAt time.Time) []airplane.Option {
	reply, err := modes.DecodeSurveillanceAltitude(frame, icao)
	if err != nil || reply.AltitudeError != nil {
		return nil
	}

	return []airplane.Option{
		airplane.WithReportedAltitude(float64(reply.AltitudeFeet), airplane.SourceAddressParity, heardAt),
	}
}

func applySurveillanceIdentity(frame modes.Frame, icao modes.ICAO, heardAt time.Time) []airplane.Option {
	reply, err := modes.DecodeSurveillanceIdentity(frame, icao)
	if err != nil {
		return nil
	}

	return []airplane.Option{
		airplane.WithReportedSquawk(fmt.Sprintf("%04d", reply.Squawk), airplane.SourceAddressParity, heardAt),
	}
}

func applyCommBAltitude(frame modes.Frame, icao modes.ICAO, heardAt time.Time) []airplane.Option {
	reply, err := modes.DecodeCommBAltitude(frame, icao)
	if err != nil {
		return nil
	}

	opts := make([]airplane.Option, 0, 2) //nolint:mnd // up to two options: altitude + callsign.

	if reply.AltitudeError == nil {
		opts = append(opts,
			airplane.WithReportedAltitude(float64(reply.AltitudeFeet), airplane.SourceAddressParity, heardAt))
	}

	if callsign, ok := modes.DecodeBDS20Callsign(reply.MB); ok && validCallsign(callsign) {
		opts = append(opts, airplane.WithReportedCallsign(callsign, airplane.SourceAddressParity, heardAt))
	}

	return opts
}

func applyCommBIdentity(frame modes.Frame, icao modes.ICAO, heardAt time.Time) []airplane.Option {
	reply, err := modes.DecodeCommBIdentity(frame, icao)
	if err != nil {
		return nil
	}

	opts := make([]airplane.Option, 0, 2) //nolint:mnd // up to two options: squawk + callsign.
	opts = append(opts,
		airplane.WithReportedSquawk(fmt.Sprintf("%04d", reply.Squawk), airplane.SourceAddressParity, heardAt))

	if callsign, ok := modes.DecodeBDS20Callsign(reply.MB); ok && validCallsign(callsign) {
		opts = append(opts, airplane.WithReportedCallsign(callsign, airplane.SourceAddressParity, heardAt))
	}

	return opts
}

// applyExtendedSquitter dispatches the ME field through modes
// and returns the airplane Update options for the typed message.
func (a *ADSB) applyExtendedSquitter(
	plane *airplane.Airplane, frame modes.Frame, icao modes.ICAO, heardAt time.Time,
) []airplane.Option {
	squitter, err := modes.DecodeExtendedSquitter(frame)
	if err != nil {
		// Unsupported TC: structural fields are populated, but
		// nothing actionable for the airplane state.
		return nil
	}

	return a.applyESMessage(plane, icao, squitter.Message, heardAt)
}

// applyESMessage translates a typed Message into the matching
// airplane Update options.
//
//nolint:exhaustive // unhandled Message types fall through cleanly.
func (a *ADSB) applyESMessage(
	plane *airplane.Airplane, icao modes.ICAO, msg modes.Message, heardAt time.Time,
) []airplane.Option {
	switch typed := msg.(type) {
	case modes.IdentificationMessage:
		a.callsignsDecoded.Add(1)

		if validCallsign(typed.Callsign) {
			a.callsignsApplied.Add(1)

			return []airplane.Option{
				airplane.WithReportedCallsign(typed.Callsign, airplane.SourceExtendedSquitter, heardAt),
			}
		}

		return nil
	case modes.AirbornePositionMessage:
		return applyAirbornePosition(a, plane, icao, typed, heardAt)
	case modes.SurfacePositionMessage:
		return applySurfacePosition(a, plane, icao, typed)
	case modes.AirborneVelocityMessage:
		return applyVelocity(typed)
	case modes.AircraftStatusMessage:
		if typed.Subtype == modes.AircraftStatusSubtypeEmergency && typed.EmergencyState != modes.EmergencyStateNone {
			squawk := fmt.Sprintf("%04d", typed.Squawk)

			return []airplane.Option{airplane.WithReportedSquawk(squawk, airplane.SourceExtendedSquitter, heardAt)}
		}

		return nil
	default:
		// Unhandled Message type — only the message-count bump survives.
		return nil
	}
}

func applyAirbornePosition(
	stream *ADSB, plane *airplane.Airplane, icao modes.ICAO, msg modes.AirbornePositionMessage, heardAt time.Time,
) []airplane.Option {
	opts := make([]airplane.Option, 0, 2) //nolint:mnd // up to two options: altitude + position.

	if msg.AltitudeError == nil {
		opts = append(opts,
			airplane.WithReportedAltitude(float64(msg.AltitudeFeet), airplane.SourceExtendedSquitter, heardAt))
	}

	if lat, lon, ok := stream.resolveCPR(icao, msg.CPR, plane.GetLastUpdate()); ok {
		opts = append(opts, airplane.WithPosition(lat, lon))

		// Feed the self-locate observer only when this frame
		// carried a usable altitude — the horizon-circle math
		// needs altitude to derive a constraint radius. Surface-
		// position frames skip this hook intentionally; they
		// arrive at small horizons but with no altitude field.
		if stream.positionObserver != nil && msg.AltitudeError == nil {
			stream.positionObserver(lat, lon, float64(msg.AltitudeFeet))
		}
	}

	return opts
}

func applySurfacePosition(
	stream *ADSB, plane *airplane.Airplane, icao modes.ICAO, msg modes.SurfacePositionMessage,
) []airplane.Option {
	opts := make([]airplane.Option, 0, 3) //nolint:mnd // up to three options: velocity + heading + position.

	if msg.GroundSpeedAvailable {
		opts = append(opts, airplane.WithVelocity(msg.GroundSpeedKnots))
	}

	if msg.HeadingAvailable {
		opts = append(opts, airplane.WithHeading(msg.HeadingDegrees))
	}

	if lat, lon, ok := stream.resolveCPR(icao, msg.CPR, plane.GetLastUpdate()); ok {
		opts = append(opts, airplane.WithPosition(lat, lon))
	}

	return opts
}

func applyVelocity(msg modes.AirborneVelocityMessage) []airplane.Option {
	opts := make([]airplane.Option, 0, 3) //nolint:mnd // up to three options: velocity + heading + vert rate.

	if msg.GroundSpeedAvailable {
		opts = append(opts,
			airplane.WithVelocity(msg.GroundSpeedKnots),
			airplane.WithHeading(msg.TrackDegrees),
		)
	}

	if msg.VerticalRateAvailable {
		opts = append(opts, airplane.WithVertRate(float64(msg.VerticalRateFeetMin)))
	}

	return opts
}
