package battery

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultFilePath = "/sys/class/power_supply/axp20x-battery/uevent"
	defaultInterval = 30 * time.Second
)

var errFileOpen = errors.New("failed to open battery uevent file")
var errScanner = errors.New("failed to scan battery uevent file")

// Watch updates the battery status periodically.
func Watch(ctx context.Context, status *Status) error {
	return WatchWithInterval(ctx, status, defaultInterval, defaultFilePath)
}

// WatchWithInterval updates the battery status with a custom interval and file path.
func WatchWithInterval(ctx context.Context, status *Status, interval time.Duration, filePath string) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := update(status, filePath); err != nil {
				return fmt.Errorf("battery update failed: %w", err)
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

	if key == "POWER_SUPPLY_STATUS" {
		status.Update(WithCharging(value == "Charging"))
	}

	if key == "POWER_SUPPLY_CAPACITY" {
		if v, err := strconv.ParseInt(value, 10, 8); err == nil {
			status.Update(WithPercentage(int8(v)))
		}
	}
}
