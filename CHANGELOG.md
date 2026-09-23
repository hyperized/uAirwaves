# Changelog

All notable changes to this project are documented in this file.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.0] - 2026-09-23

### Added

- `selflocate.Fix.SpreadNm`, a precision figure beside the confidence
  bound: the RMS distance of five sub-estimates, each solved on an
  interleaved fifth of the observations, from the full estimate. The bound
  is what the horizon model guarantees and on a real feed runs to a
  hundred nautical miles; the spread is what the data shows. `pkg/location`
  carries it as `SpreadNm`, and the self-locate worker logs it as
  `spread_nm`.
- `selflocate.WithAntennaHeightFt`. The horizon now counts both ends of the
  link, is widened by a fifteen percent margin, and drops observations under
  300 ft, so an aircraft on a runway can no longer drag the estimate onto the
  airport. `Fix.Violated` counts the horizon circles that exclude the answer
  and the confidence radius is widened to cover them.
- A basic palette of named ANSI colours, chosen at startup when the terminal
  reports fewer than 256 colours, so the uConsole's own console keeps
  climbing and descending aircraft apart instead of approximating both to
  white.
- `make ship` installs and configures gpsd on the target for the HackerGadgets
  AIO board's GNSS, and verifies it is parsing the port. `GPS_SKIP=1` bypasses
  it and `GPS_PPS=1` adds the PPS overlay.

### Changed

- The 24-bit palette is explicit hex rather than tcell's named colours, tuned
  to at least 4.5:1 against dark backgrounds and with adjacent altitude bands
  at least dE 25 apart. The header bars carry light text on dark fills, the
  connection dots are picked against every bar they can sit on, and each
  notification severity has a text colour of its own.
- A gpsd that is simply not there is reported once rather than on every
  reconnect attempt.
- The confidence radius is measured over the whole plausible region rather
  than inside the search's final zoom, which used to report a fraction of a
  nautical mile whatever the error was.

### Fixed

- The Linux battery reader had never been linted; its errors are wrapped and
  its key switch has a default case.
- `make ship` renames the upload over a running binary instead of failing
  with ETXTBSY.
- The lint configuration disables `exhaustruct_v5` beside `exhaustruct`, so
  golangci-lint v2.13 locally and v2.12 in CI report the same findings, and
  the palette work's findings are cleared.

## [0.1.0] - 2026-07-17

First public release.

### Added

- In-process ADS-B pipeline for the RTL-SDR (RTL2832U + R860), with no
  external demodulator daemon: USB, IQ, Mode S demodulation and decoding
  all inside one binary.
- Alternative ingest modes: `--beast host:port` for a remote Mode-S Beast
  feed over TCP, and `--replay-iq path` for deterministic replay of a
  captured IQ file.
- Radar scope with altitude-coloured trails, heading projection, heatmap
  and airport overlays, plus a plane-centred MiniView in the flight-details
  panel.
- GPS positioning via gpsd, with a self-locate fallback that derives the
  receiver position from ADS-B horizon-circle intersection when gpsd has
  no fix.
- Automatic reconnect with exponential backoff on all three ingest paths,
  including reopening the local SDR after a USB unplug.
- Gain auto-sweep (`--auto-sweep`), bias-tee control (`--bias-t` and the
  `b` key), cross-platform battery status, and a non-TUI `--check`
  diagnostic mode that emits a JSON report.

[0.1.0]: https://github.com/hyperized/uAirwaves/releases/tag/v0.1.0
