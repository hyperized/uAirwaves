package battery

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const FilePath = "/sys/class/power_supply/axp20x-battery/uevent"

type State struct {
	Name    string
	Type    string
	Present bool
	Online  bool
	Status  string
	Voltage struct {
		Now    float64
		Design struct {
			Max float64
			Min float64
		}
	}
	Current struct {
		Now    float64
		Charge struct {
			Now float64
			Max float64
		}
	}
	Capacity int8
	Health   string
}

func Watch(ctx context.Context, status *Status) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := update(status); err != nil {
				return fmt.Errorf("battery update failed: %w", err)
			}
		}
	}
}

func update(status *Status) error {
	var err error
	state := &State{}

	fh, err := os.Open(FilePath)
	if err != nil {
		return err
	}
	defer fh.Close()

	scanner := bufio.NewScanner(fh)

	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, "=")
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		value := parts[1]

		switch key {
		case "POWER_SUPPLY_NAME":
			state.Name = value
		case "POWER_SUPPLY_TYPE":
			state.Type = value
		case "POWER_SUPPLY_PRESENT":
			state.Present = value == "1"
		case "POWER_SUPPLY_ONLINE":
			state.Online = value == "1"
		case "POWER_SUPPLY_STATUS":
			state.Status = value
		case "POWER_SUPPLY_VOLTAGE_NOW":
			if v, err := strconv.ParseFloat(value, 64); err == nil {
				state.Voltage.Now = v
			}
		case "POWER_SUPPLY_VOLTAGE_MAX_DESIGN":
			if v, err := strconv.ParseFloat(value, 64); err == nil {
				state.Voltage.Design.Max = v
			}
		case "POWER_SUPPLY_VOLTAGE_MIN_DESIGN":
			if v, err := strconv.ParseFloat(value, 64); err == nil {
				state.Voltage.Design.Min = v
			}
		case "POWER_SUPPLY_CURRENT_NOW":
			if v, err := strconv.ParseFloat(value, 64); err == nil {
				state.Current.Now = v
			}
		case "POWER_SUPPLY_CHARGE_NOW":
			if v, err := strconv.ParseFloat(value, 64); err == nil {
				state.Current.Charge.Now = v
			}
		case "POWER_SUPPLY_CHARGE_FULL":
			if v, err := strconv.ParseFloat(value, 64); err == nil {
				state.Current.Charge.Max = v
			}
		case "POWER_SUPPLY_CAPACITY":
			if v, err := strconv.ParseInt(value, 10, 8); err == nil {
				state.Capacity = int8(v)
			}
		case "POWER_SUPPLY_HEALTH":
			state.Health = value
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	status.Update(
		WithCharging(state.Status == "Charging"),
		WithPercentage(state.Capacity),
	)

	return nil
}
