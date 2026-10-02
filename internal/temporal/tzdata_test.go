package temporal

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	stderrors "errors"
	"fmt"
	"hash/crc32"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	perr "github.com/frankbardon/pulse/errors"
)

// TestTZDataVersion_MatchesEmbeddedZip pins TZDataVersion to the bytes
// of the embedded zoneinfo.zip: refreshing the file without recording
// the new release (and its hash) fails here.
func TestTZDataVersion_MatchesEmbeddedZip(t *testing.T) {
	if !regexp.MustCompile(`^[0-9]{4}[a-z]$`).MatchString(TZDataVersion) {
		t.Errorf("TZDataVersion = %q, want an IANA release name such as \"2026c\"", TZDataVersion)
	}
	sum := sha256.Sum256(zoneinfoZip)
	if got := hex.EncodeToString(sum[:]); got != tzdataSHA256 {
		t.Fatalf("internal/temporal/zoneinfo.zip changed (sha256 %s, recorded %s): refresh TZDataVersion "+
			"to the DATA= release in $(go env GOROOT)/lib/time/update.bash and set tzdataSHA256 to %s "+
			"(see docs/src/internals/refreshing-tzdata.md)", got, tzdataSHA256, got)
	}
}

// TestEmbeddedZones_EveryWellFormedEntryLoads walks the embedded index:
// every entry with an Area/Location shape resolves through LoadZone, and
// the index carries the names the rest of the suite relies on.
func TestEmbeddedZones_EveryWellFormedEntryLoads(t *testing.T) {
	idx, err := embeddedZones()
	if err != nil {
		t.Fatal(err)
	}
	if len(idx) < 300 {
		t.Fatalf("embedded index has %d entries, want the full tz database", len(idx))
	}
	for _, name := range []string{"Europe/Berlin", "America/New_York", "Asia/Kolkata", "Etc/UTC"} {
		if _, ok := idx[name]; !ok {
			t.Errorf("embedded index lacks %q", name)
		}
	}
	n := 0
	for name := range idx {
		if !wellFormedZoneName(name) {
			continue
		}
		n++
		if _, err := LoadZone(name); err != nil {
			t.Errorf("LoadZone(%q) = %v, want accepted (it is in the embedded zip)", name, err)
		}
	}
	if n == 0 {
		t.Fatal("no well-formed entries in the embedded index")
	}
}

// TestLoadZone_CaseSensitiveOnEveryHost pins the exact-name lookup: a
// case-folded spelling is refused even where the host filesystem is
// case-insensitive, because the embedded index is the only source.
func TestLoadZone_CaseSensitiveOnEveryHost(t *testing.T) {
	for _, name := range []string{"EUROPE/BERLIN", "europe/berlin", "Europe/BERLIN", "America/NEW_YORK"} {
		_, err := LoadZone(name)
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_TIMEZONE_UNKNOWN {
			t.Errorf("LoadZone(%q) err = %v, want PULSE_TIMEZONE_UNKNOWN", name, err)
		}
	}
}

// zoneDigest summarises the zones' acceptance and full transition tables.
func zoneDigest(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for _, name := range []string{"Europe/Berlin", "America/New_York", "Asia/Kolkata", "Australia/Lord_Howe", "EUROPE/BERLIN", "europe/berlin", "Europe/Atlantis"} {
		z, err := LoadZone(name)
		if err != nil {
			fmt.Fprintf(&b, "%s:refused;", name)
			continue
		}
		fmt.Fprintf(&b, "%s:%v:%v;", name, z.starts, z.offs)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// doctoredZip writes a zoneinfo zip in the layout time.LoadLocation
// reads from $ZONEINFO, with Europe/Berlin replaced by UTC data and an
// extra Europe/Atlantis entry.
func doctoredZip(t *testing.T) string {
	t.Helper()
	idx, err := embeddedZones()
	if err != nil {
		t.Fatal(err)
	}
	rc, err := idx["Etc/UTC"].Open()
	if err != nil {
		t.Fatal(err)
	}
	var utc bytes.Buffer
	if _, err := utc.ReadFrom(rc); err != nil {
		t.Fatal(err)
	}
	_ = rc.Close()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"Europe/Berlin", "Europe/Atlantis", "EUROPE/BERLIN"} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(utc.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "zoneinfo.zip")
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const (
	tzChildEnv  = "TEMPORAL_TZ_CHILD"
	digestToken = "ZONE_DIGEST="
)

// TestLoadZone_IgnoresHostZoneinfo proves $ZONEINFO cannot change what
// LoadZone accepts or the offsets it returns. The stdlib reads $ZONEINFO
// once per process, so each scenario runs in a fresh child process
// (TestLoadZone_IgnoresHostZoneinfoChild); the child also confirms the
// doctored zip IS live for time.LoadLocation, so the comparison is never
// vacuous. The in-process t.Setenv arm covers the same-process case.
func TestLoadZone_IgnoresHostZoneinfo(t *testing.T) {
	want := zoneDigest(t)

	doctored := doctoredZip(t)
	missing := filepath.Join(t.TempDir(), "nonexistent", "zoneinfo.zip")

	t.Run("in-process", func(t *testing.T) {
		for _, zi := range []string{missing, doctored} {
			t.Setenv("ZONEINFO", zi)
			if got := zoneDigest(t); got != want {
				t.Errorf("ZONEINFO=%s changed LoadZone results", zi)
			}
		}
	})

	for _, sc := range []struct{ name, zoneinfo, mode string }{
		{"nonexistent", missing, "missing"},
		{"doctored", doctored, "doctored"},
	} {
		t.Run("child-"+sc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestLoadZone_IgnoresHostZoneinfoChild$", "-test.v", "-test.count=1")
			cmd.Env = append(os.Environ(), "ZONEINFO="+sc.zoneinfo, tzChildEnv+"="+sc.mode)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("child failed: %v\n%s", err, out)
			}
			s := string(out)
			i := strings.Index(s, digestToken)
			if i < 0 {
				t.Fatalf("child printed no digest:\n%s", s)
			}
			got := strings.Fields(s[i+len(digestToken):])[0]
			if got != want {
				t.Errorf("ZONEINFO=%s (fresh process) changed LoadZone results: digest %s, want %s", sc.zoneinfo, got, want)
			}
		})
	}
}

// TestLoadZone_IgnoresHostZoneinfoChild is the child half of
// TestLoadZone_IgnoresHostZoneinfo; it does nothing unless that test
// spawned it.
func TestLoadZone_IgnoresHostZoneinfoChild(t *testing.T) {
	mode := os.Getenv(tzChildEnv)
	if mode == "" {
		return
	}
	summer := time.Date(2024, 7, 1, 12, 0, 0, 0, time.UTC).Unix()
	if mode == "doctored" {
		// Sanity: the doctored zip is what the stdlib now resolves from,
		// so a LoadZone that consulted $ZONEINFO would see it.
		loc, err := time.LoadLocation("Europe/Berlin")
		if err != nil {
			t.Fatalf("doctored ZONEINFO not live: time.LoadLocation: %v", err)
		}
		if _, off := time.Unix(summer, 0).In(loc).Zone(); off != 0 {
			t.Fatalf("doctored ZONEINFO not live: stdlib Europe/Berlin offset %d, want 0", off)
		}
		if _, err := time.LoadLocation("Europe/Atlantis"); err != nil {
			t.Fatalf("doctored ZONEINFO not live: Europe/Atlantis: %v", err)
		}
	}
	z, err := LoadZone("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	if off := z.Offset(summer); off != 7200 {
		t.Errorf("LoadZone(Europe/Berlin).Offset(summer) = %d, want 7200", off)
	}
	fmt.Printf("%s%s\n", digestToken, zoneDigest(t))
}

// stubIndex builds a one-entry index over raw zip bytes.
func stubIndex(t *testing.T, build func(zw *zip.Writer)) func() (map[string]*zip.File, error) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	build(zw)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return func() (map[string]*zip.File, error) {
		b := buf.Bytes()
		r, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			return nil, err
		}
		m := map[string]*zip.File{}
		for _, f := range r.File {
			m[f.Name] = f
		}
		return m, nil
	}
}

// TestLoadLocationFrom_FailureArms pins that every way an entry can be
// unusable is a miss (which LoadZone turns into PULSE_TIMEZONE_UNKNOWN),
// never a panic or a half-built location.
func TestLoadLocationFrom_FailureArms(t *testing.T) {
	const name = "Area/Zone"
	cases := map[string]func() (map[string]*zip.File, error){
		"index error": func() (map[string]*zip.File, error) { return nil, stderrors.New("boom") },
		"no entry":    stubIndex(t, func(zw *zip.Writer) {}),
		"not tzif": stubIndex(t, func(zw *zip.Writer) {
			w, _ := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
			_, _ = w.Write([]byte("not a tzif file"))
		}),
		"unsupported method": stubIndex(t, func(zw *zip.Writer) {
			w, _ := zw.CreateRaw(&zip.FileHeader{Name: name, Method: 99, CompressedSize64: 3, UncompressedSize64: 3})
			_, _ = w.Write([]byte("abc"))
		}),
		"checksum mismatch": stubIndex(t, func(zw *zip.Writer) {
			data := []byte("TZif-bytes")
			w, _ := zw.CreateRaw(&zip.FileHeader{
				Name: name, Method: zip.Store,
				CRC32:            crc32.ChecksumIEEE(data) ^ 1,
				CompressedSize64: uint64(len(data)), UncompressedSize64: uint64(len(data)),
			})
			_, _ = w.Write(data)
		}),
	}
	for label, idx := range cases {
		if loc, ok := loadLocationFrom(idx, name); ok || loc != nil {
			t.Errorf("%s: loadLocationFrom = (%v, %v), want (nil, false)", label, loc, ok)
		}
	}
	// The happy path through the same seam, for contrast.
	if _, ok := loadLocationFrom(embeddedZones, "Europe/Berlin"); !ok {
		t.Error("loadLocationFrom(embedded, Europe/Berlin) = miss")
	}
}

// BenchmarkLoadZone reports the warm per-call cost of LoadZone (index
// already parsed; it still builds a full 1900..2100 transition table per
// call) and, as index-parse, the one-time cold cost of reading the
// embedded zip's central directory.
func BenchmarkLoadZone(b *testing.B) {
	if _, err := embeddedZones(); err != nil {
		b.Fatal(err)
	}
	b.Run("Europe/Berlin", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := LoadZone("Europe/Berlin"); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("index-parse", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := zip.NewReader(bytes.NewReader(zoneinfoZip), int64(len(zoneinfoZip))); err != nil {
				b.Fatal(err)
			}
		}
	})
}
