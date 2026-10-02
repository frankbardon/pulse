package temporal

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"io"
	"sync"
	"time"
)

// zoneinfoZip is Pulse's own copy of the IANA tz database, in the
// uncompressed zip layout the Go toolchain ships at
// $GOROOT/lib/time/zoneinfo.zip. LoadZone resolves names ONLY from it:
// never from $ZONEINFO, the host's /usr/share/zoneinfo, or the
// time/tzdata embed, so zone acceptance and every offset are identical
// on every host. Refresh it with `make tzdata` (recipe:
// docs/src/internals/refreshing-tzdata.md).
//
//go:embed zoneinfo.zip
var zoneinfoZip []byte

// TZDataVersion is the IANA tz database release embedded in
// zoneinfo.zip. Go's zip carries no version entry, so the value is
// recorded when the file is refreshed (from the DATA= line of the
// toolchain's lib/time/update.bash) and pinned to the file's bytes by
// tzdataSHA256: TestTZDataVersion_MatchesEmbeddedZip fails when the zip
// changes without both constants being updated.
const TZDataVersion = "2026c"

// tzdataSHA256 is the hex SHA-256 of the zoneinfo.zip TZDataVersion
// describes.
const tzdataSHA256 = "b2d18a7c8fa8142097a48c99609fb3c92db5ee98bc740294e57eab8ae9f94779"

var (
	tzIndexOnce sync.Once
	tzIndex     map[string]*zip.File
	tzIndexErr  error
)

// embeddedZones parses the embedded zip's central directory once and
// returns the name -> entry index. Lookups are exact and case-sensitive:
// a map key matches only the byte-identical entry name.
func embeddedZones() (map[string]*zip.File, error) {
	tzIndexOnce.Do(func() {
		r, err := zip.NewReader(bytes.NewReader(zoneinfoZip), int64(len(zoneinfoZip)))
		if err != nil {
			tzIndexErr = err
			return
		}
		idx := make(map[string]*zip.File, len(r.File))
		for _, f := range r.File {
			idx[f.Name] = f
		}
		tzIndex = idx
	})
	return tzIndex, tzIndexErr
}

// loadEmbeddedLocation builds name's *time.Location from the embedded
// tz database alone. ok is false when the name has no entry (or the
// entry is unreadable or not valid TZif data).
func loadEmbeddedLocation(name string) (*time.Location, bool) {
	return loadLocationFrom(embeddedZones, name)
}

// loadLocationFrom is loadEmbeddedLocation over an arbitrary index
// source, so tests can exercise the failure arms.
func loadLocationFrom(index func() (map[string]*zip.File, error), name string) (*time.Location, bool) {
	idx, err := index()
	if err != nil {
		return nil, false
	}
	f, found := idx[name]
	if !found {
		return nil, false
	}
	rc, err := f.Open()
	if err != nil {
		return nil, false
	}
	data, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		return nil, false
	}
	loc, err := time.LoadLocationFromTZData(name, data)
	if err != nil {
		return nil, false
	}
	return loc, true
}
