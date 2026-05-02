# uAirwaves Improvement Plan

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
