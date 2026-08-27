# Build, test, and deploy targets for uAirwaves.
#
# Deployment hosts are personal, so they live in a gitignored .env
# file next to this Makefile:
#
#   DEVICE = user@uconsole-host     # the uConsole running the TUI
#   RADIO  = user@radio-host        # optional remote BEAST receiver
#
# Targets that scp check for these and explain what to set when
# they're missing. Plain builds and tests need no .env at all.

-include .env

GOARCH_DEV    ?= arm64
GOARCH_RADIO  ?= arm64
RTL2832U      ?= ../rtl2832u
MODES         ?= ../modes
DEMOD1090     ?= ../demod1090
USB_PATH      ?= 1-1.3:1.0
# GNSS wiring. Comments go above the assignment, never after it: make keeps
# the whitespace before a trailing # inside the value.
# The AIO board GNSS sits on the mini-UART.
GPS_DEVICE    ?= /dev/ttyS0
GPS_BAUD      ?= 9600
# GPS_PPS=1 appends the pps-gpio overlay to config.txt; takes effect on reboot.
GPS_PPS       ?= 0
# GPS_SKIP=1 bypasses gps-setup entirely.
GPS_SKIP      ?=
CAPTURE_BYTES ?= 12000000  # ~5s @ 2.4 MS/s; enough for ≥1 ICAO under typical traffic

.PHONY: all build build-aarch64 build-macos build-radio test lint fmt \
        ship ship-all ship-radio radio smoke smoke-tui unbind clean \
        gps-setup gps-check

all: build

# Build for the machine you're sitting at.
build:
	go build -o uAirwaves .

build-aarch64:
	env GOOS=linux GOARCH=$(GOARCH_DEV) go build -o uAirwaves-aarch64 .

build-macos:
	env GOOS=darwin GOARCH=arm64 go build -o uAirwaves .

test:
	go test -race -cover ./...

lint:
	golangci-lint run ./...

fmt:
	go fmt ./...

# Upload beside the target and rename, rather than straight over it. scp onto
# a running binary fails with ETXTBSY; rename(2) only swaps the directory
# entry, so a TUI session that is already open keeps running on the old inode
# and picks the new build up next time it starts.
ship: build-aarch64 gps-setup
	@test -n "$(DEVICE)" || { echo "DEVICE not set. Create .env with: DEVICE = user@host"; exit 1; }
	scp uAirwaves-aarch64 $(DEVICE):~/uAirwaves.new
	ssh $(DEVICE) 'mv -f ~/uAirwaves.new ~/uAirwaves'

# ---------------------------------------------------------------
# SMOKE WORKFLOW
# ---------------------------------------------------------------
# ship-all  builds rtl-probe, demod1090, modes-decode, and
#           uAirwaves for arm64 and scps each binary into the
#           operator's home directory on the uConsole. Needs the
#           sibling library checkouts (RTL2832U / MODES /
#           DEMOD1090 above).
#
# unbind    detaches the kernel's dvb_usb_rtl28xxu driver from the
#           dongle. rtl2832u v0.1.4+ does this in-process, so this
#           target only matters for the standalone rtl-probe smoke
#           tools; harmless when already unbound. Override
#           USB_PATH if your dongle isn't at 1-1.3:1.0.
#
# smoke     runs the layered chain on the device: open the SDR,
#           capture an IQ window, replay it through demod1090 and
#           modes-decode, and assert at least one Mode S ICAO
#           landed. Catches regressions in any single layer
#           without needing a TUI.
#
# smoke-tui captures live IQ and runs uAirwaves against the file
#           via --replay-iq for a fixed window. Useful when a
#           layer change might affect the TUI's frame counters.

GO_BUILD_DEV_ENV := env GOOS=linux GOARCH=$(GOARCH_DEV)

ship-all: gps-setup
	@test -n "$(DEVICE)" || { echo "DEVICE not set. Create .env with: DEVICE = user@host"; exit 1; }
	mkdir -p dist
	$(GO_BUILD_DEV_ENV) go build -C $(RTL2832U)  -trimpath -o $(CURDIR)/dist/rtl-probe-aarch64    ./cmd/rtl-probe
	$(GO_BUILD_DEV_ENV) go build -C $(MODES)     -trimpath -o $(CURDIR)/dist/modes-decode-aarch64 ./cmd/modes-decode
	$(GO_BUILD_DEV_ENV) go build -C $(DEMOD1090) -trimpath -o $(CURDIR)/dist/demod1090-aarch64    ./cmd/demod1090
	$(MAKE) build-aarch64
	scp dist/rtl-probe-aarch64    $(DEVICE):~/rtl-probe.new
	scp dist/modes-decode-aarch64 $(DEVICE):~/modes-decode.new
	scp dist/demod1090-aarch64    $(DEVICE):~/demod1090.new
	scp uAirwaves-aarch64         $(DEVICE):~/uAirwaves.new
	ssh $(DEVICE) 'for b in rtl-probe modes-decode demod1090 uAirwaves; do mv -f ~/$$b.new ~/$$b; done'

# Make sure the device can actually supply a GPS fix: gpsd installed, pointed
# at the receiver, enabled and running. Idempotent, so `ship` runs it every
# time; it is a couple of dpkg queries when there is nothing to do. Skip it
# with `make ship GPS_SKIP=1` when deploying somewhere without a receiver.
gps-setup:
	@test -n "$(DEVICE)" || { echo "DEVICE not set. Create .env with: DEVICE = user@host"; exit 1; }
	@if [ "$(GPS_SKIP)" = "1" ]; then echo "gps-setup skipped (GPS_SKIP=1)"; else \
	  ssh $(DEVICE) "GPS_DEVICE=$(GPS_DEVICE) GPS_BAUD=$(GPS_BAUD) GPS_PPS=$(GPS_PPS) sh -s" \
	    < scripts/setup-gpsd.sh; \
	fi

# What is gpsd actually seeing right now? Changes nothing.
gps-check:
	@test -n "$(DEVICE)" || { echo "DEVICE not set. Create .env with: DEVICE = user@host"; exit 1; }
	@ssh $(DEVICE) 'systemctl is-active gpsd.socket gpsd | paste -sd" " -; \
	  gpspipe -w -n 10 2>/dev/null | grep -E "\"class\":\"(DEVICE|TPV|SKY)\"" | tail -3'

unbind:
	@test -n "$(DEVICE)" || { echo "DEVICE not set. Create .env with: DEVICE = user@host"; exit 1; }
	@ssh $(DEVICE) 'echo $(USB_PATH) | sudo tee /sys/bus/usb/drivers/dvb_usb_rtl28xxu/unbind 2>/dev/null || true'

smoke: unbind
	@ssh $(DEVICE) 'set -euo pipefail; \
		echo "==> rtl-probe (open + IQ stats)"; \
		./rtl-probe; \
		echo "==> rtl-probe -capture (~5s of IQ)"; \
		./rtl-probe -no-probe -capture /tmp/uaw-smoke.iq -capture-bytes $(CAPTURE_BYTES); \
		echo "==> demod1090 -replay-iq | modes-decode"; \
		./demod1090 -replay-iq /tmp/uaw-smoke.iq 2>/tmp/uaw-demod.err > /tmp/uaw-frames.hex; \
		decoded=$$(cat /tmp/uaw-frames.hex | ./modes-decode 2>/tmp/uaw-modes.err | tee /tmp/uaw-decoded.txt | wc -l); \
		echo "==> decoded $$decoded lines"; \
		test "$$decoded" -ge 1 || { echo "FAIL: no decoded frames"; tail /tmp/uaw-demod.err /tmp/uaw-modes.err; exit 1; }; \
		echo "OK"'

smoke-tui: unbind
	@ssh $(DEVICE) 'set -euo pipefail; \
		./rtl-probe -no-probe -capture /tmp/uaw-smoke.iq -capture-bytes $(CAPTURE_BYTES); \
		echo "==> uAirwaves (replay, 8s window)"; \
		timeout 8 ./uAirwaves --replay-iq /tmp/uaw-smoke.iq; \
		rc=$$?; \
		[ "$$rc" = 0 ] || [ "$$rc" = 124 ] || { echo "FAIL: uAirwaves exit $$rc"; exit $$rc; }; \
		echo "OK"'

clean:
	rm -rf dist uAirwaves uAirwaves-aarch64

# ---------------------------------------------------------------
# RADIO HOST
# ---------------------------------------------------------------
# A separate machine with the RTL-SDR plugged in, serving Mode-S
# Beast frames over TCP for the operator station to consume via
# --beast. Decouples antenna placement from where the TUI runs.
#
# build-radio  cross-compiles demod1090 for the radio host
#              (GOARCH_RADIO, default arm64).
#
# ship-radio   builds + scps demod1090 to ~/demod1090 on RADIO.
#              Override the host with `make ship-radio RADIO=user@host`.
#              rtl2832u v0.1.4+ auto-detaches the kernel DVB driver
#              via USBDEVFS_DISCONNECT_CLAIM, so no sysfs unbind is
#              required on a fresh boot; the operator still needs
#              udev permissions for /dev/bus/usb/* (plugdev rule).
#
# radio        prints the canonical operator commands. Start the
#              Beast server on the radio host, point uAirwaves at
#              it from the operator station. Run-and-supervise is
#              a systemd job, not a build-Makefile job, so we don't
#              kick off the process here.

GO_BUILD_RADIO_ENV := env GOOS=linux GOARCH=$(GOARCH_RADIO)

build-radio:
	mkdir -p dist
	$(GO_BUILD_RADIO_ENV) go build -C $(DEMOD1090) -trimpath \
		-o $(CURDIR)/dist/demod1090-$(GOARCH_RADIO) ./cmd/demod1090

ship-radio: build-radio
	@test -n "$(RADIO)" || { echo "RADIO not set. Create .env with: RADIO = user@host"; exit 1; }
	scp dist/demod1090-$(GOARCH_RADIO) $(RADIO):~/demod1090.new
	ssh $(RADIO) 'mv -f ~/demod1090.new ~/demod1090'

radio:
	@test -n "$(RADIO)" || { echo "RADIO not set. Create .env with: RADIO = user@host"; exit 1; }
	@echo "On the radio host ($(RADIO)):"
	@echo "  ./demod1090 --beast-listen :30005 --permissive-preamble --error-correction"
	@echo ""
	@echo "On the operator station:"
	@echo "  ./uAirwaves --beast $(shell echo $(RADIO) | cut -d@ -f2):30005"
