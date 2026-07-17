# Changelog

All notable changes to this project are documented in this file.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
