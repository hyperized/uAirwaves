package battery

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const FilePath = "/sys/class/power_supply/axp20x-battery/uevent"

type Battery struct {
	result Result
	mu     sync.RWMutex
}

type Result struct {
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

func New() *Battery {
	return &Battery{
		result: Result{},
		mu:     sync.RWMutex{},
	}
}

func (b *Battery) Watch(ctx context.Context, errChan chan<- error) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	// Initial update on watch
	if err := b.Update(); err != nil {
		// Send error to channel but continue the loop
		errChan <- fmt.Errorf("battery update failed: %w", err)
		return
	}

	for {
		select {
		case <-ctx.Done():
			// Clean exit when context is cancelled
			return
		case <-ticker.C:
			if err := b.Update(); err != nil {
				// Send error to channel but continue the loop
				errChan <- fmt.Errorf("battery update failed: %w", err)
			}
		}
	}
}

func (b *Battery) Update() error {
	b.mu.Lock()
	defer b.mu.Unlock()

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
			b.result.Name = value
		case "POWER_SUPPLY_TYPE":
			b.result.Type = value
		case "POWER_SUPPLY_PRESENT":
			b.result.Present = value == "1"
		case "POWER_SUPPLY_ONLINE":
			b.result.Online = value == "1"
		case "POWER_SUPPLY_STATUS":
			b.result.Status = value
		case "POWER_SUPPLY_VOLTAGE_NOW":
			if v, err := strconv.ParseFloat(value, 64); err == nil {
				b.result.Voltage.Now = v
			}
		case "POWER_SUPPLY_VOLTAGE_MAX_DESIGN":
			if v, err := strconv.ParseFloat(value, 64); err == nil {
				b.result.Voltage.Design.Max = v
			}
		case "POWER_SUPPLY_VOLTAGE_MIN_DESIGN":
			if v, err := strconv.ParseFloat(value, 64); err == nil {
				b.result.Voltage.Design.Min = v
			}
		case "POWER_SUPPLY_CURRENT_NOW":
			if v, err := strconv.ParseFloat(value, 64); err == nil {
				b.result.Current.Now = v
			}
		case "POWER_SUPPLY_CHARGE_NOW":
			if v, err := strconv.ParseFloat(value, 64); err == nil {
				b.result.Current.Charge.Now = v
			}
		case "POWER_SUPPLY_CHARGE_FULL":
			if v, err := strconv.ParseFloat(value, 64); err == nil {
				b.result.Current.Charge.Max = v
			}
		case "POWER_SUPPLY_CAPACITY":
			if v, err := strconv.ParseInt(value, 10, 8); err == nil {
				b.result.Capacity = int8(v)
			}
		case "POWER_SUPPLY_HEALTH":
			b.result.Health = value
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func (b *Battery) Display() string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	r := ""
	if b.result.Status == "Charging" {
		r = r + "~C "
	}
	return r + strconv.Itoa(int(b.result.Capacity)) + "%"
}
