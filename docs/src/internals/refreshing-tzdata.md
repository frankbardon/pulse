# Refreshing the Time-Zone Database

**Audience:** Pulse internals contributors picking up a newer IANA tz
database release.

Pulse resolves every time-zone name from its own embedded copy of the
tz database, `internal/temporal/zoneinfo.zip` — the uncompressed zip the
Go toolchain ships at `$(go env GOROOT)/lib/time/zoneinfo.zip`.
`temporal.LoadZone` looks the exact, case-sensitive name up in that zip
and builds the location with `time.LoadLocationFromTZData`. It never
consults `$ZONEINFO`, the host's zoneinfo files or `time/tzdata`, so the
accepted set of names and every offset are identical on every host
(`TestLoadZone_IgnoresHostZoneinfo`).

Go's zip carries no version entry, so the release is recorded by hand as
`temporal.TZDataVersion` and pinned to the file's bytes by the constant
`tzdataSHA256` in `internal/temporal/tzdata.go`.
`TestTZDataVersion_MatchesEmbeddedZip` fails whenever the zip changes
without both constants changing with it.

## When to refresh

After a Go toolchain upgrade whose `lib/time/update.bash` names a newer
`DATA=` release, or when a tz change affecting users has shipped. The
file never refreshes on its own: a toolchain upgrade alone changes
nothing.

## Recipe

1. With the toolchain you want to take the data from active, run
   `make tzdata`. It copies the zip into `internal/temporal/` and prints
   the release (`DATA=` from `lib/time/update.bash`) and the new
   SHA-256.
2. Set `TZDataVersion` and `tzdataSHA256` in
   `internal/temporal/tzdata.go` to the printed values.
3. Run `go test -race ./internal/temporal/`. The property tests compare
   against the stdlib over the same embedded locations, so they hold
   across releases; a changed zone shows up only if a pinned example
   (for instance a DST-at-midnight zone) changed its rules.
4. Commit the zip and `tzdata.go` together.
