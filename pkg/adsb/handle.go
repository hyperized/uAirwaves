package adsb

import (
	"fmt"
	"strings"

	"github.com/hyperized/demod1090/demod"
	"github.com/hyperized/modes"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplane"
	"lab.hyperized.net/hyperized/uAirwaves/pkg/airplanes"
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
	planes.Ensure(icaoStr)

	plane, found := planes.Get(icaoStr)
	if !found {
		return
	}

	a.applyFrame(plane, mFrame, icao, frame)
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

	switch frame.DF() {
	case modes.DFSurveillanceAlt:
		opts = append(opts, applySurveillanceAltitude(frame, icao)...)
	case modes.DFSurveillanceID:
		opts = append(opts, applySurveillanceIdentity(frame, icao)...)
	case modes.DFExtendedSquitter, modes.DFNonTransponderES:
		opts = append(opts, a.applyExtendedSquitter(plane, frame, icao)...)
	case modes.DFCommBAltitude:
		opts = append(opts, applyCommBAltitude(frame, icao)...)
	case modes.DFCommBIdentity:
		opts = append(opts, applyCommBIdentity(frame, icao)...)
	default:
		// DF 0/11/16/19/24-31: message-count-only.
	}

	plane.Update(opts...)
}

func applySurveillanceAltitude(frame modes.Frame, icao modes.ICAO) []airplane.Option {
	reply, err := modes.DecodeSurveillanceAltitude(frame, icao)
	if err != nil || reply.AltitudeError != nil {
		return nil
	}

	return []airplane.Option{airplane.WithAltitude(float64(reply.AltitudeFeet))}
}

func applySurveillanceIdentity(frame modes.Frame, icao modes.ICAO) []airplane.Option {
	reply, err := modes.DecodeSurveillanceIdentity(frame, icao)
	if err != nil {
		return nil
	}

	return []airplane.Option{airplane.WithSquawk(fmt.Sprintf("%04d", reply.Squawk))}
}

func applyCommBAltitude(frame modes.Frame, icao modes.ICAO) []airplane.Option {
	reply, err := modes.DecodeCommBAltitude(frame, icao)
	if err != nil {
		return nil
	}

	opts := make([]airplane.Option, 0, 2) //nolint:mnd // up to two options: altitude + callsign.

	if reply.AltitudeError == nil {
		opts = append(opts, airplane.WithAltitude(float64(reply.AltitudeFeet)))
	}

	if callsign, ok := modes.DecodeBDS20Callsign(reply.MB); ok && validCallsign(callsign) {
		opts = append(opts, airplane.WithCallsign(callsign))
	}

	return opts
}

func applyCommBIdentity(frame modes.Frame, icao modes.ICAO) []airplane.Option {
	reply, err := modes.DecodeCommBIdentity(frame, icao)
	if err != nil {
		return nil
	}

	opts := make([]airplane.Option, 0, 2) //nolint:mnd // up to two options: squawk + callsign.
	opts = append(opts, airplane.WithSquawk(fmt.Sprintf("%04d", reply.Squawk)))

	if callsign, ok := modes.DecodeBDS20Callsign(reply.MB); ok && validCallsign(callsign) {
		opts = append(opts, airplane.WithCallsign(callsign))
	}

	return opts
}

// applyExtendedSquitter dispatches the ME field through modes
// and returns the airplane Update options for the typed message.
func (a *ADSB) applyExtendedSquitter(plane *airplane.Airplane, frame modes.Frame, icao modes.ICAO) []airplane.Option {
	squitter, err := modes.DecodeExtendedSquitter(frame)
	if err != nil {
		// Unsupported TC: structural fields are populated, but
		// nothing actionable for the airplane state.
		return nil
	}

	return a.applyESMessage(plane, icao, squitter.Message)
}

// applyESMessage translates a typed Message into the matching
// airplane Update options.
//
//nolint:exhaustive // unhandled Message types fall through cleanly.
func (a *ADSB) applyESMessage(plane *airplane.Airplane, icao modes.ICAO, msg modes.Message) []airplane.Option {
	switch typed := msg.(type) {
	case modes.IdentificationMessage:
		a.callsignsDecoded.Add(1)

		if validCallsign(typed.Callsign) {
			a.callsignsApplied.Add(1)

			return []airplane.Option{airplane.WithCallsign(typed.Callsign)}
		}

		return nil
	case modes.AirbornePositionMessage:
		return applyAirbornePosition(a, plane, icao, typed)
	case modes.SurfacePositionMessage:
		return applySurfacePosition(a, plane, icao, typed)
	case modes.AirborneVelocityMessage:
		return applyVelocity(typed)
	case modes.AircraftStatusMessage:
		if typed.Subtype == modes.AircraftStatusSubtypeEmergency && typed.EmergencyState != modes.EmergencyStateNone {
			return []airplane.Option{airplane.WithSquawk(fmt.Sprintf("%04d", typed.Squawk))}
		}

		return nil
	default:
		// Unhandled Message type — only the message-count bump survives.
		return nil
	}
}

func applyAirbornePosition(
	stream *ADSB, plane *airplane.Airplane, icao modes.ICAO, msg modes.AirbornePositionMessage,
) []airplane.Option {
	opts := make([]airplane.Option, 0, 2) //nolint:mnd // up to two options: altitude + position.

	if msg.AltitudeError == nil {
		opts = append(opts, airplane.WithAltitude(float64(msg.AltitudeFeet)))
	}

	if lat, lon, ok := stream.resolveCPR(icao, msg.CPR, plane.GetLastUpdate()); ok {
		opts = append(opts, airplane.WithPosition(lat, lon))
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
