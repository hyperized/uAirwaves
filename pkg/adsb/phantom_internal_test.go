package adsb

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hyperized/demod1090/demod"
	"github.com/hyperized/modes"
	"github.com/hyperized/uAirwaves/pkg/airplanes"
)

const (
	phantomFixture = "testdata/phantom-altitudes.txt"

	// phantomErrorFt is what counts as a phantom when the old behaviour is
	// characterised: further from the aircraft's own squitters than any
	// real reply in the capture came.
	phantomErrorFt = 2000

	// worstAcceptedErrorFt bounds what the new rule may apply. An
	// address-parity altitude taken on its own must lie within 300 ft plus
	// 6,000 ft/min of an altitude accepted at most 15 seconds earlier, so it
	// can never be more than 1,800 ft from it.
	worstAcceptedErrorFt = 1800

	// referenceSpan is how far a squitter may be from a reply and still
	// serve as its reference, interpolated when there is one on each side.
	referenceSpan = 30 * time.Second
	// referenceNearest is the limit for a one-sided reference.
	referenceNearest = 10 * time.Second
)

type fixtureFrame struct {
	frame demod.Frame
	icao  modes.ICAO
}

type squitterAltitude struct {
	at   time.Time
	feet float64
}

// loadPhantomFixture reads the capture slice and turns it into the frames
// the demodulator would have handed over, stamped from a fixed epoch.
func loadPhantomFixture(t *testing.T) []fixtureFrame {
	t.Helper()

	file, err := os.Open(phantomFixture)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}

	defer func() { _ = file.Close() }()

	epoch := time.Date(2026, 11, 17, 17, 12, 0, 0, time.UTC)

	var frames []fixtureFrame

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		frames = append(frames, parseFixtureLine(t, line, epoch))
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	return frames
}

func parseFixtureLine(t *testing.T, line string, epoch time.Time) fixtureFrame {
	t.Helper()

	fields := strings.Fields(line)
	if len(fields) != 2 { //nolint:mnd // offset and hex.
		t.Fatalf("fixture line %q: want two fields", line)
	}

	offset, err := strconv.Atoi(fields[0])
	if err != nil {
		t.Fatalf("fixture line %q: %v", line, err)
	}

	raw, err := hex.DecodeString(fields[1])
	if err != nil {
		t.Fatalf("fixture line %q: %v", line, err)
	}

	mFrame := modes.Frame(raw)
	residual := modes.CRCResidual(mFrame)
	icao := modes.ICAO(residual)

	if announcesAddress(mFrame.DF()) {
		icao = modes.ICAO(uint32(raw[1])<<16 | uint32(raw[2])<<8 | uint32(raw[3]))
	}

	return fixtureFrame{
		frame: demod.Frame{
			Bytes:    raw,
			DF:       uint8(mFrame.DF()),
			CRC:      residual,
			WallTime: epoch.Add(time.Duration(offset) * time.Millisecond),
		},
		icao: icao,
	}
}

// parityAltitude decodes the altitude of a DF 4 or DF 20 frame.
func parityAltitude(entry fixtureFrame) (float64, bool) {
	mFrame := modes.Frame(entry.frame.Bytes)

	//nolint:exhaustive // the fixture holds DF 4, 17 and 20 only.
	switch mFrame.DF() {
	case modes.DFSurveillanceAlt:
		reply, err := modes.DecodeSurveillanceAltitude(mFrame, entry.icao)

		return float64(reply.AltitudeFeet), err == nil && reply.AltitudeError == nil
	case modes.DFCommBAltitude:
		reply, err := modes.DecodeCommBAltitude(mFrame, entry.icao)

		return float64(reply.AltitudeFeet), err == nil && reply.AltitudeError == nil
	default:
		return 0, false
	}
}

// squitterAltitudes collects each aircraft's DF 17 altitudes in time order.
func squitterAltitudes(t *testing.T, frames []fixtureFrame) map[modes.ICAO][]squitterAltitude {
	t.Helper()

	out := make(map[modes.ICAO][]squitterAltitude)

	for _, entry := range frames {
		if !announcesAddress(modes.Frame(entry.frame.Bytes).DF()) {
			continue
		}

		squitter, err := modes.DecodeExtendedSquitter(modes.Frame(entry.frame.Bytes))
		if err != nil {
			t.Fatalf("fixture squitter: %v", err)
		}

		position, ok := squitter.Message.(modes.AirbornePositionMessage)
		if !ok || position.AltitudeError != nil {
			continue
		}

		out[entry.icao] = append(out[entry.icao], squitterAltitude{
			at: entry.frame.WallTime, feet: float64(position.AltitudeFeet),
		})
	}

	return out
}

// reference is what the aircraft's own squitters say its altitude was at
// when: interpolated between the squitters either side, or the nearest one.
func reference(series []squitterAltitude, when time.Time) (float64, bool) {
	index := sort.Search(len(series), func(i int) bool { return !series[i].at.Before(when) })

	var before, after *squitterAltitude
	if index > 0 && when.Sub(series[index-1].at) <= referenceSpan {
		before = &series[index-1]
	}

	if index < len(series) && series[index].at.Sub(when) <= referenceSpan {
		after = &series[index]
	}

	switch {
	case before != nil && after != nil:
		span := after.at.Sub(before.at)
		if span == 0 {
			return after.feet, true
		}

		fraction := float64(when.Sub(before.at)) / float64(span)

		return before.feet + fraction*(after.feet-before.feet), true
	case before != nil && when.Sub(before.at) <= referenceNearest:
		return before.feet, true
	case after != nil && after.at.Sub(when) <= referenceNearest:
		return after.feet, true
	default:
		return 0, false
	}
}

// TestPhantomAltitudesBeforeTheRule characterises the bug: before the trust
// rule every address-parity altitude in the fixture was applied as it came,
// and most of them are thousands of feet from the aircraft's own squitters.
func TestPhantomAltitudesBeforeTheRule(t *testing.T) {
	t.Parallel()

	frames := loadPhantomFixture(t)
	series := squitterAltitudes(t, frames)

	var replies, phantoms int

	worst := 0.0

	for _, entry := range frames {
		feet, ok := parityAltitude(entry)
		if !ok {
			continue
		}

		replies++

		want, known := reference(series[entry.icao], entry.frame.WallTime)
		if !known {
			continue
		}

		miss := math.Abs(feet - want)
		worst = max(worst, miss)

		if miss > phantomErrorFt {
			phantoms++
		}
	}

	// Pinned to the fixture: 70 decodable DF 4/20 altitudes, 56 of them
	// more than 2,000 ft off, the worst by over 40,000 ft.
	if replies != 70 || phantoms != 56 || worst < 40000 {
		t.Errorf("replies %d, phantoms %d, worst %.0f ft; want 70, 56, over 40000", replies, phantoms, worst)
	}
}

// TestPhantomAltitudesUnderTheRule replays the fixture through handleFrame
// and checks that no altitude the rule applies from an address-parity reply
// is further from the aircraft's own squitters than the rule allows.
func TestPhantomAltitudesUnderTheRule(t *testing.T) {
	t.Parallel()

	frames := loadPhantomFixture(t)
	series := squitterAltitudes(t, frames)
	stream := New()
	planes := airplanes.New()

	var accepted, held int

	worst := 0.0

	for _, entry := range frames {
		stream.handleFrame(entry.frame, planes)

		feet, ok := parityAltitude(entry)
		if !ok {
			continue
		}

		plane, found := planes.Get(fmt.Sprintf("%06X", uint32(entry.icao)))
		if !found {
			t.Fatalf("%06X: not tracked", uint32(entry.icao))
		}

		snap := plane.GetSnapshot()
		if !snap.AltitudeAt.Equal(entry.frame.WallTime) || snap.Altitude != feet {
			held++

			continue
		}

		accepted++

		if want, known := reference(series[entry.icao], entry.frame.WallTime); known {
			worst = max(worst, math.Abs(feet-want))
		}
	}

	t.Logf("address-parity altitudes: %d accepted, %d held, worst accepted error %.0f ft", accepted, held, worst)

	if worst > worstAcceptedErrorFt {
		t.Errorf("worst accepted error %.0f ft, want at most %d", worst, worstAcceptedErrorFt)
	}

	// Pinned to the fixture so a change to the rule shows up here.
	if accepted != 6 || held != 64 {
		t.Errorf("accepted %d, held %d; want 6 and 64", accepted, held)
	}
}

// TestAddressParityFrameNeverStartsTrack: a DF 4 whose residual is trusted
// but whose aircraft is not in the list must not put it there.
func TestAddressParityFrameNeverStartsTrack(t *testing.T) {
	t.Parallel()

	stream := New()
	planes := airplanes.New()
	now := time.Date(2026, 11, 17, 17, 0, 0, 0, time.UTC)

	stream.icaoFilter.Trust(0x4D24CB, now)
	stream.handleFrame(demod.Frame{
		Bytes:    makeShortFrameWithDF(modes.DFSurveillanceAlt),
		DF:       uint8(modes.DFSurveillanceAlt),
		CRC:      0x4D24CB,
		WallTime: now,
	}, planes)

	if planes.Count() != 0 {
		t.Errorf("address-parity frame created %d track(s)", planes.Count())
	}
}

func TestAnnouncesAddress(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		df   modes.DownlinkFormat
		want bool
	}{
		{modes.DFShortAirAir, false},
		{modes.DFSurveillanceAlt, false},
		{modes.DFSurveillanceID, false},
		{modes.DFAllCallReply, true},
		{modes.DFLongAirAir, false},
		{modes.DFExtendedSquitter, true},
		{modes.DFNonTransponderES, true},
		{modes.DFCommBAltitude, false},
		{modes.DFCommBIdentity, false},
		{modes.DFCommDExtendedLength, false},
	} {
		t.Run(fmt.Sprintf("DF%d", testCase.df), func(t *testing.T) {
			t.Parallel()

			if got := announcesAddress(testCase.df); got != testCase.want {
				t.Errorf("announcesAddress(DF%d) = %v, want %v", testCase.df, got, testCase.want)
			}
		})
	}
}
