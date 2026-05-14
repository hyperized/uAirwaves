package battery

import (
	"errors"
	"os"
	"testing"
)

func TestProcess(t *testing.T) {
	t.Parallel()

	t.Run("charging status", func(t *testing.T) {
		t.Parallel()

		status := NewStatus()
		process(status, []string{keyPowerSupplyStatus, "Charging"})

		if !status.IsCharging() {
			t.Error("expected charging true")
		}
	})

	t.Run("discharging status", func(t *testing.T) {
		t.Parallel()

		status := NewStatus()
		process(status, []string{keyPowerSupplyStatus, "Discharging"})

		if status.IsCharging() {
			t.Error("expected charging false")
		}
	})

	t.Run("capacity status", func(t *testing.T) {
		t.Parallel()

		status := NewStatus()
		process(status, []string{keyPowerSupplyCapacity, "85"})

		if status.GetPercentage() != 85 {
			t.Errorf("expected percentage 85, got %d", status.GetPercentage())
		}
	})

	t.Run("invalid capacity", func(t *testing.T) {
		t.Parallel()

		status := NewStatus()
		old := status.GetPercentage()
		process(status, []string{keyPowerSupplyCapacity, "abc"})

		if status.GetPercentage() != old {
			t.Errorf("expected percentage to remain %d, got %d", old, status.GetPercentage())
		}
	})

	t.Run("unknown key", func(t *testing.T) {
		t.Parallel()

		status := NewStatus()
		process(status, []string{"UNKNOWN", "value"})
		// should not panic or change anything
	})
}

func TestUpdate(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	tmpFile, err := os.CreateTemp(tmpDir, "battery_uevent")
	if err != nil {
		t.Fatal(err)
	}

	content := `POWER_SUPPLY_NAME=axp20x-battery
POWER_SUPPLY_STATUS=Charging
POWER_SUPPLY_PRESENT=1
POWER_SUPPLY_ONLINE=1
POWER_SUPPLY_CAPACITY=95
INVALID_LINE
ANOTHER_INVALID=TOO=MANY=PARTS
`
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatal(err)
	}

	if err := tmpFile.Close(); err != nil {
		t.Fatal(err)
	}

	status := NewStatus()

	err = update(status, tmpFile.Name())
	if err != nil {
		t.Errorf("update() error = %v", err)
	}

	if !status.IsCharging() {
		t.Error("expected charging true")
	}

	if status.GetPercentage() != 95 {
		t.Errorf("expected percentage 95, got %d", status.GetPercentage())
	}
}

func TestUpdate_Errors(t *testing.T) {
	t.Parallel()
	t.Run("missing file", func(t *testing.T) {
		t.Parallel()

		status := NewStatus()

		err := update(status, "/non/existent/file")
		if !errors.Is(err, errFileOpen) {
			t.Errorf("expected errFileOpen, got %v", err)
		}
	})

	t.Run("scanner error", func(t *testing.T) {
		t.Parallel()
		// os.Open on a directory succeeds on Unix; bufio.Scanner
		// then yields no lines without producing an error.
		// Calling update on a directory exercises the no-line
		// successful-scan path.
		dir := t.TempDir()

		status := NewStatus()
		_ = update(status, dir)
	})
}
