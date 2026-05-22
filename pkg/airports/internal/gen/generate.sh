#!/usr/bin/env bash
# Regenerates pkg/airports/data.go from a hand-picked ICAO list
# (codes.txt) against a fresh OurAirports CSV at /tmp/ourairports.csv.
# Output goes to stdout. README.md in this dir documents the workflow.
#
# Uses python3 (stdlib) for the CSV parse — awk's naive field split
# corrupts rows whose name carries an embedded comma (e.g. "Bergen
# Airport, Flesland").
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
csv="/tmp/ourairports.csv"

if [[ ! -f "$csv" ]]; then
    echo "fatal: $csv not found — see README.md for the curl command" >&2
    exit 1
fi

python3 - "$here/codes.txt" "$csv" <<'PY'
import csv, sys

codes_path, csv_path = sys.argv[1], sys.argv[2]

with open(codes_path) as f:
    wanted = {line.strip() for line in f if line.strip()}

found = {}
with open(csv_path, newline="") as f:
    for row in csv.DictReader(f):
        ident = row["ident"]
        if ident in wanted:
            found[ident] = row

print("package airports")
print()
print("// airportList is the embedded data set. Generated from")
print("// the OurAirports public-domain CSV via the regen workflow")
print("// documented at pkg/airports/internal/gen/README.md.")
print("// Entries are alphabetical by ICAO.")
print("//")
print("//nolint:gochecknoglobals // package-level static data, read-only.")
print("var airportList = []Airport{")

for ident in sorted(found):
    row = found[ident]
    name = row["name"].replace("\\", "\\\\").replace('"', '\\"')
    print('\t{{ICAO: "{icao}", IATA: "{iata}", Name: "{name}", Country: "{cc}", Latitude: {lat}, Longitude: {lon}}},'.format(
        icao=ident,
        iata=row["iata_code"],
        name=name,
        cc=row["iso_country"],
        lat=row["latitude_deg"],
        lon=row["longitude_deg"],
    ))

print("}")

missing = wanted - set(found)
if missing:
    for m in sorted(missing):
        print("MISSING:", m, file=sys.stderr)
PY
