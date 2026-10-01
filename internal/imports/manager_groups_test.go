package imports

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"

	"github.com/spf13/afero"
)

// writeJoinCSV writes a synthetic denormalized orders⋈customers table:
// rows orders over customers customers, each customer's wide attributes
// (two f64 coordinates) repeated on every one of its orders — the shape a
// parent group exists to fold.
func writeJoinCSV(t *testing.T, afs afero.Fs, p string, rows, customers int) {
	t.Helper()
	var b strings.Builder
	b.WriteString("order_id,cust_id,cust_lat,cust_lon,amount\n")
	for i := 0; i < rows; i++ {
		c := i % customers
		fmt.Fprintf(&b, "%d,%d,%.6f,%.6f,%.2f\n", 1000+i, c+1, 10.123457+float64(c)*1.5, -70.654321-float64(c)*0.75, float64(i%37)+0.25)
	}
	if err := afero.WriteFile(afs, p, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

// custGroup is the declaration the fixture's structure supports.
var custGroup = pio.GroupDecl{Key: []string{"cust_id"}, Members: []string{"cust_lat", "cust_lon"}}

func versionByte(t *testing.T, afs afero.Fs, p string) byte {
	t.Helper()
	b, err := afero.ReadFile(afs, p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	if len(b) < encoding.HeaderSize {
		t.Fatalf("%s: %d bytes, shorter than the header", p, len(b))
	}
	return b[encoding.HeaderSize-1]
}

// TestManager_Open_Groups_WritesGroupedCohort: a Spec.Groups declaration
// reaches the import job — the managed cohort is 0x02 and the result
// reports the admitted group.
func TestManager_Open_Groups_WritesGroupedCohort(t *testing.T) {
	m, afs, _ := newTestManager(t)
	writeJoinCSV(t, afs, "orders.csv", 200, 10)
	res, err := m.Open(context.Background(), Spec{SourcePath: "orders.csv", Groups: []pio.GroupDecl{custGroup}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := versionByte(t, afs, res.Path); got != 0x02 {
		t.Fatalf("version byte = 0x%02x, want 0x02 for a grouped cohort", got)
	}
	if len(res.Groups) != 1 || res.Groups[0].Verdict != encoding.GroupVerdictAdmitted || res.Groups[0].EntryCount != 10 {
		t.Fatalf("Groups = %+v, want one admitted group of 10 tuples", res.Groups)
	}
	if len(res.GroupWarnings) != 0 {
		t.Errorf("GroupWarnings = %v, want none for a 20x group", res.GroupWarnings)
	}
	if res.GroupCandidates != nil {
		t.Errorf("GroupCandidates = %+v without SuggestGroups, want nil", res.GroupCandidates)
	}
}

// TestManager_Open_Groups_LowRatioWarnsAtDefaultFloor: the managed path
// applies the default 2.0 floor — a 1.67x group is written and warned.
func TestManager_Open_Groups_LowRatioWarnsAtDefaultFloor(t *testing.T) {
	m, afs, _ := newTestManager(t)
	writeJoinCSV(t, afs, "orders.csv", 20, 12)
	res, err := m.Open(context.Background(), Spec{SourcePath: "orders.csv", Groups: []pio.GroupDecl{custGroup}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(res.GroupWarnings) != 1 || res.GroupWarnings[0].Code != perr.PULSE_DEDUP_LOW_RATIO {
		t.Fatalf("GroupWarnings = %v, want one PULSE_DEDUP_LOW_RATIO", res.GroupWarnings)
	}
	if res.Groups[0].RatioFloor != encoding.DefaultDedupRatioFloor {
		t.Errorf("RatioFloor = %v, want the default %v", res.Groups[0].RatioFloor, encoding.DefaultDedupRatioFloor)
	}
}

// TestManager_Open_NoGroups_ByteIdentical: omitting groups writes exactly
// the cohort a plain ImportJob writes over the same source.
func TestManager_Open_NoGroups_ByteIdentical(t *testing.T) {
	m, afs, _ := newTestManager(t)
	writeJoinCSV(t, afs, "orders.csv", 200, 10)
	res, err := m.Open(context.Background(), Spec{SourcePath: "orders.csv"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	reader, err := pio.NewReader(pio.FormatCSV, afs, "orders.csv", pio.ReaderOptions{})
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	defer reader.Close()
	job := pio.NewImportJob(reader, "direct.pulse")
	job.FS = afs
	job.SetInferenceMinPct = m.effectiveSetInferenceMinPct(0)
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("direct Run: %v", err)
	}
	managed, _ := afero.ReadFile(afs, res.Path)
	direct, _ := afero.ReadFile(afs, "direct.pulse")
	if !bytes.Equal(managed, direct) {
		t.Fatalf("managed cohort (%d bytes) differs from the plain import (%d bytes)", len(managed), len(direct))
	}
	if versionByte(t, afs, res.Path) != 0x01 {
		t.Errorf("version byte != 0x01 with no groups")
	}
}

// TestManager_Open_Groups_ReimportNeedsOverwrite: a handle already
// imported flat is not silently reused (or replaced) by a grouped
// re-import — it collides like any other, and overwrite re-encodes it.
func TestManager_Open_Groups_ReimportNeedsOverwrite(t *testing.T) {
	m, afs, _ := newTestManager(t)
	writeJoinCSV(t, afs, "orders.csv", 200, 10)
	if _, err := m.Open(context.Background(), Spec{SourcePath: "orders.csv"}); err != nil {
		t.Fatalf("first Open: %v", err)
	}
	grouped := Spec{SourcePath: "orders.csv", Groups: []pio.GroupDecl{custGroup}}
	_, err := m.Open(context.Background(), grouped)
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_IMPORT_HANDLE_EXISTS {
		t.Fatalf("grouped re-import err = %v, want PULSE_IMPORT_HANDLE_EXISTS", err)
	}
	if versionByte(t, afs, m.handlePath("orders")) != 0x01 {
		t.Fatalf("refused re-import changed the existing cohort")
	}
	grouped.Overwrite = true
	res, err := m.Open(context.Background(), grouped)
	if err != nil {
		t.Fatalf("overwrite Open: %v", err)
	}
	if versionByte(t, afs, res.Path) != 0x02 {
		t.Errorf("overwrite did not re-encode the handle grouped")
	}
}

// TestManager_Open_SuggestGroups: detection runs on the managed path and
// suggests the fixture's customer group, without declaring it.
func TestManager_Open_SuggestGroups(t *testing.T) {
	m, afs, _ := newTestManager(t)
	writeJoinCSV(t, afs, "orders.csv", 200, 10)
	res, err := m.Open(context.Background(), Spec{SourcePath: "orders.csv", SuggestGroups: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if res.GroupCandidates == nil {
		t.Fatalf("GroupCandidates nil with SuggestGroups set")
	}
	found := false
	for _, c := range res.GroupCandidates.Candidates {
		if c.Suggested && len(c.Key) == 1 && c.Key[0] == "cust_id" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no suggested cust_id candidate in %+v", res.GroupCandidates.Candidates)
	}
	if versionByte(t, afs, res.Path) != 0x01 || len(res.Groups) != 0 {
		t.Errorf("SuggestGroups declared a group; it must only suggest")
	}
}

// TestManager_Open_SuggestGroups_ComplementsDeclared: detection sees
// the declaration, so no candidate re-proposes a field already grouped —
// every candidate is an ADDITION to the current Groups.
func TestManager_Open_SuggestGroups_ComplementsDeclared(t *testing.T) {
	m, afs, _ := newTestManager(t)
	writeJoinCSV(t, afs, "orders.csv", 200, 10)
	res, err := m.Open(context.Background(), Spec{SourcePath: "orders.csv", SuggestGroups: true, Groups: []pio.GroupDecl{custGroup}})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if res.GroupCandidates == nil {
		t.Fatalf("GroupCandidates nil with SuggestGroups set")
	}
	declared := map[string]bool{"cust_id": true, "cust_lat": true, "cust_lon": true}
	for _, c := range res.GroupCandidates.Candidates {
		for _, f := range append(append([]string(nil), c.Key...), c.Members...) {
			if declared[f] {
				t.Errorf("candidate %s re-proposes declared field %q", c.Label, f)
			}
		}
	}
}

// TestManager_Open_Groups_PulsePassthroughRejected: a .pulse source is
// never re-encoded, so a group declaration on one is refused rather than
// silently ignored.
func TestManager_Open_Groups_PulsePassthroughRejected(t *testing.T) {
	m, afs, _ := newTestManager(t)
	if err := afero.WriteFile(afs, "curated.pulse", []byte("PULSE\x00\x00\x00\x01"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := m.Open(context.Background(), Spec{SourcePath: "curated.pulse", Groups: []pio.GroupDecl{custGroup}})
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_GROUP_DECLARATION_INVALID {
		t.Fatalf("err = %v, want PULSE_GROUP_DECLARATION_INVALID", err)
	}
}
