package pulse

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// TestInspect_GroupFiguresMatchImportReport: the figures inspect derives
// from a grouped cohort's header, schema and file length are the SAME
// numbers the import that wrote it reported — one admitted group, one
// low-ratio group, plus elided constants — through InspectEnvelope (the
// only surface that keeps warnings) with a clean envelope.
func TestInspect_GroupFiguresMatchImportReport(t *testing.T) {
	cols := []string{"id", "parent_code", "parent_label", "parent_weight", "pair_a", "pair_b", "amount", "source"}
	var rows [][]string
	for i := 0; i < 240; i++ {
		p := i / 8
		rows = append(rows, []string{
			fmt.Sprint(i + 1),
			fmt.Sprint(100000 + p),
			[]string{"alpha", "beta", "gamma"}[p%3],
			fmt.Sprint(5000000 + p*13),
			fmt.Sprint(7000000000 + i%120), // u64 pairs repeating twice: ratio 2
			fmt.Sprint(9000000000 + i%120),
			fmt.Sprintf("%d.%02d", 10+i%37, i%100),
			"batch-a",
		})
	}
	fsys := afero.NewMemMapFs()
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	job := pio.NewImportJob(newMockReader(cols, rows), "cohort.pulse")
	job.ElideConstants = true
	job.DedupRatioFloor = 3
	job.Groups = []pio.GroupDecl{
		{Key: []string{"parent_code"}, Members: []string{"parent_label", "parent_weight"}},
		{Members: []string{"pair_a", "pair_b"}},
	}
	rep, err := p.Import(context.Background(), job)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(rep.Groups) != 2 || len(rep.ElidedConstants) == 0 {
		t.Fatalf("fixture: import groups=%+v elided=%v", rep.Groups, rep.ElidedConstants)
	}

	env, err := p.InspectEnvelope(context.Background(), "cohort.pulse", nil)
	if err != nil || len(env.Errors) != 0 || len(env.Warnings) != 0 {
		t.Fatalf("InspectEnvelope: err=%v errors=%+v warnings=%+v", err, env.Errors, env.Warnings)
	}
	res := env.Data.(*descriptor.InspectResult)
	if res.RecordCount != 240 || res.Layout == nil {
		t.Fatalf("record_count=%d layout=%+v", res.RecordCount, res.Layout)
	}
	if len(res.Groups) != 3 {
		t.Fatalf("inspect groups = %d, want 2 declared + 1 constant", len(res.Groups))
	}

	for i, want := range rep.Groups {
		got := res.Groups[i]
		if got.Label != want.Label ||
			strings.Join(got.Key, ",") != strings.Join(want.Key, ",") ||
			strings.Join(got.Members, ",") != strings.Join(want.Members, ",") {
			t.Errorf("group %d naming: inspect %q key=%v members=%v, import %q key=%v members=%v",
				i, got.Label, got.Key, got.Members, want.Label, want.Key, want.Members)
		}
		if got.EntryCount != want.EntryCount || got.EntryWidth != want.EntryWidth ||
			got.MemberRowBytes != want.MemberRowBytes || got.IndexWidth != want.IndexWidth ||
			got.DictionaryBytes != want.DictionaryBytes || got.Ratio != want.Ratio ||
			got.BreakEvenRatio != want.BreakEvenRatio || got.ByteDelta != want.ByteDelta {
			t.Errorf("group %d figures differ:\n inspect %+v\n import  %+v", i, got, want)
		}
	}
	// The import judged against a floor of 3; inspect judges against the
	// default floor of 2, so the ratio-2 pair group is low only at import.
	if rep.Groups[1].Verdict != "low_ratio" || res.Groups[1].Verdict != "admitted" {
		t.Errorf("pair group verdicts import=%s inspect=%s, want low_ratio / admitted (floors 3 vs 2)",
			rep.Groups[1].Verdict, res.Groups[1].Verdict)
	}
	if res.Groups[0].Verdict != "admitted" || res.Groups[0].Ratio != 8 {
		t.Errorf("parent group verdict=%s ratio=%v, want admitted 8", res.Groups[0].Verdict, res.Groups[0].Ratio)
	}
	if c := res.Groups[2]; c.Kind != "constant" || strings.Join(c.Fields, ",") != strings.Join(rep.ElidedConstants, ",") {
		t.Errorf("constant group = %+v, elided = %v", c, rep.ElidedConstants)
	}
}
