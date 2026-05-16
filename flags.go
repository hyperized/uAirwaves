package main

import (
	"flag"
	"fmt"
	"io"
	"time"
)

// cliConfig is the parsed CLI surface for uAirwaves. Pure data:
// no behaviour, no defaults beyond what the flag declarations
// set. main owns translating these into adsb.Option /
// runCheckMode / watcher arguments.
//
// Empty-string defaults for gpsdAddress and batteryPath keep
// the historical "library picks the default when caller passes
// empty" contract — gps.New() and battery.Watch() both treat an
// empty path/address as "use my baked-in default". Setting an
// explicit non-empty value here overrides that default.
type cliConfig struct {
	beastAddress  string
	replayIQPath  string
	autoSweep     bool
	gpsdAddress   string
	batteryPath   string
	checkDuration time.Duration
}

// parseFlags wires the Go flag package to a cliConfig. Uses
// ContinueOnError so main can map a parse failure to a
// non-zero exit code without flag.Parse calling os.Exit on its
// own.
//
// The flag set's writer is stderr so --help and parse errors
// both land on the same stream the operator already watches for
// startup diagnostics.
func parseFlags(args []string, stderr io.Writer) (cliConfig, error) {
	var cfg cliConfig

	flagSet := flag.NewFlagSet("uAirwaves", flag.ContinueOnError)
	flagSet.SetOutput(stderr)

	flagSet.StringVar(&cfg.beastAddress, "beast", "",
		"host:port of a remote demod1090 --beast-listen (or any BEAST-emitting source). "+
			"When set, uAirwaves consumes Mode-S Beast over TCP instead of opening the local SDR.")
	flagSet.StringVar(&cfg.replayIQPath, "replay-iq", "",
		"path to a captured IQ file; bypasses the SDR and streams the file through the demod chain. "+
			"Overrides --beast when both are set.")
	flagSet.BoolVar(&cfg.autoSweep, "auto-sweep", false,
		"run a 3D LNA×Mix×VGA gain sweep (64 cells, ~96 s) before the first frame and apply "+
			"the winning cell. Local-SDR mode only; ignored under --beast or --replay-iq.")
	flagSet.StringVar(&cfg.gpsdAddress, "gpsd", "",
		"gpsd address as host:port (empty = use gps library default, currently 127.0.0.1:2947)")
	flagSet.StringVar(&cfg.batteryPath, "battery", "",
		"path to the power_supply uevent file (empty = use battery library default, "+
			"currently /sys/class/power_supply/axp20x-battery/uevent)")
	flagSet.DurationVar(&cfg.checkDuration, "check", 0,
		"non-TUI diagnostic mode: run the GPS + ADSB workers for this duration (1s..5m), "+
			"write a JSON report to stdout, exit non-zero on threshold failure. 0 = TUI mode.")

	if err := flagSet.Parse(args); err != nil {
		return cfg, fmt.Errorf("uAirwaves: flag parse: %w", err)
	}

	return cfg, nil
}
