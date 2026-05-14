# uAirwaves Improvement Plan

## SDR Stack Improvements (audit refreshed 2026-05-14)

Originally captured 2026-05-07 after the rtl2832u + demod1090 + modes
pure-Go pipeline went end-to-end on silicon. Findings benchmarked
against readsb on the same hardware (Jetvision antenna direct into
uConsole-attached RTL-SDR, no LNA): readsb produces ~21 000 accepted
Mode S frames per minute; our pipeline with the just-shipped
permissive + error-correction defaults reaches ~1 000 — still 20×
behind. The items below are the path to closing that gap.

### Yield (callsigns/sec, traffic visibility)

| # | What | Where | Why it matters |
|---|---|---|---|
| Y1 | **demod1090 preamble detector** — readsb fires ~3 600 events/sec on the same antenna, ours ~16/sec even with `--permissive-preamble`. Order-of-magnitude detection gap. | `~/repos/demod1090/demod/preamble.go` | Closes the residual 20× gap to readsb. Biggest single yield lever. |
| Y2 | **demod1090 bit decoder marginal-SNR yield** — long-frame CRC fails far more than short-frame CRC at the same SNR; dump1090's bit-decision logic uses neighbouring-sample slope tests our committed code skips. | `~/repos/demod1090/demod/bits.go` | Long frames carry the ADS-B payload. Biggest ident win. |
| Y3 | **Triage demod1090 WIP** — 477 lines of in-flight changes that currently produce 0 frames. Likely a real attempt at Y1 + Y2 that's mid-debug. | `~/repos/demod1090/demod/{preamble,bits,demodulator}.go` | Either the work resumes or it gets parked cleanly. |

### Operational (run / debug / iterate)

| # | What | Where |
|---|---|---|
| O1 | **Expose demod knobs from uAirwaves** — env vars `UAIRWAVES_PERMISSIVE`, `UAIRWAVES_ERROR_CORRECTION` so the operator can A/B without rebuild. Currently always-on by default; sometimes strict mode is what you want for noise diagnosis. | `pkg/adsb/adsb.go` `defaultDemodulatorFactory` |
| O2 | **Auto-unbind kernel driver on uAirwaves start** — currently fails with a verbose diagnostic; could run the unbind path itself if root, or print a one-line copy-pasteable command. | `main.go` startup, or a `pre-start.sh` helper |
| O3 | **`make compare-readsb` smoke target** — capture once, replay through both demod1090 and readsb (where `readsb --device-type ifile` works), print yield delta. Closes the loop on yield experiments. | `Makefile` |
| O4 | **rtl-probe `-gain N` VGA-default override** — librtlsdr ladder pins VGA at +16 dB; for tuned antennas +24..40 dB is needed. Add a `-vga-target dB` flag or a `-gain-profile=tuned-antenna` preset. | `rtl2832u/cmd/rtl-probe/main.go` |
| O5 | **Stats panel: per-DF breakdown** — currently shows total + IDs only. Adding `DF17/18 N/min · DF4/5 N/min · noise N/min` gives reception-quality at-a-glance. | `internal/ui/stats.go`, plus per-DF counters in `pkg/adsb/handle.go` |
| O6 | **uAirwaves TUI hangs over ssh without TTY** — operational; `ssh -t` works around but the binary should detect and exit clean instead of blocking on tview's terminal init. | `main.go` startup |

### Test / coverage / hygiene

| # | What | Where |
|---|---|---|
| T1 | **pkg/adsb to 100%** — currently 99%. The remaining 1% is the SDR-success branch of `defaultReceiverFactory` (needs a real dongle) and a defensive `if !found` in `handleFrame` after `planes.Ensure(...)` that's unreachable from a single goroutine. Both judged acceptable. | `pkg/adsb/handle.go` + `pkg/adsb/adsb.go` |
| T3 | **rtl2832u pre-existing `wsl_v5` lint hits** — two whitespace warnings in the new `tuner_r860_bandwidth.go`. | `~/repos/rtl2832u/tuner_r860_bandwidth.go:115,121` |

### Documentation

| # | What | Where |
|---|---|---|
| D1 | **README.md** — predates rtl-probe / modes-decode / `UAIRWAVES_REPLAY_IQ`. Document the smoke chain so a fresh checkout knows how to verify on silicon. | `readme.md` |
| D2 | **Document the AGC-disabled-by-design quirk** — the rtl2832u driver intentionally disables the demod's RF/IF AGC loop on R820T silicon; a future reader might try to "fix" the disable. Codify the rationale in a top-of-file comment in `init_chip.go` (already partially documented in `c445dd7`). | `~/repos/rtl2832u/init_chip.go` |

### Suggested order

1. **O1** (knob exposure, ~30 min) — operator agility before any other work.
2. **Y1 + Y2 + Y3** as one focused demod-improvement session.
3. Polish: **O5**, **D1**.

---

## Closed since 2026-05-07

Tracked here so a future reader doesn't re-litigate work already in git.

- **Y4** rtl2832u IF/LO alignment — landed in `06984d0` (rtl2832u 0.1.1 → 0.1.3 bump).
- **T2** `validCallsign` varnamelen — renamed to `callsign` during the May audit.
- **Bug fixes #1–#15** (the entire "Bug Fixes / Code Quality / Resilience / Features" section that was in the 2026-05-07 draft): GetICAO format verb, sticky emergency flag, ADSB dial timeout (obviated by the TCP→SDR switch in `223a799`), errChan buffer size, deduplicated `launchWorker`, coalesced `plane.Update`, scope step semantics, swapped squawk labels, ADSB/GPS reconnect, battery watcher resilience, env-var config, aircraft count, north indicator, trail history. All resolved via the May 2026 cleanup; see commits and `pkg/{airplane,adsb,gps,battery,radar,airplanes}` history.
