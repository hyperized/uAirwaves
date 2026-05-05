# uAirwaves

A single-binary TUI for the uConsole with a HackerGadgets Antenna board (GPS / SDR / LoRA / RTC) for monitoring the aviation airwaves.

## What it does

- **ADS-B** — opens the RTL-SDR dongle directly (no `readsb`, no `dump1090`, no daemon), demodulates 1090 MHz Mode S, decodes every Downlink Format the spec defines, and renders a live aircraft list with callsign / altitude / squawk / speed / heading / position / vertical rate.
- **GPS** — talks to `gpsd` and pins a "you-are-here" reference. The ADS-B side reads it on every position frame so locally-unambiguous CPR resolves to lat/lon from a single airborne-position broadcast (no even/odd pairing wait).
- **Plotting** — radar / scope panels rendered in [`tview`](https://github.com/rivo/tview).
- **Battery + system status** — secondary panels.

## Architecture

```
RTL-SDR dongle
   │ USB (usbfs)
   ▼
github.com/hyperized/rtl2832u   — pure-Go RTL2832U + R820T/R860 driver
   │ IQ samples
   ▼
github.com/hyperized/demod1090/demod  — magnitude / preamble / PPM / CRC-24
   │ validated Mode S frames
   ▼
github.com/hyperized/modes      — DF dispatch, ME field decoders, CPR
   │ typed messages
   ▼
pkg/adsb                        — per-ICAO aggregation into pkg/airplanes
   │
   ▼
pkg/airplanes  ─────────►  pkg/scope  /  pkg/radar  /  TUI panel list
```

The whole stack is pure Go, no CGo on the deploy target, no external daemons.

## Target

```sh
Linux uconsole 5.10.17-v8+ #5 SMP PREEMPT Thu Jun 29 13:36:01 CST 2023 aarch64 GNU/Linux
```

(uConsole + Raspberry Pi CM4 + HackerGadgets All-In-One: RTL2838 dongle with R820T2 tuner + GPS + battery monitor.)

Local development is `darwin/arm64` — the SDR layer's non-Linux backend returns `ErrUnsupportedPlatform`, so `go build ./...` works but you can't open a dongle on a Mac.

## Build

```sh
make all              # builds uAirwaves-aarch64 and scp's it to the uConsole
make build-aarch64    # cross-build only
make build-macos      # local build for development
```

## Components

- [`github.com/hyperized/rtl2832u`](https://github.com/hyperized/rtl2832u) — radio driver.
- [`github.com/hyperized/modes`](https://github.com/hyperized/modes) — ICAO Annex 10 Vol IV / DO-260B decoder.
- `github.com/hyperized/demod1090` — Mode S demodulator (currently consumed via a local-replace until publication).
- [`github.com/rivo/tview`](https://github.com/rivo/tview) — TUI framework.
- [`github.com/stratoberry/go-gpsd`](https://github.com/stratoberry/go-gpsd) — `gpsd` client.

## Configuration

Environment variables (all optional):

| Variable        | Default                  | Effect |
|-----------------|--------------------------|--------|
| `GPS_ADDRESS`   | `localhost:2947`         | `gpsd` socket. |

The ADS-B path no longer takes an `ADSB_ADDRESS` — it owns the SDR directly.

## Status

ADS-B ingest is an in-process SDR pipeline as of the latest commit. The previous `readsb`-driven JSON-over-TCP consumer has been retired; uAirwaves no longer needs `readsb` running on the host.
