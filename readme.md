# uAirwaves

A single-binary TUI for the uConsole with a HackerGadgets Antenna board (GPS / SDR / LoRa / RTC) for monitoring the aviation airwaves.

## What it does

- **ADS-B** — two ingest modes:
  - **Local SDR** (default): opens the RTL-SDR dongle directly (no `readsb`, no `dump1090`, no daemon), demodulates 1090 MHz Mode S, and decodes every Downlink Format the spec defines. If the dongle drops mid-session (USB unplug, bus reset), the stream reopens it with exponential backoff instead of taking the app down.
  - **BEAST-over-TCP**: with `--beast host:port`, consume Mode-S Beast frames from a remote demodulator (e.g. another box running `demod1090 --beast-listen :30005`) over the wire. Reconnects with exponential backoff.

  Both paths fold per-ICAO state into a live aircraft list with callsign / altitude / squawk / speed / heading / position / vertical rate.

- **GPS** — talks to `gpsd` and pins a "you-are-here" reference. The ADS-B side reads it on every position frame so locally-unambiguous CPR resolves to lat/lon from a single airborne-position broadcast (no even/odd pairing wait). A watchdog forces a reconnect if no TPV report arrives in 30 s (silent socket stall), and every mode change (e.g. `no fix → 3D fix`) is surfaced in the in-app notification bar.
- **Self-locate (GPS fallback)** — when `gpsd` isn't reachable or hasn't produced a fix in 30 s, the receiver derives its own position from the ADS-B stream it's already decoding: every aircraft broadcast with a CPR-resolved lat/lon and a usable altitude constrains the receiver to lie within that aircraft's radio horizon, and the recursive intersection of all such circles converges on the operator's location (typically ±5–30 nm after a few minutes of moderately busy traffic). Internet-free, GPS-free, and good enough to bootstrap CPR locally-unambiguous decoding on a cold-start uConsole in the field. The first applied fix is surfaced in the notification bar; gpsd reasserts authority the moment it produces a real fix.
- **Phantom-ICAO suppression** — address-parity Mode S frames (DF 0/4/5/16/20/21) only register a contact once the ICAO has been seen in a CRC-verified DF 11/17/18 frame; random noise stops minting ghosts. Backed by [`demod1090/icaofilter`](https://github.com/hyperized/demod1090/tree/main/icaofilter).
- **Plotting** — radar / scope panels rendered in [`tview`](https://github.com/rivo/tview). Trails are coloured by the altitude flown at each fix (so the climb/descent profile is readable straight off the scope); heading lines are white and slow-blink so the projection visually separates from the steady trail dots. Trail length is a tri-state cycle: off, short (last 10 fixes ≈ 100 s of history) or long (every retained fix — retention is capped at 256 fixes per plane, about 43 minutes at the one-fix-per-10 s rate). An optional heatmap overlay tracks contact density.
- **Airport overlay** — ~310 major civil airports (sourced from the OurAirports public-domain dataset) render as dim cyan `⊕ ICAO` markers on the scope, filtered to those inside the current range. Toggle with `l`.
- **Flight details** — press `Enter` on the right-column plane list to open a detail panel for the highlighted contact: identity, altitude (with FL), heading, velocity (kt + km/h), vertical rate, position, distance + great-circle bearing from the receiver, age, message count, plus a single-flight mini-scope inset centred on the plane (not the receiver) that auto-fits to the full known trail. The mini scope also draws the receiver as a regular `X` crosshair when its position falls inside the rendered range. While the details panel is open the `+/-` (zoom) and `a` (autoscope) keys redirect from the main radar to the mini view so the operator can dig in without leaving the panel. `Esc` returns to the radar.
- **Stats panel** — running tally of tracked / positioned contacts, nearest/farthest/highest with session peaks, frames-per-second with a session-peak appendix, and the rolling callsign-decode ratio.
- **Battery + system status** — secondary panels. Battery reads are cross-platform: sysfs `power_supply` on Linux (auto-discovered, not pinned to the uConsole's `axp20x-battery`) and `pmset` on macOS.

## Architecture

```
                                ┌──────────────────────────────────┐
                                │  --beast set?                    │
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
git clone https://github.com/hyperized/uAirwaves
cd uAirwaves
make                  # builds ./uAirwaves for the machine you're on
./uAirwaves           # opens the local RTL-SDR and starts the TUI
```

Cross-compile and deploy targets:

```sh
make build-aarch64    # cross-build for the uConsole (linux/arm64)
make build-macos      # macOS build for development
make ship             # build-aarch64 + scp to the uConsole
make test lint        # what CI runs
```

`ship`, `smoke`, and the other deploy targets read their hosts from a
gitignored `.env` next to the Makefile (`DEVICE = user@uconsole-host`,
`RADIO = user@radio-host`); they tell you what to set when it's missing.

## Keyboard shortcuts

| Key      | Action |
|----------|--------|
| `q` | Quit |
| `Esc` | Close flight-details panel if open; otherwise quit |
| `Enter` | Open the flight-details panel for the highlighted plane in the right-column list |
| `↑` / `↓` | Navigate the plane list (cursor index stays put across the per-tick rebuild; no wrap at the ends) |
| `+` / `-` | Increase / decrease scope range (main radar; redirects to the mini view when the details panel is open) |
| `a` | Toggle auto-scope (fits scope to the farthest plane, rounded up to the next 20 nm; on by default — when details open, toggles the mini view's auto-fit instead) |
| `h` | Toggle heading indicator (on by default; line slow-blinks so it's distinguishable from the trail) |
| `t` | Cycle trail length: short (default, last ~100 s) → long (every fix until the plane is pruned) → off |
| `m` | Toggle heatmap overlay (off by default) |
| `l` | Toggle airport overlay (on by default) |
| `b` | Toggle bias-tee on the SDR (powered off automatically on app shutdown) |
| `x` / `X` | Dismiss the current / all queued notification-bar messages |

The right-column list always shows only contacts with a resolved CPR position — position-less shadow planes never appear (this used to be the `p` toggle, now hard-wired on).

Current state of every toggle is shown along the footer line.

## Components

- [`github.com/hyperized/rtl2832u`](https://github.com/hyperized/rtl2832u) — pure-Go RTL2832U + R820T2/R860 driver. Auto-detaches the kernel `dvb_usb_rtl28xxu` driver via `USBDEVFS_DISCONNECT_CLAIM` (kernel ≥ 3.18), so no `unbind` sysfs ritual on boot.
- [`github.com/hyperized/modes`](https://github.com/hyperized/modes) — ICAO Annex 10 Vol IV / DO-260B decoder (pure-stateless decoding).
- [`github.com/hyperized/demod1090`](https://github.com/hyperized/demod1090) — Mode S demodulator + Beast wire format. Sub-packages used here:
  - `demod` — IQ → Mode S frames.
  - `beast` — Beast frame encode + streaming `Reader` for incoming TCP feeds.
  - `icaofilter` — TTL'd ICAO whitelist (the `icao_filter` dump1090/readsb use to kill address-parity phantoms).
- [`github.com/rivo/tview`](https://github.com/rivo/tview) — TUI framework.
- [`github.com/stratoberry/go-gpsd`](https://github.com/stratoberry/go-gpsd) — `gpsd` client.

## Configuration

Everything is a CLI flag (all optional; `--help` prints the canonical list):

| Flag | Default | Effect |
|------|---------|--------|
| `--beast HOST:PORT` | unset | Consume Mode-S Beast frames from a remote demodulator (e.g. `demod1090 --beast-listen :30005`) instead of opening the local SDR. Reconnects with exponential backoff (1 s base, 30 s cap). |
| `--replay-iq PATH` | unset | Stream a captured IQ file through the demod chain instead of the SDR — deterministic on-device replay and off-line A/B testing. The receiver returns `adsb.ErrReplayEnded` on EOF, which `Stream` converts to a clean shutdown. Precedence: `--replay-iq` > `--beast` > local SDR. |
| `--auto-sweep` | off | Run a 3D LNA × Mixer × VGA gain sweep (64 cells, ~96 s) before the first frame and apply the winning cell. Local-SDR mode only; runs once per session, not again on a reconnect. |
| `--bias-t` | off | Power the dongle's bias-tee at open time (feeds DC up the coax for an external LNA / SAW filter). Local-SDR mode only; the `b` key toggles it live, and shutdown always powers it off. |
| `--gpsd HOST:PORT` | `127.0.0.1:2947` | Override the `gpsd` socket. |
| `--battery PATH` | auto-discover | **Linux:** explicit `power_supply` uevent file; empty auto-discovers the first `Battery`-type device under `/sys/class/power_supply`. **macOS:** ignored — battery state comes from `pmset`. Missing/unreadable is non-fatal: the watcher logs a warning and keeps polling, so a transient udev race at boot doesn't take the app down. |
| `--check DURATION` | `0` (TUI mode) | Non-TUI diagnostic: run the GPS + ADSB workers for the window (1 s – 5 min), write a JSON report to stdout, exit non-zero on threshold failure. |

### Check mode

A non-TUI diagnostic, gated by `--check <duration>`. The duration is parsed with `time.ParseDuration` and must fall in `[1s, 5m]`. JSON is written to stdout; structured logs stay on stderr, so `jq` works straight through:

```sh
./uAirwaves --check 10s --beast 192.168.1.50:30005 | jq .
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
                          ICAO list)
pkg/battery/            — cross-platform battery watcher: sysfs power_supply (Linux,
                          auto-discovered) / pmset (macOS), resilient to transient read errors
pkg/gps/                — gpsd client with reconnect-on-hangup + 30 s TPV watchdog
pkg/location/           — receiver lat/lon
pkg/radar/              — scope, plane rendering, heatmap, altitude-coloured trails,
                          slow-blink heading projection, trail-length cycle
                          (off/short/long), auto-scope fit-to-farthest,
                          plane-centred MiniView with auto-fit + manual zoom and
                          receiver crosshair when in range, airport overlay
pkg/scope/              — scope range arithmetic
pkg/selflocate/         — horizon-circle intersection self-locator: derives receiver
                          position from CPR-decoded aircraft fixes when gpsd is
                          unavailable. Passive (Observe + Estimate); main.go runs
                          the coordinator that decides GPS-vs-self-locate.
```

## Testing

```sh
go test -race -cover ./...           # matches CI
go test -race -run TestName ./pkg/x  # single test
```

Internal/external test split per package (`*_internal_test.go` for unexported access, `*_external_test.go` for the public surface). All package tests run with `-race` and `t.Parallel()`. Coverage budget is 100% per package, with three documented exceptions:

- `pkg/adsb` (99.5%) — the live-silicon `defaultReceiverFactory` open path and a transient `Ensure → Get` race guard in `handleFrame` that the suite can't deterministically hit.
- `pkg/battery` (90.8% on macOS) — the `pmset`-backed entrypoints sit behind the `integration` build tag; `go test -tags integration ./pkg/battery/...` covers them, and Linux CI reports 100% by default.
- the root package (86.6%) — `main()` itself, the hardware receiver factory, and the sweep-cadence branch need a terminal or a dongle; everything extractable is extracted and tested.

Hot paths carry benchmarks (`go test -bench . -run '^$' ./...`): snapshotting, distance-sorting, radar drawing, and frame handling all report allocations.

## Status

Stable in-process SDR pipeline (no `readsb`, no `dump1090`) with an alternative BEAST-over-TCP ingest mode for remote-receiver deployments. Address-parity phantom suppression via the shared `demod1090/icaofilter` package brings the tracked-list down to roughly real-airspace size; kernel DVB driver fights are handled in `rtl2832u` itself so the binary runs out of the box on a fresh Linux boot.

## License

[Business Source License 1.1](LICENSE): free for non-commercial use, converts to Apache 2.0 on the Change Date. Contact the licensor for commercial licensing.
