//go:build linux

package battery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeFile creates a file with content, failing the test on any
// I/O error.
func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// addDevice materialises a fake power_supply device directory
// (name/type plus an optional uevent) under root.
func addDevice(t *testing.T, root, name, kind, uevent string) {
	t.Helper()

	dev := filepath.Join(root, name)
	if err := os.MkdirAll(dev, 0o750); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(dev, "type"), kind+"\n")

	if uevent != "" {
		writeFile(t, filepath.Join(dev, "uevent"), uevent)
	}
}

func TestReadUevent(t *testing.T) {
	t.Parallel()

	t.Run("parses capacity and charging", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "uevent")
		writeFile(t, path, "POWER_SUPPLY_NAME=axp20x-battery\n"+
			"POWER_SUPPLY_STATUS=Charging\n"+
			"POWER_SUPPLY_CAPACITY=95\n"+
			"MALFORMED_LINE\n"+
			"POWER_SUPPLY_EXTRA=a=b=c\n")

		got, err := readUevent(path)
		if err != nil {
			t.Fatalf("readUevent() error: %v", err)
		}

		if got.percentage != 95 || !got.charging {
			t.Errorf("unexpected reading: %+v", got)
		}
	})

	t.Run("discharging and unparseable capacity", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "uevent")
		writeFile(t, path, "POWER_SUPPLY_STATUS=Discharging\nPOWER_SUPPLY_CAPACITY=nan\n")

		got, err := readUevent(path)
		if err != nil {
			t.Fatalf("readUevent() error: %v", err)
		}

		if got.percentage != 0 || got.charging {
			t.Errorf("expected zero-value reading, got %+v", got)
		}
	})

	t.Run("missing file errors", func(t *testing.T) {
		t.Parallel()

		if _, err := readUevent("/non/existent/uevent"); err == nil {
			t.Error("expected error for missing file")
		}
	})
}

func TestDiscoverBattery(t *testing.T) {
	t.Parallel()

	t.Run("skips mains and finds the battery", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		addDevice(t, root, "AC", "Mains", "")            // sorts first, must be skipped.
		addDevice(t, root, "BAT0", typeBattery, "x=y\n") // the winner.

		path, err := discoverBattery(root)
		if err != nil {
			t.Fatalf("discoverBattery() error: %v", err)
		}

		if path != filepath.Join(root, "BAT0", "uevent") {
			t.Errorf("unexpected path: %s", path)
		}
	})

	t.Run("no battery and typeless devices", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		addDevice(t, root, "AC", "Mains", "")

		// A directory entry with no type file exercises the
		// ReadFile-error continue path.
		if err := os.MkdirAll(filepath.Join(root, "orphan"), 0o750); err != nil {
			t.Fatal(err)
		}

		if _, err := discoverBattery(root); !errors.Is(err, errNoBattery) {
			t.Errorf("expected errNoBattery, got %v", err)
		}
	})

	t.Run("missing root errors", func(t *testing.T) {
		t.Parallel()

		if _, err := discoverBattery("/no/such/root"); err == nil {
			t.Error("expected error for missing root")
		}
	})
}

func TestNewReader_Linux(t *testing.T) {
	t.Parallel()

	t.Run("override reads the given uevent", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "uevent")
		writeFile(t, path, "POWER_SUPPLY_STATUS=Charging\nPOWER_SUPPLY_CAPACITY=33\n")

		got, err := newReader(path)(context.Background())
		if err != nil {
			t.Fatalf("reader error: %v", err)
		}

		if got.percentage != 33 || !got.charging {
			t.Errorf("unexpected reading: %+v", got)
		}
	})

	t.Run("discovery resolves and caches", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		addDevice(t, root, "battery", typeBattery, "POWER_SUPPLY_CAPACITY=61\n")

		read := newReaderFrom(root)

		first, err := read(context.Background())
		if err != nil {
			t.Fatalf("first read error: %v", err)
		}

		if first.percentage != 61 {
			t.Errorf("expected 61, got %d", first.percentage)
		}

		if second, _ := read(context.Background()); second.percentage != 61 {
			t.Errorf("cached read changed: %d", second.percentage)
		}
	})

	t.Run("discovery surfaces no-battery", func(t *testing.T) {
		t.Parallel()

		if _, err := newReaderFrom(t.TempDir())(context.Background()); !errors.Is(err, errNoBattery) {
			t.Errorf("expected errNoBattery, got %v", err)
		}
	})
}
