//go:build darwin

package battery

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

const (
	maxPercent = 100
	minPercent = 0
)

// pmsetPercent matches the "83%" charge token pmset prints for a
// power source. The id= field carries no trailing %, so anchoring
// on % keeps the match on the charge level.
var pmsetPercent = regexp.MustCompile(`(\d+)%`)

// pmsetRunner executes `pmset -g batt` and returns its stdout. A
// seam so tests can drive parsePmset without a real battery.
type pmsetRunner func(ctx context.Context) (string, error)

// runPmset is the production runner. CommandContext ties the
// subprocess lifetime to the poll loop's context so a shutdown
// mid-read reaps it.
func runPmset(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "pmset", "-g", "batt").Output()
	if err != nil {
		return "", fmt.Errorf("battery: pmset: %w", err)
	}

	return string(out), nil
}

// newReader returns a reader backed by pmset. The override is
// ignored on macOS: there is no sysfs path to point at, so battery
// data always comes from IOKit via pmset.
func newReader(string) reader {
	return newReaderWith(runPmset)
}

// newReaderWith builds the pmset-backed reader over an injected
// runner, so tests can supply a fake without shelling out.
func newReaderWith(run pmsetRunner) reader {
	return func(ctx context.Context) (reading, error) {
		out, err := run(ctx)
		if err != nil {
			return reading{}, err
		}

		return parsePmset(out)
	}
}

// parsePmset extracts percentage + charging state from
// `pmset -g batt` output. It returns ErrUnsupported when no
// battery source is present (desktop Macs / CI), so Watch stops
// cleanly instead of polling a machine that has no battery.
func parsePmset(out string) (reading, error) {
	match := pmsetPercent.FindStringSubmatch(out)
	if match == nil {
		return reading{}, ErrUnsupported
	}

	pct, err := strconv.Atoi(match[1])
	if err != nil {
		return reading{}, fmt.Errorf("battery: parse pmset percent: %w", err)
	}

	// pmset prints "; charging" while gaining charge and
	// "; finishing charge" on the final trickle; both mean the
	// pack is actively topping up. "; charged" (full on AC) and
	// "; discharging" map to charging=false, matching the Linux
	// reader's "only Charging is charging" semantics.
	charging := strings.Contains(out, "; charging") || strings.Contains(out, "; finishing charge")

	return reading{percentage: clampPercent(pct), charging: charging}, nil
}

// clampPercent bounds a parsed percentage into [0,100] before the
// int8 narrowing, so a nonsensical pmset value can never overflow
// the Status field.
func clampPercent(pct int) int8 {
	switch {
	case pct < minPercent:
		return minPercent
	case pct > maxPercent:
		return maxPercent
	default:
		return int8(pct)
	}
}
