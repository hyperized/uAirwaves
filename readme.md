# uAirwaves

A single-binary TUI for the uConsole with a HackerGadgets Antenna board (GPS / SDR / LoRA / RTC) for monitoring the aviation airwaves.

## What it does

- **ADS-B** — two ingest modes:
  - **Local SDR** (default): opens the RTL-SDR dongle directly (no `readsb`, no `dump1090`, no daemon), demodulates 1090 MHz Mode S, and decodes every Downlink Format the spec defines.
  - **BEAST-over-TCP**: when `BEAST_ADDRESS=host:port` is set, consume Mode-S Beast frames from a remote demodulator (e.g. another box running `demod1090 --beast-listen :30005`) over the wire. Reconnects with exponential backoff.

  Both paths fold per-ICAO state into a live aircraft list with callsign / altitude / squawk / speed / heading / position / vertical rate.

- **GPS** — talks to `gpsd` and pins a "you-are-here" reference. The ADS-B side reads it on every position frame so locally-unambiguous CPR resolves to lat/lon from a single airborne-position broadcast (no even/odd pairing wait). A watchdog forces a reconnect if no TPV report arrives in 30 s (silent socket stall), and every mode change (e.g. `no fix → 3D fix`) is surfaced in the in-app notification bar.
- **Phantom-ICAO suppression** — address-parity Mode S frames (DF 0/4/5/16/20/21) only register a contact once the ICAO has been seen in a CRC-verified DF 11/17/18 frame; random noise stops minting ghosts. Backed by [`demod1090/icaofilter`](https://github.com/hyperized/demod1090/tree/main/icaofilter).
- **Plotting** — radar / scope panels rendered in [`tview`](https://github.com/rivo/tview). Trails are coloured by the altitude flown at each fix (so the climb/descent profile is readable straight off the scope); heading lines are white (a projection, not a flown path). An optional heatmap overlay tracks contact density.
- **Airport overlay** — ~310 major civil airports (sourced from the OurAirports public-domain dataset) render as dim cyan `⊕ ICAO` markers on the scope, filtered to those inside the current range. Toggle with `l`.
- **Flight details** — press `Enter` on the right-column plane list to open a detail panel for the highlighted contact: identity, altitude (with FL), heading, velocity (kt + km/h), vertical rate, position, distance + great-circle bearing from the receiver, age, message count, plus a single-flight mini-scope inset showing only that plane and its trail. `Esc` returns to the radar.
- **Battery + system status** — secondary panels.

## Architecture

```
                                ┌──────────────────────────────────┐
                                │  BEAST_ADDRESS set?              │
                                └──────────────────────────────────┘
                                            yes │              │ no
                                                ▼              ▼
RTL-SDR dongle                          remote BEAST     RTL-SDR dongle
   │ USB (usbfs)                        TCP stream          │ USB (usbfs)
   ▼                                        │               ▼
github.com/hyperized/rtl2832u          demod1090/beast    github.com/hyperized/rtl2832u
   │  pure-Go RTL2832U + R820T/R860         │  Reader        │  pure-Go RTL2832U + R820T/R860
   │  auto-detaches kernel DVB driver       │                │
   │  via USBDEVFS_DISCONNECT_CLAIM         │                │
   ▼                                        │                ▼
github.com/hyperized/demod1090/demod        │            (same demod chain as the local path)
   │  magnitude / preamble / PPM / CRC-24   │                │
   ▼                                        │                │
   │  validated Mode S frames               ▼                │
   └────────────────────►   github.com/hyperized/demod1090/icaofilter
                                            │  address-parity gate
                                            ▼
                            github.com/hyperized/modes — DF dispatch, ME decoders, CPR
                                            │
                                            ▼
                            pkg/adsb — per-ICAO aggregation into pkg/airplanes
                                            │
                                            ▼
                            pkg/airplanes ─► pkg/scope / pkg/radar / TUI panel list
```

The whole stack is pure Go, no CGo on the deploy target, no external daemons. The kernel DVB driver fight is handled in-process by `rtl2832u` v0.1.4+ — no `make unbind` ritual on a fresh boot.

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

## Keyboard shortcuts

| Key      | Action |
|----------|--------|
| `q` | Quit |
| `Esc` | Close flight-details panel if open; otherwise quit |
| `Enter` | Open the flight-details panel for the highlighted plane in the right-column list |
| `↑` / `↓` | Navigate the plane list (cursor index stays put across the per-tick rebuild; no wrap at the ends) |
| `+` / `-` | Increase / decrease scope range |
| `a` | Toggle auto-scope (fits scope to the farthest plane, rounded up to the next 20 nm; on by default) |
| `h` | Toggle heading indicator (on by default) |
| `t` | Toggle trail / position history (on by default) |
| `m` | Toggle heatmap overlay (off by default) |
| `l` | Toggle airport overlay (on by default) |
| `b` | Toggle bias-tee on the SDR (powered off automatically on app shutdown) |
| `x` / `X` | Dismiss the current / all queued notification-bar messages |

The right-column list always shows only contacts with a resolved CPR position — position-less shadow planes never appear (this used to be the `p` toggle, now hard-wired on).

Current state of every toggle is shown along the footer line.

## Components

- [`github.com/hyperized/rtl2832u`](https://github.com/hyperized/rtl2832u) — pure-Go RTL2832U + R820T2/R860 driver. Auto-detaches the kernel `dvb_usb_rtl28xxu` driver via `USBDEVFS_DISCONNECT_CLAIM` (kernel ≥ 3.18), so no `unbind` sysfs ritual on boot.
- [`github.com/hyperized/modes`](https://github.com/hyperized/modes) — ICAO Annex 10 Vol IV / DO-260B decoder (pure-stateless decoding).
- `github.com/hyperized/demod1090` — Mode S demodulator + Beast wire format. Consumed via a local-replace until publication. Sub-packages used here:
  - `demod` — IQ → Mode S frames.
  - `beast` — Beast frame encode + streaming `Reader` for incoming TCP feeds.
  - `icaofilter` — TTL'd ICAO whitelist (the `icao_filter` dump1090/readsb use to kill address-parity phantoms).
- [`github.com/rivo/tview`](https://github.com/rivo/tview) — TUI framework.
- [`github.com/stratoberry/go-gpsd`](https://github.com/stratoberry/go-gpsd) — `gpsd` client.

## Configuration

Environment variables (all optional):

| Variable              | Default                                            | Effect |
|-----------------------|----------------------------------------------------|--------|
| `GPSD_ADDRESS`        | `127.0.0.1:2947`                                   | `gpsd` socket. |
| `BATTERY_PATH`        | `/sys/class/power_supply/axp20x-battery/uevent`    | `power_supply` uevent file. Missing/unreadable is non-fatal — the watcher logs a warning and keeps polling, so a transient udev race at boot doesn't take the app down. |
| `BEAST_ADDRESS`       | unset                                              | `host:port` of a remote BEAST TCP server. When set, uAirwaves consumes Mode-S Beast frames from that server instead of opening the local SDR. Reconnects with exponential backoff (1 s base, 30 s cap). Precedence: `UAIRWAVES_REPLAY_IQ > BEAST_ADDRESS > local SDR`. |
| `UAIRWAVES_REPLAY_IQ` | unset                                              | Path to a captured IQ file. When set, the SDR backend is bypassed and the file is streamed through the demod chain instead — useful for deterministic on-device replay and off-line A/B testing. The receiver returns `adsb.ErrReplayEnded` on EOF, which `Stream` converts to a clean shutdown. |
| `UAIRWAVES_CHECK`     | unset                                              | Go duration (1 s – 5 min). When set, uAirwaves skips the TUI, runs the GPS + ADSB workers for the window, writes a JSON report to stdout, and exits non-zero when default thresholds (≥ 1 frame, ≥ 1 tracked plane) are not met. Useful as a deployment smoke test. |

The ADS-B path no longer takes an `ADSB_ADDRESS` — it owns the SDR directly (or speaks BEAST natively over `BEAST_ADDRESS`).

### Check mode

A non-TUI diagnostic, gated by `UAIRWAVES_CHECK=<duration>`. The duration is parsed with `time.ParseDuration` and clamped to `[1s, 5m]`. JSON is written to stdout; structured logs stay on stderr, so `jq` works straight through:

```sh
UAIRWAVES_CHECK=10s BEAST_ADDRESS=192.168.1.50:30005 ./uAirwaves | jq .
```

Exit codes:

| Code | Meaning |
|------|---------|
| `0`  | Window completed, thresholds met. |
| `1`  | Window completed, one or more thresholds failed (`failures` array in the JSON names them). |
| `2`  | Bad duration, or a worker returned a fatal error. |

## Repository layout

```
main.go                 — wiring only (workers, ticker, panic recovery, flag precedence)
internal/ui/            — pure UI helpers extracted out of main for testability
   ui.go                  — header colours, key dispatch, plane list / footer
   flight_details.go      — per-plane detail panel renderer (identity, geometry, bearing)
   selection.go           — picked-plane state for the details panel (index → ICAO mapping)
   notifications.go       — in-app notification queue + slog handler + bar renderer
   stats.go               — stats panel aggregation
   worker.go              — LaunchWorker (panic recovery + WaitGroup wiring)
pkg/adsb/               — Receiver/Demodulator factories, frame dispatch, CPR resolution
   adsb.go                — Stream() branches between SDR and BEAST paths; bias-tee
                            powered off in the shutdown defer so external LNAs aren't left hot
   beast.go               — BEAST-over-TCP consumer (dial, reconnect, handleFrame bridge)
   handle.go              — per-DF dispatch into airplane state via icaofilter.Admit
   replay.go              — --replay-iq file-backed receiver
pkg/airplane/           — single-plane state + Snapshot (lock-free read path). PositionEntry
                          captures altitude at append time so trails can colour by flight level.
pkg/airplanes/          — thread-safe map; Sorted() returns []Snapshot
pkg/airports/           — embedded ~310-airport overlay set (CC0 OurAirports data, hand-picked
                          ICAO list). Regen workflow + scripts live in internal/gen/.
pkg/battery/            — power_supply uevent watcher (resilient to transient read errors)
pkg/gps/                — gpsd client with reconnect-on-hangup + 30 s TPV watchdog
pkg/location/           — receiver lat/lon
pkg/radar/              — scope, plane rendering, heatmap, altitude-coloured trails,
                          auto-scope fit-to-farthest, single-flight MiniView, airport overlay
pkg/scope/              — scope range arithmetic
```

## Testing

```sh
go test -race -cover ./...           # matches CI
go test -race -run TestName ./pkg/x  # single test
```

Internal/external test split per package (`*_internal_test.go` for unexported access, `*_external_test.go` for the public surface). All package tests run with `-race` and `t.Parallel()`. Coverage budget is 100% per package; the only exception is `pkg/adsb` at 99% — the two uncovered branches are the live-silicon `defaultReceiverFactory` open path and a transient `Ensure → Get` race in `handleFrame` that the test suite can't deterministically hit.

## Status

Stable in-process SDR pipeline (no `readsb`, no `dump1090`) with an alternative BEAST-over-TCP ingest mode for remote-receiver deployments. Address-parity phantom suppression via the shared `demod1090/icaofilter` package brings the tracked-list down to roughly real-airspace size; kernel DVB driver fights are handled in `rtl2832u` itself so the binary runs out of the box on a fresh Linux boot.
