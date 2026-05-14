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

| Variable              | Default                                            | Effect |
|-----------------------|----------------------------------------------------|--------|
| `GPSD_ADDRESS`        | `127.0.0.1:2947`                                   | `gpsd` socket. |
| `BATTERY_PATH`        | `/sys/class/power_supply/axp20x-battery/uevent`    | `power_supply` uevent file. Missing/unreadable is non-fatal — the watcher logs a warning and keeps polling, so a transient udev race at boot doesn't take the app down. |
| `UAIRWAVES_REPLAY_IQ` | unset                                              | Path to a captured IQ file. When set, the SDR backend is bypassed and the file is streamed through the demod chain instead — useful for deterministic on-device replay and off-line A/B testing. The receiver returns `adsb.ErrReplayEnded` on EOF, which `Stream` converts to a clean shutdown. |

The ADS-B path no longer takes an `ADSB_ADDRESS` — it owns the SDR directly.

## Repository layout

```
main.go                 — wiring only (workers, ticker, panic recovery)
internal/ui/            — pure UI helpers extracted out of main for testability (100% covered)
pkg/adsb/               — Receiver/Demodulator factories, frame dispatch, CPR resolution
pkg/airplane/           — single-plane state + Snapshot (lock-free read path)
pkg/airplanes/          — thread-safe map; Sorted() returns []Snapshot
pkg/battery/            — power_supply uevent watcher (resilient to transient read errors)
pkg/gps/                — gpsd client with reconnect-on-hangup (errSessionClosed sentinel)
pkg/location/           — receiver lat/lon
pkg/radar/              — scope, plane rendering, heatmap, trails
pkg/scope/              — auto-scope range arithmetic
```

## Testing

```sh
go test -race -cover ./...           # matches CI
go test -race -run TestName ./pkg/x  # single test
```

Internal/external test split per package (`*_internal_test.go` for unexported access, `*_external_test.go` for the public surface). All package tests run with `-race` and `t.Parallel()`.

## Status

ADS-B ingest is an in-process SDR pipeline as of the latest commit. The previous `readsb`-driven JSON-over-TCP consumer has been retired; uAirwaves no longer needs `readsb` running on the host.
