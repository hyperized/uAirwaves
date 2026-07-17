# uAirwaves

A terminal radar for aircraft. Plug in an RTL-SDR dongle, point an antenna at the sky, and uAirwaves shows you every plane it can hear: a live radar scope, a plane list sorted by distance, flight details, and a running picture of how well your antenna is actually doing. One binary, no daemons, no internet connection needed.

It was built for the [uConsole](https://www.clockworkpi.com/uconsole) with the HackerGadgets All-In-One board (SDR, GPS, LoRa, RTC), but any Linux machine with an RTL-SDR works.

uAirwaves is also the top of a four-part, pure-Go SDR stack: a USB driver, a demodulator, a decoder, and this radar on top. Every layer works as a standalone tool with its own view of the signal, so you can follow a transmission all the way from raw samples to a blip on the scope. If you want to learn how ADS-B reception actually works, that is the point of the whole stack. See [How it works](#how-it-works).

## What you need

- An RTL-SDR dongle (RTL2832U with an R820T2 or R860 tuner) and an antenna for 1090 MHz. Alternatively, a remote receiver that serves Mode-S Beast frames over TCP, or a captured IQ file to replay.
- Linux to use the dongle directly. On macOS the USB backend is unavailable, so use a remote feed or a replay file there.
- Go 1.26 or newer to build.
- Optional: `gpsd` with a GPS receiver. Without it, uAirwaves works out where you are on its own (more on that below).

## Getting started

```sh
git clone https://github.com/hyperized/uAirwaves
cd uAirwaves
make
./uAirwaves
```

That opens the dongle and starts the TUI. Planes should appear within a minute if you have traffic overhead; commercial aircraft at cruise altitude are audible from well over 100 nm away with a decent antenna.

The process needs read/write access to the dongle's USB device node. Run it as root, or give your user access with a udev rule for the device under `/dev/bus/usb/`. The kernel's DVB driver is detached automatically, so there is nothing to blacklist or unbind first.

No dongle on this machine? Point it at a remote receiver, or replay a capture:

```sh
./uAirwaves --beast 192.168.1.50:30005    # remote demod1090 --beast-listen
./uAirwaves --replay-iq capture.iq        # decode a recorded IQ file
```

A few things worth knowing on the first run:

- Without a GPS receiver the header starts out reading `no fix`. After a few minutes of decoded traffic uAirwaves estimates its own location from the planes it hears, and the header switches to something like `EST lat 52.31, lon 4.92 (inferred ±22 nm)`. The `EST` marker means estimated: good enough to sort planes by distance, not precise coordinates. A real GPS fix takes over the moment one arrives.
- If the dongle is unplugged mid-session, the app keeps running and reconnects when it comes back. Same for the remote feed and gpsd.
- Reception weak? Try `--auto-sweep` to let it find the best gain settings, and `--bias-t` if you have a mast-mounted amplifier that takes power over the coax.

### Deploying to a uConsole

Build on your workstation and ship the binary over ssh:

```sh
make ship          # cross-builds for linux/arm64 and scps it over
```

The deploy targets read their hosts from a gitignored `.env` file next to the Makefile (`DEVICE = user@uconsole-host`, `RADIO = user@radio-host`) and tell you what to set when it is missing. `make build-aarch64` and `make build-macos` do the plain cross-builds, and `make test lint` runs what CI runs.

## Using it

The screen is split in three: the radar scope on the left, the plane list with stats and the coverage panel on the right, notifications along the bottom. The footer shows the current state of every toggle.

| Key | Action |
|----------|--------|
| `q` | Quit |
| `Esc` | Close the flight-details panel, or quit if it is already closed |
| `Enter` | Open flight details for the highlighted plane |
| `↑` / `↓` | Move through the plane list |
| `+` / `-` | Zoom the radar in and out |
| `a` | Toggle auto-scope, which fits the range to the farthest plane (on by default) |
| `h` | Toggle the heading projection (on by default; it blinks slowly so it stands apart from the trail) |
| `t` | Cycle trail length: short (about 100 s), long (everything retained, about 43 min), off |
| `m` | Toggle the contact-density heatmap overlay |
| `l` | Toggle the airport overlay, ~310 major airports drawn as `⊕ ICAO` markers |
| `c` | Cycle the coverage panel: cone, shadows, off |
| `b` | Toggle the bias-tee (always powered off again at shutdown) |
| `x` / `X` | Dismiss the current / all notifications |

While flight details are open, `+`, `-` and `a` control the mini scope inside that panel instead of the main radar. The mini scope is centred on the selected plane rather than on you, and fits itself to the plane's full trail.

Trails are coloured by the altitude flown at each fix, so a climb or descent is readable straight off the scope. The plane list only shows contacts with a resolved position.

### The coverage panel

Press `c` and uAirwaves shows what your antenna actually sees, built from every plane detected this session. The **cone** view plots detections by distance and altitude and reveals the classic pattern of a vertical antenna: a long low-elevation skirt and a silent cone directly overhead. The **shadows** view is a top-down silhouette, filled out to the farthest detection in each compass direction. A building, a hill, or your own body blocking one side shows up as a dent. Move the antenna, restart, and compare; the data is session-only.

### Flags

Everything is a flag and everything is optional. `--help` prints the full list.

| Flag | Default | Effect |
|------|---------|--------|
| `--beast HOST:PORT` | unset | Read Mode-S Beast frames from a remote demodulator instead of the local dongle. |
| `--replay-iq PATH` | unset | Decode a captured IQ file instead of live radio. Takes precedence over `--beast`. |
| `--auto-sweep` | off | Spend ~96 s trying 64 gain combinations before starting, then keep the best. Local SDR only, once per session. |
| `--bias-t` | off | Power an external amplifier through the antenna coax from the moment the dongle opens. |
| `--gpsd HOST:PORT` | `127.0.0.1:2947` | Where to find gpsd. |
| `--battery PATH` | auto-discover | Linux: a specific `power_supply` uevent file, if auto-discovery picks the wrong one. macOS reads `pmset` and ignores this. |
| `--check DURATION` | off | Headless health check instead of the TUI (see below). |

### Check mode

For scripted smoke tests: `--check` runs the receivers for a fixed window (1 s to 5 m), writes a JSON report to stdout, and exits non-zero if nothing was received. Logs go to stderr, so piping into `jq` just works:

```sh
./uAirwaves --check 10s --beast 192.168.1.50:30005 | jq .
```

Exit codes: `0` thresholds met, `1` thresholds failed (the JSON names which), `2` bad duration or a fatal worker error.

## How it works

The pipeline is four separate projects, stacked. Each is pure Go, each runs on its own, and each can show you what it is doing. That is deliberate: the usual SDR receiver is a black box that turns antenna voltage into a web page, and this stack exists to let you open that box one layer at a time.

1. [rtl2832u](https://github.com/hyperized/rtl2832u) drives the dongle over USB and produces raw IQ samples. Its `rtl-probe` tool opens the tuner, reports signal statistics, and captures IQ to a file.
2. [demod1090](https://github.com/hyperized/demod1090) turns IQ into validated Mode S frames: magnitude, preamble detection, bit decisions, CRC. It has its own TUI, replays captured IQ, and serves frames over TCP in the Beast format.
3. [modes](https://github.com/hyperized/modes) turns frames into meaning: message types, callsigns, altitudes, CPR position resolution (ICAO Annex 10 / DO-260B). Its `modes-decode` reads hex frames on stdin and prints one decoded line per frame.
4. **uAirwaves** draws the radar and adds everything stateful: aircraft tracking, GPS, self-locate, the coverage picture.

Every boundary between layers is a format you can capture, inspect, and replay: IQ files between the driver and the demodulator, hex frames or Beast TCP between the demodulator and the decoder. Break the chain wherever you are curious, look at what flows through, and feed it back in. Captured IQ replayed through `--replay-iq` decodes the same way every time, which is also how the stack tests itself on real recordings.

When you run uAirwaves normally, all four layers run inside the one binary; there is no `readsb` or `dump1090` behind it.

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

A few pieces deserve a word:

**Self-locate.** Every aircraft position broadcast also tells you something about where *you* are: if you can hear a plane, you must be within its radio horizon, a circle whose size follows from the plane's altitude. Intersect enough of those circles and your own position falls out, typically within 5 to 30 nm after a few minutes of moderate traffic. That is enough to bootstrap position decoding and distance sorting on a device with no GPS and no internet, in the middle of a field. The estimate is clearly marked in the header and steps aside as soon as gpsd delivers a real fix.

**Phantom suppression.** Some Mode S frame types carry no verifiable checksum, and random noise can decode into plausible-looking aircraft. Those frames only count once the same aircraft has been seen in a properly CRC-verified frame, which keeps the plane list at real-airspace size.

**Position without waiting.** With a known receiver position, a single position broadcast resolves to latitude and longitude immediately, instead of waiting to pair the two frame variants aircraft alternate between.

Outside the stack itself, the TUI is drawn with [tview](https://github.com/rivo/tview) and GPS comes in through [go-gpsd](https://github.com/stratoberry/go-gpsd).

## Hacking on it

```sh
go test -race -cover ./...           # matches CI
go test -race -run TestName ./pkg/x  # single test
golangci-lint run ./...              # lint (CI pins v2.8)
```

The layout, briefly:

```
main.go            — wiring: workers, ticker, panic recovery, flags
internal/ui/       — everything tview: panels, key dispatch, notifications, coverage art
internal/check/    — the --check diagnostic mode
pkg/adsb/          — ingest: SDR / BEAST / replay receivers, frame dispatch, CPR
pkg/airplane(s)/   — per-aircraft state and the thread-safe collection
pkg/airports/      — embedded airport overlay data (CC0, OurAirports)
pkg/battery/       — cross-platform battery reader (sysfs / pmset)
pkg/coverage/      — antenna reception-pattern tracker behind the coverage panel
pkg/gps/           — gpsd client with reconnect and a stall watchdog
pkg/location/      — the shared receiver position, with source and confidence
pkg/radar/         — all scope drawing: trails, heatmap, MiniView, overlays
pkg/scope/         — range arithmetic
pkg/selflocate/    — the horizon-circle intersection locator
```

Tests are split per package into `*_internal_test.go` (unexported access) and `*_external_test.go` (public surface), all parallel, all raced. Coverage is held at 100% per package with three documented exceptions: `pkg/adsb` at 99.5% (a code path that needs the physical dongle, plus one race guard), `pkg/battery` at 90.8% on macOS (the `pmset` paths run under `-tags integration`; Linux CI reports 100%), and the root package at 86.6% (`main()` and hardware wiring). Hot paths carry allocation-reporting benchmarks: `go test -bench . -run '^$' ./...`.

## License

[Business Source License 1.1](LICENSE): free for non-commercial use, converts to Apache 2.0 on the Change Date. Contact the licensor for commercial licensing.
