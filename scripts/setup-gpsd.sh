#!/bin/sh
#
# setup-gpsd.sh -- make sure gpsd is installed, pointed at the GNSS receiver,
# and running. Runs on the deployment target, not on your workstation:
#
#   ssh $DEVICE "GPS_DEVICE=/dev/ttyS0 sh -s" < scripts/setup-gpsd.sh
#
# `make ship` does that for you. It is idempotent, so running it on every
# deploy costs a couple of dpkg queries when there is nothing to do.
#
# Environment:
#   GPS_DEVICE    serial port the receiver is on   (default /dev/ttyS0)
#   GPS_BAUD      fixed line speed                 (default 9600)
#   GPS_PPS       1 to add the PPS overlay         (default 0, needs a reboot)
#   GPS_PPS_GPIO  GPIO carrying PPS                (default 6, the AIO board)
#   GPS_STRICT    1 to fail when no receiver is found (default 0, warn only)
#
# Defaults match a uConsole with the HackerGadgets AIO board, where the GNSS
# hangs off the mini-UART. Other wiring: pass GPS_DEVICE.
#
set -eu

GPS_DEVICE="${GPS_DEVICE:-/dev/ttyS0}"
GPS_BAUD="${GPS_BAUD:-9600}"
GPS_PPS="${GPS_PPS:-0}"
GPS_PPS_GPIO="${GPS_PPS_GPIO:-6}"
GPS_STRICT="${GPS_STRICT:-0}"

BOOTCFG=/boot/firmware/config.txt
[ -f "$BOOTCFG" ] || BOOTCFG=/boot/config.txt

say()  { printf '  %s\n' "$*"; }
ok()   { printf '  \033[32m+\033[0m %s\n' "$*"; }
warn() { printf '  \033[33m!\033[0m %s\n' "$*"; }
die()  { printf '  \033[31mERROR\033[0m %s\n' "$*" >&2; exit 1; }
head_(){ printf '\n\033[1m%s\033[0m\n' "$*"; }

head_ "gpsd setup on $(hostname)"

# ------------------------------------------------------------ environment --
command -v systemctl >/dev/null 2>&1 || die "no systemd here; configure gpsd by hand"
command -v apt-get   >/dev/null 2>&1 || die "not a Debian-family system; install gpsd by hand"
sudo -n true 2>/dev/null || die "passwordless sudo is required (make ship runs unattended)"

# ---------------------------------------------------------------- receiver --
# Only a warning when the port is missing: shipping to a machine with no GNSS
# is legitimate, and uAirwaves falls back to ADS-B self-locate.
if [ ! -e "$GPS_DEVICE" ]; then
  warn "$GPS_DEVICE does not exist"
  CANDIDATES=$(ls /dev/ttyS[0-9]* /dev/ttyAMA[0-9]* /dev/ttyUSB[0-9]* /dev/ttyACM[0-9]* 2>/dev/null | tr '\n' ' ')
  [ -n "$CANDIDATES" ] && say "serial ports present: $CANDIDATES"
  say "set GPS_DEVICE to the right one, e.g. make ship GPS_DEVICE=/dev/ttyUSB0"
  [ "$GPS_STRICT" = "1" ] && die "GPS_STRICT=1 and no receiver"
  warn "continuing without GPS; uAirwaves will self-locate from ADS-B"
  exit 0
fi
ok "receiver port $GPS_DEVICE present"

# A getty on the same port will fight gpsd for the bytes.
GETTY="serial-getty@$(basename "$GPS_DEVICE").service"
if systemctl is-active --quiet "$GETTY" 2>/dev/null; then
  warn "$GETTY is holding $GPS_DEVICE -- disabling it, nothing else can read the port"
  sudo systemctl disable --now "$GETTY" >/dev/null 2>&1 || true
fi

# ---------------------------------------------------------------- packages --
MISSING=""
for pkg in gpsd gpsd-clients; do
  dpkg-query -W -f='${Status}' "$pkg" 2>/dev/null | grep -q "ok installed" || MISSING="$MISSING $pkg"
done
if [ -n "$MISSING" ]; then
  say "installing:$MISSING"
  sudo DEBIAN_FRONTEND=noninteractive apt-get update -qq
  # shellcheck disable=SC2086
  sudo DEBIAN_FRONTEND=noninteractive apt-get install -y -qq $MISSING >/dev/null \
    || die "apt failed to install$MISSING"
  ok "installed:$MISSING"
else
  ok "gpsd and gpsd-clients already installed"
fi

# ------------------------------------------------------------------ config --
NEW=$(mktemp)
cat > "$NEW" <<EOF
# Managed by uAirwaves scripts/setup-gpsd.sh -- edits here are overwritten.
START_DAEMON="true"
DEVICES="$GPS_DEVICE"
GPSD_OPTIONS="-n -s $GPS_BAUD"
USBAUTO="false"
EOF

CHANGED=0
if ! sudo cmp -s "$NEW" /etc/default/gpsd 2>/dev/null; then
  sudo install -m 0644 "$NEW" /etc/default/gpsd
  CHANGED=1
  ok "wrote /etc/default/gpsd ($GPS_DEVICE @ ${GPS_BAUD}, -n so it polls without a client)"
else
  ok "/etc/default/gpsd already correct"
fi
rm -f "$NEW"

# --------------------------------------------------------------------- PPS --
# The AIO board brings the receiver PPS out on a GPIO rather than the serial
# DCD line, so gpsd cannot find it without an overlay. Opt-in: it edits the
# boot config and only takes effect after a reboot.
if [ "$GPS_PPS" = "1" ]; then
  if [ ! -w "$BOOTCFG" ] && [ ! -f "$BOOTCFG" ]; then
    warn "no $BOOTCFG -- skipping PPS"
  elif grep -q "^dtoverlay=pps-gpio" "$BOOTCFG"; then
    ok "PPS overlay already in $BOOTCFG"
  else
    printf 'dtoverlay=pps-gpio,gpiopin=%s\n' "$GPS_PPS_GPIO" | sudo tee -a "$BOOTCFG" >/dev/null
    warn "added pps-gpio on GPIO$GPS_PPS_GPIO to $BOOTCFG -- reboot for it to take effect"
  fi
fi

# ------------------------------------------------------------------ enable --
sudo systemctl enable gpsd.socket gpsd >/dev/null 2>&1 || true
if [ "$CHANGED" = "1" ] || ! systemctl is-active --quiet gpsd; then
  sudo systemctl restart gpsd.socket
  sudo systemctl restart gpsd
  say "gpsd restarted"
fi
systemctl is-active --quiet gpsd.socket || die "gpsd.socket is not running"
ok "gpsd.socket and gpsd enabled and active"

# ------------------------------------------------------------------ verify --
# Proves gpsd opened the port and is parsing it, which is what uAirwaves needs.
head_ "Verifying"
OUT=$(timeout 10 gpspipe -w -n 12 2>/dev/null || true)
[ -n "$OUT" ] || die "gpsd is running but produced nothing on 127.0.0.1:2947"

DRIVER=$(printf '%s' "$OUT" | sed -n 's/.*"driver":"\([^"]*\)".*/\1/p' | head -1)
[ -n "$DRIVER" ] && ok "receiver identified as $DRIVER on $GPS_DEVICE" \
                 || warn "gpsd has not identified the receiver yet"

MODE=$(printf '%s' "$OUT" | sed -n 's/.*"class":"TPV".*"mode":\([0-9]\).*/\1/p' | tail -1)

# gpsd only emits the satellite-bearing SKY periodically; a short sample often
# catches only the DOP-only variant. Report the count when it is there rather
# than printing a question mark.
USED=$(printf '%s' "$OUT" | sed -n 's/.*"uSat":\([0-9]*\).*/\1/p' | tail -1)
if [ -n "$USED" ]; then SATS=", $USED satellites used"; else SATS=""; fi

case "${MODE:-0}" in
  3) ok "3D fix$SATS" ;;
  2) ok "2D fix$SATS" ;;
  *) warn "no fix yet$SATS -- normal indoors; gpsd is wired up correctly either way" ;;
esac

head_ "Done"
say "uAirwaves picks this up on 127.0.0.1:2947 with no flag needed."
say "Diagnose by hand with: gpspipe -w -n 10   or   cgps -s"
