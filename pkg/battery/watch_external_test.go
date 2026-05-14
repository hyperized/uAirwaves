package battery_test

import (
	"context"
	"os"
	"testing"
	"time"

	"lab.hyperized.net/hyperized/uAirwaves/pkg/battery"
)

func TestWatch(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	tmpFile, err := os.CreateTemp(tmpDir, "battery_uevent_external_watch")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := tmpFile.WriteString("POWER_SUPPLY_STATUS=Discharging\nPOWER_SUPPLY_CAPACITY=50\n"); err != nil {
		t.Fatal(err)
	}

	if err := tmpFile.Close(); err != nil {
		t.Fatal(err)
	}

	status := battery.NewStatus()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)

	go func() {
		errCh <- battery.WatchWithInterval(ctx, status, 10*time.Millisecond, tmpFile.Name())
	}()

	time.Sleep(50 * time.Millisecond)

	if status.GetPercentage() != 50 {
		t.Errorf("expected percentage 50, got %d", status.GetPercentage())
	}

	content := []byte("POWER_SUPPLY_STATUS=Charging\nPOWER_SUPPLY_CAPACITY=60\n")
	if err := os.WriteFile(tmpFile.Name(), content, 0600); err != nil { //nolint:mnd // standard file mode
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)

	if status.GetPercentage() != 60 {
		t.Errorf("expected percentage 60, got %d", status.GetPercentage())
	}

	cancel()

	err = <-errCh
	if err != nil {
		t.Errorf("Watch() unexpected error: %v", err)
	}
}

// TestWatch_MissingFileNonFatal verifies that an unreadable
// battery file at startup is logged and retried rather than
// killing the watcher, so a transient udev race doesn't take the
// app down.
func TestWatch_MissingFileNonFatal(t *testing.T) {
	t.Parallel()

	status := battery.NewStatus()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	err := battery.WatchWithInterval(ctx, status, 10*time.Millisecond, "/non/existent/file")
	if err != nil {
		t.Errorf("Watch() expected nil on ctx cancel, got %v", err)
	}

	if status.GetPercentage() != 0 {
		t.Errorf("expected percentage to remain 0 when file is missing, got %d", status.GetPercentage())
	}
}

// TestWatch_Default exercises the package-default file path; on a
// non-target host the file is missing, so the watcher should log
// warnings and return nil on ctx cancel.
func TestWatch_Default(t *testing.T) {
	t.Parallel()

	status := battery.NewStatus()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	if err := battery.Watch(ctx, status); err != nil {
		t.Errorf("Watch() unexpected error: %v", err)
	}
}
