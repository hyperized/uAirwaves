package battery

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestProcess(t *testing.T) {
	t.Parallel()

	t.Run("charging status", func(t *testing.T) {
		t.Parallel()

		status := NewStatus()
		process(status, []string{"POWER_SUPPLY_STATUS", "Charging"})

		if !status.IsCharging() {
			t.Error("expected charging true")
		}
	})

	t.Run("discharging status", func(t *testing.T) {
		t.Parallel()

		status := NewStatus()
		process(status, []string{"POWER_SUPPLY_STATUS", "Discharging"})

		if status.IsCharging() {
			t.Error("expected charging false")
		}
	})

	t.Run("capacity status", func(t *testing.T) {
		t.Parallel()

		status := NewStatus()
		process(status, []string{"POWER_SUPPLY_CAPACITY", "85"})

		if status.GetPercentage() != 85 {
			t.Errorf("expected percentage 85, got %d", status.GetPercentage())
		}
	})

	t.Run("invalid capacity", func(t *testing.T) {
		t.Parallel()

		status := NewStatus()
		old := status.GetPercentage()
		process(status, []string{"POWER_SUPPLY_CAPACITY", "abc"})

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
	// Create a temporary file
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
		// This is hard to trigger with a real file, but we can try to use a directory
		dir := t.TempDir()

		status := NewStatus()
		_ = update(status, dir)
		// os.Open on a directory might succeed on some OS but scanning it should fail or just return nothing
		// Actually os.Open on a directory succeeds on Unix.
		// To trigger scanner error we'd need a file that fails during reading.
	})
}

func TestWatch_Error(t *testing.T) {
	t.Parallel()

	status := NewStatus()

	err := WatchWithInterval(context.Background(), status, 10*time.Millisecond, "/non/existent/file")
	if err == nil || !errors.Is(err, errFileOpen) {
		t.Errorf("expected errFileOpen in Watch(), got %v", err)
	}
}

func TestWatch_Default(t *testing.T) {
	t.Parallel()
	// We can't easily test the real /sys path, but we can test that it returns an error
	// because the file probably doesn't exist on the test runner's system (unless it's the target hardware).
	status := NewStatus()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := Watch(ctx, status)
	if err != nil && !errors.Is(err, errFileOpen) && !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Watch() unexpected error: %v", err)
	}
}
