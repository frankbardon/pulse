package pulse

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/io/csv"
	"github.com/frankbardon/pulse/internal/processing"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// preambleOf returns a cohort file's header + schema bytes and its
// parsed schema.
func preambleOf(t *testing.T, raw []byte) ([]byte, *encoding.Schema, byte) {
	t.Helper()
	br := bytes.NewReader(raw)
	s, v, err := encx.ReadPreamble(br)
	if err != nil {
		t.Fatalf("ReadPreamble: %v", err)
	}
	return raw[:len(raw)-br.Len()], s, v
}

// filterToFileProbes are the read paths a filtered output must answer
// identically to its reference: streaming and buffered Process over
// member and child fields, and a full CSV export of every field.
func filterToFileProbes(ctx context.Context, path string) []formatProbe {
	req := func(extra ...*types.Aggregation) *Request {
		return &Request{
			Cohort: &types.Cohort{Filename: path},
			Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}, {Type: types.GROUP_CATEGORY, Field: "source"}},
			Aggregations: append([]*types.Aggregation{
				{Type: types.AGG_SUM, Field: "amount"},
				{Type: types.AGG_COUNT, Field: "id"},
				{Type: types.AGG_FREQUENCY, Field: "region"},
			}, extra...),
		}
	}
	return []formatProbe{
		{"ProcessStreaming", func(p *Pulse, _ afero.Fs) (any, error) { return p.Process(ctx, req()) }},
		{"ProcessBuffered", func(p *Pulse, _ afero.Fs) (any, error) {
			return p.Process(ctx, req(&types.Aggregation{Type: types.AGG_MEDIAN, Field: "amount"}))
		}},
		{"ExportCSV", func(p *Pulse, _ afero.Fs) (any, error) {
			w := csv.NewWriterToBuffer()
			if _, err := p.Export(ctx, pio.NewExportJob(path, w)); err != nil {
				return nil, err
			}
			return string(w.Bytes()), nil
		}},
	}
}

// TestGroupedCohort_FilterToFile (E5-S2): filter-to-file over a deduped
// cohort — an indexed parent group and constant elision — emits a valid
// 0x02 cohort whose preamble (header, schema, group descriptors and
// dictionaries) is the source's byte-for-byte, and whose rows are the
// surviving physical rows, so every entry index stays valid against the
// carried dictionary. The output answers every probe exactly as the
// same surviving rows deduped afresh with the same groups (the pruned
// reference) and as the 0x01 twin filtered the same way — whether the
// predicate reads only child fields (the members are projected out of
// the decode), members, or both. A filter that keeps nothing still
// emits a valid, empty 0x02 cohort. Unreferenced entries are carried,
// so inspect's ratio is surviving rows ÷ CARRIED entries.
func TestGroupedCohort_FilterToFile(t *testing.T) {
	ctx := context.Background()
	cases := []struct{ name, expr string }{
		{"ChildOnly", "score > 5"},
		{"MemberOnly", `region == "north" || region == "east"`},
		{"Mixed", `score > 5 && region == "west"`},
		{"Empty", "score > 100"},
	}
	for _, tw := range strideTwins(t) {
		srcRaw, _ := afero.ReadFile(tw.v2, "cohort.pulse")
		srcPre, srcSchema, _ := preambleOf(t, srcRaw)
		flatRaw, _ := afero.ReadFile(tw.flat, "cohort.pulse")
		flatPre, _, _ := preambleOf(t, flatRaw)
		specs := make([]encx.GroupSpec, len(srcSchema.Groups))
		for g := range specs {
			specs[g] = encx.GroupSpecOf(srcSchema, g)
		}
		for _, c := range cases {
			t.Run(tw.name+"/"+c.name, func(t *testing.T) {
				pv2, err := New(Options{FS: tw.v2})
				if err != nil {
					t.Fatal(err)
				}
				pflat, err := New(Options{FS: tw.flat})
				if err != nil {
					t.Fatal(err)
				}
				kept, err := pv2.FilterToFile(ctx, "cohort.pulse", "out.pulse", c.expr)
				if err != nil {
					t.Fatalf("FilterToFile(0x02): %v", err)
				}
				flatKept, err := pflat.FilterToFile(ctx, "cohort.pulse", "out.pulse", c.expr)
				if err != nil || flatKept != kept {
					t.Fatalf("FilterToFile(0x01) kept %d (err %v), 0x02 kept %d", flatKept, err, kept)
				}
				if (c.name == "Empty") != (kept == 0) {
					t.Fatalf("fixture: %s kept %d rows", c.name, kept)
				}

				// The preamble is carried verbatim: same version, same
				// groups, same dictionaries (unreferenced entries included).
				out, _ := afero.ReadFile(tw.v2, "out.pulse")
				outPre, outSchema, v := preambleOf(t, out)
				if v != encoding.FormatVersionV2 || !bytes.Equal(outPre, srcPre) {
					t.Fatalf("output preamble (version 0x%02x) is not the source's byte-for-byte", v)
				}
				if got, want := len(out)-len(outPre), int(kept)*outSchema.RecordByteSize(); got != want {
					t.Fatalf("output payload %d bytes, want %d rows x physical stride %d", got, kept, outSchema.RecordByteSize())
				}
				// The 0x01 arm filters exactly as it always has.
				flatOut, _ := afero.ReadFile(tw.flat, "out.pulse")
				if fp, _, fv := preambleOf(t, flatOut); fv != encoding.FormatVersionV1 || !bytes.Equal(fp, flatPre) {
					t.Fatalf("0x01 output preamble changed (version 0x%02x)", fv)
				}

				// Reference: the same surviving rows deduped afresh with the
				// same groups. A zero-row cohort has no value to hold a
				// constant group, so the empty case is referenced by the
				// filtered 0x01 twin alone.
				refFS := afero.NewMemMapFs()
				if kept > 0 {
					var ref bytes.Buffer
					if _, n, err := encx.DedupCohort(&ref, bytes.NewReader(flatOut), specs); err != nil || n != kept {
						t.Fatalf("reference dedup: %d rows, err %v", n, err)
					}
					_ = afero.WriteFile(refFS, "out.pulse", ref.Bytes(), 0o644)
					// The documented prune route: re-deduping the carried
					// output with the same groups yields the reference
					// byte-for-byte (unreferenced entries dropped).
					var pruned bytes.Buffer
					if _, _, err := encx.DedupCohort(&pruned, bytes.NewReader(out), specs); err != nil || !bytes.Equal(pruned.Bytes(), ref.Bytes()) {
						t.Fatalf("re-dedup of the carried output (err %v) is not the fresh dedup of the same rows", err)
					}
				} else {
					_ = afero.WriteFile(refFS, "out.pulse", flatOut, 0o644)
				}
				pref, err := New(Options{FS: refFS})
				if err != nil {
					t.Fatal(err)
				}

				for _, pr := range filterToFileProbes(ctx, "out.pulse") {
					var got [3]string
					for i, arm := range []struct {
						p *Pulse
						f afero.Fs
					}{{pv2, tw.v2}, {pref, refFS}, {pflat, tw.flat}} {
						res, err := pr.run(arm.p, arm.f)
						if err != nil {
							t.Fatalf("%s on arm %d: %v", pr.name, i, err)
						}
						got[i] = mustJSON(t, pr.name, res)
					}
					if got[0] != got[1] || got[0] != got[2] {
						t.Fatalf("%s: filtered 0x02 output differs:\n carried:   %s\n reference: %s\n 0x01:      %s", pr.name, got[0], got[1], got[2])
					}
				}

				// Count and inspect: surviving rows, carried dictionary.
				n, err := pv2.CountRecords(ctx, "out.pulse")
				if err != nil || n != uint64(kept) {
					t.Fatalf("CountRecords = %d (err %v), want %d", n, err, kept)
				}
				env, err := pv2.InspectEnvelope(ctx, "out.pulse", nil)
				if err != nil || len(env.Warnings) != 0 {
					t.Fatalf("inspect: err %v, warnings %s", err, mustJSON(t, "warnings", env.Warnings))
				}
				ins, err := pv2.Inspect(ctx, "out.pulse")
				if err != nil || ins.RecordCount != kept || len(ins.Groups) != len(srcSchema.Groups) {
					t.Fatalf("inspect: %d records, %d groups (err %v)", ins.RecordCount, len(ins.Groups), err)
				}
				for g, ig := range ins.Groups {
					if ig.EntryCount != srcSchema.GroupEntryCount(g) {
						t.Fatalf("group %d: inspect reports %d entries, want the %d carried", g, ig.EntryCount, srcSchema.GroupEntryCount(g))
					}
					if want := float64(kept) / float64(ig.EntryCount); ig.EntryCount > 0 && fmt.Sprintf("%.6f", ig.Ratio) != fmt.Sprintf("%.6f", want) {
						t.Fatalf("group %d: ratio %v, want surviving %d / carried %d", g, ig.Ratio, kept, ig.EntryCount)
					}
				}
			})
		}
	}
}

// TestGroupedCohort_FilterToFilePrecompute (E5-S2): filter-to-file's
// map decode hands each record its row's group entry indices, so a
// predicate over one group's members is evaluated once per dictionary
// entry (30 here), not once per row (360) — with the same output.
func TestGroupedCohort_FilterToFilePrecompute(t *testing.T) {
	ctx := context.Background()
	_, fs2 := groupedTwinFS(t)
	p, err := New(Options{FS: fs2})
	if err != nil {
		t.Fatal(err)
	}
	expr := `region == "north" || parent_code > 1025`
	var outs [2][]byte
	for i, on := range []bool{true, false} {
		prev := processing.SetFilterPrecompute(on)
		before := processing.FilterPrecomputeStats().EntryEvaluations
		if _, err := p.FilterToFile(ctx, "cohort.pulse", "out.pulse", expr); err != nil {
			processing.SetFilterPrecompute(prev)
			t.Fatal(err)
		}
		evals := processing.FilterPrecomputeStats().EntryEvaluations - before
		processing.SetFilterPrecompute(prev)
		outs[i], _ = afero.ReadFile(fs2, "out.pulse")
		want := int64(0)
		if on {
			want = 30
		}
		if evals != want {
			t.Fatalf("precompute=%v: %d entry evaluations, want %d", on, evals, want)
		}
	}
	if !bytes.Equal(outs[0], outs[1]) {
		t.Fatal("filter-to-file output differs with the precompute on and off")
	}
}
