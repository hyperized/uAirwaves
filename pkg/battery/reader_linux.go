//go:build linux

package battery

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// powerSupplyRoot is the sysfs power_supply class directory.
	// Every battery, mains and USB source the kernel knows about
	// appears here as a subdirectory with a `type` file.
	powerSupplyRoot = "/sys/class/power_supply"

	keyStatus   = "POWER_SUPPLY_STATUS"
	keyCapacity = "POWER_SUPPLY_CAPACITY"

	// typeBattery is the `type` file value that marks a device as
	// a battery (as opposed to Mains, USB or UPS).
	typeBattery = "Battery"

	// statusCharging is the only POWER_SUPPLY_STATUS value that
	// means "actively taking on charge". Full / Discharging /
	// Not charging / Unknown all map to charging=false.
	statusCharging = "Charging"
)

// errNoBattery signals that no Battery-type device is registered
// under powerSupplyRoot yet. It is deliberately not ErrUnsupported:
// a cold-boot udev race can leave the device unregistered for a
// moment, so the loop retries rather than giving up for good.
var errNoBattery = errors.New("battery: no power_supply device of type Battery found")

// newReader returns a reader over the Linux sysfs power_supply
// class. A non-empty override is a fixed path to a uevent file —
// the historical --battery contract (e.g. the uConsole's
// axp20x-battery). Empty means auto-discover: enumerate the class
// and read the first device whose type is Battery. Discovery is
// retried on each read until it resolves, then cached, so a
// startup udev race recovers on a later tick.
func newReader(override string) reader {
	if override != "" {
		return func(context.Context) (reading, error) { return readUevent(override) }
	}

	return newReaderFrom(powerSupplyRoot)
}

// newReaderFrom returns a discovery reader rooted at the given
// power_supply directory. Split from newReader so tests can point
// discovery at a fake sysfs tree without mutating package globals.
// The resolved device path is cached after the first successful
// discovery.
func newReaderFrom(root string) reader {
	var resolved string

	return func(context.Context) (reading, error) {
		if resolved == "" {
			path, err := discoverBattery(root)
			if err != nil {
				return reading{}, err
			}

			resolved = path
		}

		return readUevent(resolved)
	}
}

// discoverBattery returns the uevent path of the first
// power_supply device whose `type` file reads "Battery". Entries
// are walked in the sorted order os.ReadDir yields, so BAT0 wins
// over BAT1 deterministically.
func discoverBattery(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}

	for _, entry := range entries {
		typePath := filepath.Join(root, entry.Name(), "type")

		data, err := os.ReadFile(filepath.Clean(typePath))
		if err != nil {
			continue
		}

		if strings.TrimSpace(string(data)) == typeBattery {
			return filepath.Join(root, entry.Name(), "uevent"), nil
		}
	}

	return "", errNoBattery
}

// readUevent parses POWER_SUPPLY_CAPACITY and POWER_SUPPLY_STATUS
// out of a sysfs uevent file into a reading.
func readUevent(path string) (reading, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return reading{}, err
	}

	defer func() { _ = file.Close() }()

	var result reading

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			continue
		}

		applyUeventPair(&result, key, value)
	}

	if err := scanner.Err(); err != nil {
		return reading{}, err
	}

	return result, nil
}

// applyUeventPair folds a single uevent key/value pair into the
// reading. Unknown keys and unparseable capacities are ignored so
// a malformed line never corrupts an otherwise-valid read.
func applyUeventPair(result *reading, key, value string) {
	switch key {
	case keyStatus:
		result.charging = value == statusCharging
	case keyCapacity:
		if v, err := strconv.ParseInt(value, 10, 8); err == nil {
			result.percentage = int8(v)
		}
	}
}
