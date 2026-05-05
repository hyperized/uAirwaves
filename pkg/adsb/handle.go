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
// to '#'. Pure noise frames typically yield ~3 '#' chars in 8;
// real frames with a single-bit error in the callsign field
// yield 0 or 1. We accept up to one '#' so partially-corrupted
// real callsigns still register, while pure noise gets dropped.
func validCallsign(s string) bool {
	const maxPlaceholders = 1

	return s != "" && strings.Count(s, "#") <= maxPlaceholders
}

// handleFrame routes a freshly-demodulated frame through the
// modes decoder and folds the result into the live airplanes
// list.
func (a *ADSB) handleFrame(frame demod.Frame, planes *airplanes.Airplanes) {
	a.totalFrames.Add(1)

	if frame.Errors > 0 {
		a.recoveredFrames.Add(1)
	}

	mFrame := modes.Frame(frame.Bytes)

	icao, learned := learnICAO(mFrame, frame.CRC)
	if !learned {
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

// learnICAO extracts the broadcasting/addressed ICAO. For
// DF 17/18 (extended squitter) and DF 11 unsolicited, plain CRC
// applies — clean frames have residual = 0 and the AA sits in
// bytes 1..3. For ICAO-overlay DFs (0/4/5/16/20/21) the producer's
// CRC residual *is* the addressed aircraft's ICAO.
//
// The residual == 0 gate on broadcasts is what filters preamble
// false-positives from random IQ; without it, ~50% of "frames"
// the demod emits are noise that happens to have valid DF bits in
// position. That noise was creating phantom planes and overwriting
// real callsigns with garbage.
//
//nolint:exhaustive // DFMilitaryES (19) is opaque to civilian receivers; falls through to the unrecognised branch.
func learnICAO(frame modes.Frame, crcResidual uint32) (modes.ICAO, bool) {
	const (
		highShift = 16
		midShift  = 8
	)

	switch frame.DF() {
	case modes.DFExtendedSquitter, modes.DFNonTransponderES:
		if len(frame) != modes.LongFrameBytes || crcResidual != 0 {
			return 0, false
		}

		return modes.ICAO(frame[1])<<highShift | modes.ICAO(frame[2])<<midShift | modes.ICAO(frame[3]), true
	case modes.DFAllCallReply:
		if len(frame) != modes.ShortFrameBytes || crcResidual != 0 {
			return 0, false
		}

		return modes.ICAO(frame[1])<<highShift | modes.ICAO(frame[2])<<midShift | modes.ICAO(frame[3]), true
	case modes.DFShortAirAir, modes.DFSurveillanceAlt, modes.DFSurveillanceID,
		modes.DFLongAirAir, modes.DFCommBAltitude, modes.DFCommBIdentity,
		modes.DFCommDExtendedLength:
		// CRC residual = addressed ICAO (parity-overlay scheme).
		// We accept it without a roster check; the airplanes
		// collection naturally tolerates short-lived bogus
		// entries because the prune sweep evicts stale ICAOs.
		return modes.ICAO(crcResidual), crcResidual != 0
	}

	return 0, false
}

// applyFrame folds a single frame into the per-airplane state.
// Per-DF dispatch keeps the spec-specific bits in modes; this
// file only knows how to translate a typed message into
// airplane.Update options.
//
//nolint:exhaustive // DFs without a per-DF aggregator (DF 0/11/16/19/24-31) are message-count-only.
func (a *ADSB) applyFrame(plane *airplane.Airplane, frame modes.Frame, icao modes.ICAO, src demod.Frame) {
	plane.Update(airplane.WithLastUpdate(src.WallTime))

	switch frame.DF() {
	case modes.DFSurveillanceAlt:
		applySurveillanceAltitude(plane, frame, icao)
	case modes.DFSurveillanceID:
		applySurveillanceIdentity(plane, frame, icao)
	case modes.DFExtendedSquitter, modes.DFNonTransponderES:
		a.applyExtendedSquitter(plane, frame, icao)
	case modes.DFCommBAltitude:
		applyCommBAltitude(plane, frame, icao)
	case modes.DFCommBIdentity:
		applyCommBIdentity(plane, frame, icao)
	default:
		// DF 0/11/16/19/24-31: message-count-only (already bumped above).
	}
}

func applySurveillanceAltitude(plane *airplane.Airplane, frame modes.Frame, icao modes.ICAO) {
	reply, err := modes.DecodeSurveillanceAltitude(frame, icao)
	if err != nil || reply.AltitudeError != nil {
		return
	}

	plane.Update(airplane.WithAltitude(float64(reply.AltitudeFeet)))
}

func applySurveillanceIdentity(plane *airplane.Airplane, frame modes.Frame, icao modes.ICAO) {
	reply, err := modes.DecodeSurveillanceIdentity(frame, icao)
	if err != nil {
		return
	}

	plane.Update(airplane.WithSquawk(fmt.Sprintf("%04d", reply.Squawk)))
}

func applyCommBAltitude(plane *airplane.Airplane, frame modes.Frame, icao modes.ICAO) {
	reply, err := modes.DecodeCommBAltitude(frame, icao)
	if err != nil {
		return
	}

	if reply.AltitudeError == nil {
		plane.Update(airplane.WithAltitude(float64(reply.AltitudeFeet)))
	}

	if callsign, ok := modes.DecodeBDS20Callsign(reply.MB); ok && validCallsign(callsign) {
		plane.Update(airplane.WithCallsign(callsign))
	}
}

func applyCommBIdentity(plane *airplane.Airplane, frame modes.Frame, icao modes.ICAO) {
	reply, err := modes.DecodeCommBIdentity(frame, icao)
	if err != nil {
		return
	}

	plane.Update(airplane.WithSquawk(fmt.Sprintf("%04d", reply.Squawk)))

	if callsign, ok := modes.DecodeBDS20Callsign(reply.MB); ok && validCallsign(callsign) {
		plane.Update(airplane.WithCallsign(callsign))
	}
}

// applyExtendedSquitter dispatches the ME field through modes
// and folds the result into the airplane state.
func (a *ADSB) applyExtendedSquitter(plane *airplane.Airplane, frame modes.Frame, icao modes.ICAO) {
	squitter, err := modes.DecodeExtendedSquitter(frame)
	if err != nil {
		// Unsupported TC: structural fields are populated, but
		// nothing actionable for the airplane state.
		return
	}

	a.applyESMessage(plane, icao, squitter.Message)
}

// applyESMessage translates a typed Message into the matching
// airplane Update options.
//
//nolint:exhaustive // unhandled Message types fall through cleanly.
func (a *ADSB) applyESMessage(plane *airplane.Airplane, icao modes.ICAO, msg modes.Message) {
	switch typed := msg.(type) {
	case modes.IdentificationMessage:
		a.callsignsDecoded.Add(1)

		if validCallsign(typed.Callsign) {
			a.callsignsApplied.Add(1)
			plane.Update(airplane.WithCallsign(typed.Callsign))
		}
	case modes.AirbornePositionMessage:
		applyAirbornePosition(a, plane, icao, typed)
	case modes.SurfacePositionMessage:
		applySurfacePosition(a, plane, icao, typed)
	case modes.AirborneVelocityMessage:
		applyVelocity(plane, typed)
	case modes.AircraftStatusMessage:
		if typed.Subtype == modes.AircraftStatusSubtypeEmergency && typed.EmergencyState != modes.EmergencyStateNone {
			plane.Update(airplane.WithSquawk(fmt.Sprintf("%04d", typed.Squawk)))
		}
	default:
		// Unhandled Message type — message-count was already bumped via Update(WithLastUpdate).
	}
}

func applyAirbornePosition(stream *ADSB, plane *airplane.Airplane, icao modes.ICAO, msg modes.AirbornePositionMessage) {
	if msg.AltitudeError == nil {
		plane.Update(airplane.WithAltitude(float64(msg.AltitudeFeet)))
	}

	if lat, lon, ok := stream.resolveCPR(icao, msg.CPR, plane.GetLastUpdate()); ok {
		plane.Update(
			airplane.WithLatitude(lat),
			airplane.WithLongitude(lon),
		)
	}
}

func applySurfacePosition(stream *ADSB, plane *airplane.Airplane, icao modes.ICAO, msg modes.SurfacePositionMessage) {
	if msg.GroundSpeedAvailable {
		plane.Update(airplane.WithVelocity(msg.GroundSpeedKnots))
	}

	if msg.HeadingAvailable {
		plane.Update(airplane.WithHeading(msg.HeadingDegrees))
	}

	if lat, lon, ok := stream.resolveCPR(icao, msg.CPR, plane.GetLastUpdate()); ok {
		plane.Update(
			airplane.WithLatitude(lat),
			airplane.WithLongitude(lon),
		)
	}
}

func applyVelocity(plane *airplane.Airplane, msg modes.AirborneVelocityMessage) {
	if msg.GroundSpeedAvailable {
		plane.Update(
			airplane.WithVelocity(msg.GroundSpeedKnots),
			airplane.WithHeading(msg.TrackDegrees),
		)
	}

	if msg.VerticalRateAvailable {
		plane.Update(airplane.WithVertRate(float64(msg.VerticalRateFeetMin)))
	}
}
