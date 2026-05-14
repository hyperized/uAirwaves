package battery

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultFilePath = "/sys/class/power_supply/axp20x-battery/uevent"
	defaultInterval = 30 * time.Second

	keyPowerSupplyStatus   = "POWER_SUPPLY_STATUS"
	keyPowerSupplyCapacity = "POWER_SUPPLY_CAPACITY"
)

var errFileOpen = errors.New("failed to open battery uevent file")
var errScanner = errors.New("failed to scan battery uevent file")

// Watch updates the battery status periodically.
func Watch(ctx context.Context, status *Status) error {
	return WatchWithInterval(ctx, status, defaultInterval, defaultFilePath)
}

// WatchWithInterval updates the battery status with a custom
// interval and file path. Both the initial read and subsequent
// ticks treat update failures the same way: log a warning and
// keep polling. This survives a transient udev race at startup
// (uevent file briefly unreadable) without taking the whole app
// down. Returns nil on ctx cancellation.
func WatchWithInterval(ctx context.Context, status *Status, interval time.Duration, filePath string) error {
	if err := update(status, filePath); err != nil {
		slog.Warn("initial battery update failed, will retry", slog.Any("error", err))
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := update(status, filePath); err != nil {
				slog.Warn("battery update failed, will retry", slog.Any("error", err))
			}
		}
	}
}

// update reads the battery uevent file and updates the battery status.
func update(status *Status, filePath string) error {
	var (
		err         error
		fileHandler *os.File
	)

	fileHandler, err = os.Open(filepath.Clean(filePath))
	if err != nil {
		return errors.Join(err, errFileOpen)
	}
	defer func(fileHandler *os.File) {
		_ = fileHandler.Close()
	}(fileHandler)

	scanner := bufio.NewScanner(fileHandler)

	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, "=")

		if len(parts) != 2 {
			continue
		}

		process(status, parts)
	}

	if err = scanner.Err(); err != nil {
		return errors.Join(err, errScanner)
	}

	return nil
}

// process updates the battery status based on the uevent key/value pair.
func process(status *Status, parts []string) {
	key := parts[0]
	value := parts[1]

	if key == keyPowerSupplyStatus {
		status.Update(WithCharging(value == "Charging"))
	}

	if key == keyPowerSupplyCapacity {
		if v, err := strconv.ParseInt(value, 10, 8); err == nil {
			status.Update(WithPercentage(int8(v)))
		}
	}
}
