# Regenerating `data.go`

`data.go` is generated from the public-domain
[OurAirports CSV](https://davidmegginson.github.io/ourairports-data/airports.csv)
and a hand-picked list of ICAO codes (`codes.txt` in this directory).

The raw CSV is **not committed**: it is large, frequently updated upstream,
and CC0-licensed (so we don't need to keep our own copy).

## Steps

```sh
cd pkg/airports/internal/gen
curl -sSL -o /tmp/ourairports.csv \
    https://davidmegginson.github.io/ourairports-data/airports.csv

./generate.sh > ../../data.go
```

Then `gofmt` the result and commit only `data.go`.

To add or remove an airport, edit `codes.txt` and re-run `generate.sh`.
The script prints any unresolved ICAOs to stderr — investigate those
case-by-case (the OurAirports classification may have changed).
