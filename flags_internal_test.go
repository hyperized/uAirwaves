package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

func TestParseFlagsDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := parseFlags(nil, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags(nil) = %v, want nil", err)
	}

	want := cliConfig{}
	if cfg != want {
		t.Errorf("parseFlags(nil) = %+v, want all-zero %+v", cfg, want)
	}
}

func TestParseFlagsEveryFlagSetsTarget(t *testing.T) {
	t.Parallel()

	args := []string{
		"--beast", "radio:30005",
		"--replay-iq", "/tmp/cap.iq",
		"--auto-sweep",
		"--bias-t",
		"--gpsd", "10.0.0.1:2947",
		"--battery", "/sys/class/power_supply/foo/uevent",
		"--check", "30s",
	}

	cfg, err := parseFlags(args, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags(%v) = %v, want nil", args, err)
	}

	if cfg.beastAddress != "radio:30005" {
		t.Errorf("beastAddress = %q, want radio:30005", cfg.beastAddress)
	}

	if cfg.replayIQPath != "/tmp/cap.iq" {
		t.Errorf("replayIQPath = %q, want /tmp/cap.iq", cfg.replayIQPath)
	}

	if !cfg.autoSweep {
		t.Error("autoSweep = false, want true")
	}

	if !cfg.biasTee {
		t.Error("biasTee = false, want true")
	}

	if cfg.gpsdAddress != "10.0.0.1:2947" {
		t.Errorf("gpsdAddress = %q, want 10.0.0.1:2947", cfg.gpsdAddress)
	}

	if cfg.batteryPath != "/sys/class/power_supply/foo/uevent" {
		t.Errorf("batteryPath = %q, want /sys/class/power_supply/foo/uevent", cfg.batteryPath)
	}

	if cfg.checkDuration != 30*time.Second {
		t.Errorf("checkDuration = %v, want 30s", cfg.checkDuration)
	}
}

func TestParseFlagsSingleDashAccepted(t *testing.T) {
	t.Parallel()

	// stdlib `flag` accepts both -foo and --foo. Pin the
	// behaviour so a future migration to a different flag
	// library doesn't silently break operator scripts.
	cfg, err := parseFlags([]string{"-auto-sweep"}, io.Discard)
	if err != nil {
		t.Fatalf("parseFlags = %v, want nil", err)
	}

	if !cfg.autoSweep {
		t.Error("autoSweep = false, want true via single-dash form")
	}
}

func TestParseFlagsUnknownFlagReturnsError(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer

	_, err := parseFlags([]string{"--nope"}, &stderr)
	if err == nil {
		t.Fatal("parseFlags(--nope) returned nil error")
	}

	if !strings.Contains(err.Error(), "flag") {
		t.Errorf("error %q should mention the flag-parse failure", err)
	}
}

func TestParseFlagsHelpReturnsError(t *testing.T) {
	t.Parallel()

	// flag.ContinueOnError treats --help as a usage error: it
	// prints the usage banner to stderr and returns
	// flag.ErrHelp. We surface that as a wrapped error so main
	// can exit non-zero.
	var stderr bytes.Buffer

	_, err := parseFlags([]string{"--help"}, &stderr)
	if err == nil {
		t.Error("parseFlags(--help) returned nil error")
	}

	if stderr.Len() == 0 {
		t.Error("--help should have written usage to stderr")
	}
}

func TestParseFlagsCheckBadDurationFormat(t *testing.T) {
	t.Parallel()

	_, err := parseFlags([]string{"--check", "not-a-duration"}, io.Discard)
	if err == nil {
		t.Error("--check with bad duration returned nil error")
	}
}
