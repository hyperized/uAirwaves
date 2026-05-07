# uAirwaves Improvement Plan

## SDR Stack Improvements (2026-05-07)

Added after the rtl2832u + demod1090 + modes pure-Go pipeline went
end-to-end on silicon. Findings benchmarked against readsb on the
same hardware (Jetvision antenna direct into uConsole-attached
RTL-SDR, no LNA): readsb produces ~21 000 accepted Mode S frames
per minute; our pipeline with the just-shipped permissive +
error-correction defaults reaches ~1 000 — still 20× behind. The
items below are the path to closing that gap.

### Yield (callsigns/sec, traffic visibility)

| # | What | Where | Why it matters |
|---|---|---|---|
| Y1 | **demod1090 preamble detector** — readsb fires ~3 600 events/sec on the same antenna, ours ~16/sec even with `--permissive-preamble`. Order-of-magnitude detection gap. | `~/repos/demod1090/demod/preamble.go` | Closes the residual 20× gap to readsb. Biggest single yield lever. |
| Y2 | **demod1090 bit decoder marginal-SNR yield** — long-frame CRC fails far more than short-frame CRC at the same SNR; dump1090's bit-decision logic uses neighbouring-sample slope tests our committed code skips. | `~/repos/demod1090/demod/bits.go` | Long frames carry the ADS-B payload. Biggest ident win. |
| Y3 | **Triage demod1090 WIP** — 477 lines of in-flight changes that currently produce 0 frames. Likely a real attempt at Y1 + Y2 that's mid-debug. | `~/repos/demod1090/demod/{preamble,bits,demodulator}.go` | Either the work resumes or it gets parked cleanly. |
| Y4 | **rtl2832u IF/LO alignment WIP** — uncommitted `tuner_r860_setfreq.go` adds the LO offset (`rfHz + intFreqHz`) so the analogue IF filter and the LO agree, plus a new `tuner_r860_bandwidth.go` ports `r82xx_set_bandwidth`. Probably correct (matches librtlsdr); needs verification + commit + tag. | `~/repos/rtl2832u/tuner_r860_setfreq.go`, `tuner_r860_bandwidth.go` | One of the structural reasons readsb's chain decodes more than ours. |

### Operational (run / debug / iterate)

| # | What | Where |
|---|---|---|
| O1 | **Expose demod knobs from uAirwaves** — env vars `UAIRWAVES_PERMISSIVE`, `UAIRWAVES_ERROR_CORRECTION` so the operator can A/B without rebuild. Currently always-on by default; sometimes strict mode is what you want for noise diagnosis. | `pkg/adsb/adsb.go` `defaultDemodulatorFactory` |
| O2 | **Auto-unbind kernel driver on uAirwaves start** — currently fails with a verbose diagnostic; could run the unbind path itself if root, or print a one-line copy-pasteable command. | `main.go` startup, or a `pre-start.sh` helper |
| O3 | **`make compare-readsb` smoke target** — capture once, replay through both demod1090 and readsb (where `readsb --device-type ifile` works), print yield delta. Closes the loop on yield experiments. | `Makefile` |
| O4 | **rtl-probe `-gain N` VGA-default override** — librtlsdr ladder pins VGA at +16 dB; for tuned antennas +24..40 dB is needed. Add a `-vga-target dB` flag or a `-gain-profile=tuned-antenna` preset. | `rtl2832u/cmd/rtl-probe/main.go` |
| O5 | **Stats panel: per-DF breakdown** — currently shows total + IDs only. Adding `DF17/18 N/min · DF4/5 N/min · noise N/min` gives reception-quality at-a-glance. | `main.go updateStatsPanel`, plus per-DF counters in `pkg/adsb/handle.go` |
| O6 | **uAirwaves TUI hangs over ssh without TTY** — operational; `ssh -t` works around but the binary should detect and exit clean instead of blocking on tview's terminal init. | `main.go` startup |

### Test / coverage / hygiene

| # | What | Where |
|---|---|---|
| T1 | **pkg/adsb to 100%** — currently 87.6%. The remaining gap is per-DF `apply*` error sub-branches; needs crafted invalid-frame fixtures for each DF's "decoder errored" path. | `pkg/adsb/handle.go` + tests |
| T2 | **Pre-existing `varnamelen` lint hit** — `validCallsign(s string)` is the only red on `golangci-lint run ./...`. One-character rename. | `pkg/adsb/handle.go:29` |
| T3 | **rtl2832u pre-existing `wsl_v5` lint hits** — two whitespace warnings in the new `tuner_r860_bandwidth.go`. | `~/repos/rtl2832u/tuner_r860_bandwidth.go:115,121` |

### Documentation

| # | What | Where |
|---|---|---|
| D1 | **README.md** — predates rtl-probe / modes-decode / `UAIRWAVES_REPLAY_IQ`. Document the smoke chain so a fresh checkout knows how to verify on silicon. | `readme.md` |
| D2 | **Document the AGC-disabled-by-design quirk** — the rtl2832u driver intentionally disables the demod's RF/IF AGC loop on R820T silicon; a future reader might try to "fix" the disable. Codify the rationale in a top-of-file comment in `init_chip.go` (already partially documented in `c445dd7`). | `~/repos/rtl2832u/init_chip.go` |

### Suggested order

1. **O1** (knob exposure, ~30 min) — operator agility before any other work.
2. **Y4** (verify + commit your IF/LO WIP — likely already correct).
3. **Y1 + Y2 + Y3** as one focused demod-improvement session.
4. Polish: **O5**, **D1**, **T1**.

---

## Bug Fixes

### 1. `GetICAO()` uses wrong format verb
**File:** `pkg/airplane/airplane.go:117`
**Issue:** `fmt.Sprintf("%06X", a.icao)` uses `%X` (hex integer) on a `string` field. This produces the hex-encoded bytes of the string rather than the ICAO code itself.
**Fix:** Return `a.icao` directly (it's already a hex string from readsb JSON `hex` field) or use `%s`.

### 2. Emergency flag never clears
**File:** `pkg/airplane/airplane.go:214-224`
**Issue:** `WithSquawk` sets `emergency = true` when squawk is 7500/7600/7700 but never resets it to `false` when the squawk changes to a non-emergency code. An aircraft that briefly squawks 7700 then reverts to 1200 remains marked as emergency forever.
**Fix:** Set `airplane.emergency = false` before the emergency check, or add an explicit else branch.

### 3. ADSB connection has no dial timeout
**File:** `pkg/adsb/adsb.go:246-248`
**Issue:** `net.Dialer{}` has zero timeout, so `connect()` can hang indefinitely if the readsb service is unreachable.
**Fix:** Set a reasonable dial timeout (e.g., `net.Dialer{Timeout: 10 * time.Second}`).

### 4. Error channel size of 1 can block senders
**File:** `main.go:98`
**Issue:** `errChan` has buffer size 1, but three goroutines (battery, GPS, ADSB) can all error. If two fail simultaneously, the second sender blocks forever.
**Fix:** Increase buffer to match the number of goroutines writing to it (3).

---

## Code Quality

### 5. Deduplicate goroutine launcher + panic recovery
**File:** `main.go:123-178`
**Issue:** `startBatteryWatcher`, `startGPSWatcher`, and `startADSBStreamer` share identical structure: add to waitgroup, defer done, defer recover, run function, send error. This is repeated three times.
**Fix:** Extract a `launchWorker(wg, errChan, errSentinel, fn)` helper that captures the pattern once.

### 6. Multiple `Update()` calls per ADS-B message acquire the lock repeatedly
**File:** `pkg/adsb/adsb.go:285-345`
**Issue:** `updatePlaneData` calls `plane.Update()` up to 8 separate times per message, each acquiring and releasing the mutex. This is correct but wasteful.
**Fix:** Collect all options into a slice and call `plane.Update(opts...)` once.

### 7. Scope `GetMin()` is used as the step size for increment/decrement
**File:** `pkg/radar/radar.go:61-67`
**Issue:** `IncrementScope` and `DecrementScope` use `GetMin()` (20nm) as the step size. This is semantically confusing — the min range and the step size are different concepts. The scope has a `steps` field (4) that seems intended for ring subdivision, not increment size.
**Fix:** Add a dedicated `GetStep()` or `incrementSize` to Scope, or rename to clarify intent.

### 8. Squawk emergency constants use wrong labels
**File:** `pkg/airplane/airplane.go:23-25`
**Issue:** `squawkGeneralEmergency` is 7500 (hijacking) and `squawkHijacking` is 7700 (general emergency). The labels are swapped. 7500 = hijack, 7600 = radio failure, 7700 = general emergency.
**Fix:** Swap the constant names: 7500 → `squawkHijacking`, 7700 → `squawkGeneralEmergency`.

---

## Resilience

### 9. ADSB stream does not reconnect on connection loss
**File:** `pkg/adsb/adsb.go:163-194`
**Issue:** If the TCP connection to readsb drops, `Stream()` returns an error and the goroutine exits permanently. The app continues running but never receives ADS-B data again.
**Fix:** Add reconnect logic with exponential backoff in the streaming loop, or have `main.go` restart the worker on error.

### 10. GPS watcher does not reconnect
**File:** `pkg/gps/gps.go:77-105`
**Issue:** Same as above — if gpsd drops the connection, the GPS goroutine exits and location data goes stale.
**Fix:** Add reconnect logic or restart the worker from main.

### 11. Battery watcher returns on any read error
**File:** `pkg/battery/watch.go:42-44`
**Issue:** A single transient read failure (e.g., momentary file lock) kills the battery watcher permanently.
**Fix:** Log the error and continue instead of returning, or retry a few times before giving up.

---

## Features

### 12. Configuration via environment variables or flags
**Issue:** All connection addresses and paths are hardcoded constants (ADSB port 30047, gpsd 127.0.0.1:2947, battery sysfs path). Running on a different setup requires code changes.
**Fix:** Accept `ADSB_ADDRESS`, `GPSD_ADDRESS`, `BATTERY_PATH` environment variables or CLI flags, falling back to current defaults.

### 13. Aircraft count in UI
**Issue:** There's no visible count of tracked aircraft. The user has to scroll the list to gauge traffic density.
**Fix:** Add a count to the header or radar title (e.g., "Radar Scope (5-20nm) — 12 aircraft").

### 14. North indicator on radar
**Issue:** The radar scope has no compass reference. When the terminal is resized or the scope range changes, there's no way to confirm orientation.
**Fix:** Draw an "N" marker at the top of the scope and optionally E/S/W at cardinal points.

### 15. Ground track history (trail dots)
**Issue:** Aircraft positions are only shown at their current location. There's no sense of where they've been or their trajectory beyond the heading line.
**Fix:** Store the last N positions per aircraft and render fading trail dots behind the current position.

---

## Priority Order

| Priority | Item | Effort |
|----------|------|--------|
| P0 | #1 GetICAO format bug | 5 min |
| P0 | #8 Swapped squawk labels | 5 min |
| P0 | #2 Emergency flag never clears | 10 min |
| P1 | #4 Error channel buffer size | 5 min |
| P1 | #3 Add dial timeout | 5 min |
| P1 | #9 ADSB reconnect on drop | 30 min |
| P1 | #10 GPS reconnect on drop | 30 min |
| P2 | #11 Battery watcher resilience | 15 min |
| P2 | #6 Batch Update() calls | 20 min |
| P2 | #5 Deduplicate goroutine launcher | 15 min |
| P2 | #7 Scope step size clarity | 10 min |
| P3 | #12 Env var configuration | 30 min |
| P3 | #13 Aircraft count in UI | 10 min |
| P3 | #14 North indicator on radar | 20 min |
| P3 | #15 Ground track history | 1-2 hrs |
