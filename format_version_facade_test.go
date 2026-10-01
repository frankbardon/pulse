package pulse

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/io/csv"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// upgradeToV2 rewrites a 0x01 cohort's bytes into the 0x02 layout with
// an empty schema extension block: version byte 0x02, the same field
// descriptors, then u64(2) + u16(0) (an extension holding zero
// sections), then the unchanged record region. It holds a 0x02 cohort
// that uses no 0x02 feature — exactly what every reader must thread the
// header version through to parse.
func upgradeToV2(t *testing.T, v1 []byte) []byte {
	t.Helper()
	r := bytes.NewReader(v1)
	if _, v, err := encoding.ReadPreamble(r); err != nil || v != 0x01 {
		t.Fatalf("source is not a 0x01 cohort: v=0x%02x err=%v", v, err)
	}
	end := len(v1) - r.Len()
	out := append([]byte{}, v1[:encoding.HeaderSize-1]...)
	out = append(out, encoding.FormatVersionV2)
	out = append(out, v1[encoding.HeaderSize:end]...)
	out = append(out, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	return append(out, v1[end:]...)
}

// formatTwinFS builds two in-memory filesystems holding the SAME cohort
// under the same name — one at 0x01, one at 0x02 — so every facade
// output can be compared byte-for-byte (names included).
func formatTwinFS(t *testing.T) (v1, v2 afero.Fs) {
	t.Helper()
	cols := []string{"id", "region", "amount", "score"}
	var rows [][]string
	for i := 0; i < 40; i++ {
		rows = append(rows, []string{
			fmt.Sprint(i + 1),
			[]string{"north", "south", "east"}[i%3],
			fmt.Sprintf("%d.%02d", 10+i, i%100),
			fmt.Sprint((i * 7) % 11),
		})
	}
	v1 = afero.NewMemMapFs()
	createTestPulseFile(t, v1, "cohort.pulse", cols, rows)
	raw, err := afero.ReadFile(v1, "cohort.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if raw[encoding.HeaderSize-1] != 0x01 {
		t.Fatalf("import wrote version 0x%02x, want 0x01 — no 0x02 feature is in use", raw[encoding.HeaderSize-1])
	}
	v2 = afero.NewMemMapFs()
	if err := afero.WriteFile(v2, "cohort.pulse", upgradeToV2(t, raw), 0o644); err != nil {
		t.Fatal(err)
	}
	return v1, v2
}

func mustJSON(t *testing.T, label string, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("%s: marshal: %v", label, err)
	}
	return string(b)
}

// logicalInspect is the LOGICAL view of an inspect result: a copy with
// the physical-layout additions a 0x02 cohort carries (Layout, Groups
// and the per-field Group marker) removed. Inspect deliberately reports
// how a cohort is stored, so the twins differ there by design; every
// other key must stay identical, which is what the parity probes pin.
// The additions themselves are pinned by descriptor's inspect group
// tests and TestInspect_GroupFiguresMatchImportReport.
func logicalInspect(res *descriptor.InspectResult) *descriptor.InspectResult {
	cp := *res
	cp.Layout, cp.Groups = nil, nil
	cp.Fields = make([]*descriptor.InspectField, len(res.Fields))
	for i, f := range res.Fields {
		fc := *f
		fc.Group = nil
		cp.Fields[i] = &fc
	}
	return &cp
}

// formatProbe is one facade read path exercised against a cohort.
type formatProbe struct {
	name string
	run  func(p *Pulse, fsys afero.Fs) (any, error)
}

// formatProbes lists every facade read path that parses a cohort's
// header + schema: descriptor (inspect, predict, facet schema), the
// buffered and streaming engines, the header-fast counter, sampling,
// profiling, export, point lookup, filter-to-file and the shard-archive
// admin + iteration paths.
func formatProbes(ctx context.Context) []formatProbe {
	req := func() *Request {
		return &Request{
			Cohort:       &types.Cohort{Filename: "cohort.pulse"},
			Filterers:    []*types.Filterer{{Type: types.FILTER_RANGE, Field: "score", Values: []string{"2", "9"}}},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount"}, {Type: types.AGG_COUNT, Field: "id"}},
		}
	}
	return []formatProbe{
		{"Inspect", func(p *Pulse, _ afero.Fs) (any, error) {
			res, err := p.Inspect(ctx, "cohort.pulse")
			if err != nil {
				return nil, err
			}
			return logicalInspect(res), nil
		}},
		{"InspectEnvelope", func(p *Pulse, _ afero.Fs) (any, error) {
			env, err := p.InspectEnvelope(ctx, "cohort.pulse", nil)
			if err == nil && len(env.Errors) > 0 {
				return nil, fmt.Errorf("%s: %s", env.Errors[0].Code, env.Errors[0].Message)
			}
			if err == nil {
				cp := *env
				cp.Data = logicalInspect(env.Data.(*descriptor.InspectResult))
				return &cp, nil
			}
			return env, err
		}},
		{"FacetSchema", func(p *Pulse, _ afero.Fs) (any, error) {
			return p.FacetSchema(ctx, &FacetRequest{Cohort: &types.Cohort{Filename: "cohort.pulse"}, Fields: []string{"region", "score"}})
		}},
		{"CountRecords", func(p *Pulse, _ afero.Fs) (any, error) { return p.CountRecords(ctx, "cohort.pulse") }},
		{"Open", func(p *Pulse, _ afero.Fs) (any, error) {
			c, err := p.Open(ctx, "cohort.pulse")
			if err != nil {
				return nil, err
			}
			n, err := c.RecordCount()
			return []any{c.Schema().Fields, n}, err
		}},
		{"Predict", func(p *Pulse, _ afero.Fs) (any, error) {
			res, err := p.Predict(ctx, req())
			if err == nil && !res.Valid {
				return nil, fmt.Errorf("predict: invalid: %s", mustJSONErr(res))
			}
			return res, err
		}},
		{"Process", func(p *Pulse, _ afero.Fs) (any, error) { return p.Process(ctx, req()) }},
		{"ProcessStream", func(p *Pulse, _ afero.Fs) (any, error) {
			it, err := p.ProcessStream(ctx, req())
			if err != nil {
				return nil, err
			}
			defer it.Close()
			var rows []any
			for {
				row, ok, err := it.Next(ctx)
				if err != nil {
					return nil, err
				}
				if !ok {
					break
				}
				rows = append(rows, row)
			}
			return rows, nil
		}},
		{"Sample", func(p *Pulse, _ afero.Fs) (any, error) { return p.Sample(ctx, "cohort.pulse", 5) }},
		{"Facet", func(p *Pulse, _ afero.Fs) (any, error) { return p.Facet(ctx, "cohort.pulse", "region") }},
		{"Profile", func(p *Pulse, _ afero.Fs) (any, error) {
			return p.Profile(ctx, "cohort.pulse", ProfileOptions{RunContinuation: true})
		}},
		{"Export", func(p *Pulse, _ afero.Fs) (any, error) {
			w := csv.NewWriterToBuffer()
			if _, err := p.Export(ctx, pio.NewExportJob("cohort.pulse", w)); err != nil {
				return nil, err
			}
			return string(w.Bytes()), nil
		}},
		{"Lookup", func(p *Pulse, _ afero.Fs) (any, error) {
			if _, err := p.BuildIndex(ctx, "cohort.pulse", []string{"id"}); err != nil {
				return nil, err
			}
			return p.Lookup(ctx, &LookupRequest{Cohort: &types.Cohort{Filename: "cohort.pulse"}, Field: "id", Value: "17"})
		}},
		{"FilterToFile", func(p *Pulse, fsys afero.Fs) (any, error) {
			n, err := p.FilterToFile(ctx, "cohort.pulse", "filtered.pulse", "score > 5")
			if err != nil {
				return nil, err
			}
			sample, err := p.Sample(ctx, "filtered.pulse", 100)
			return []any{n, sample}, err
		}},
		{"ShardArchive", func(p *Pulse, fsys afero.Fs) (any, error) {
			raw, err := afero.ReadFile(fsys, "cohort.pulse")
			if err != nil {
				return nil, err
			}
			if err := afero.WriteFile(fsys, "s2.pulse", raw, 0o644); err != nil {
				return nil, err
			}
			if _, err := p.CreateShardArchive(ctx, "arch.pulse", []string{"cohort.pulse", "s2.pulse"}); err != nil {
				return nil, err
			}
			r := req()
			r.Cohort = &types.Cohort{Filename: "arch.pulse"}
			return p.Process(ctx, r)
		}},
	}
}

// TestFormatV2_FacadeParity drives a 0x02 cohort through every facade
// read path that parses a header + schema and asserts each output is
// identical to the 0x01 twin. A read site that dropped the header's
// version would parse the 0x02 schema block as 0x01, start the record
// region four bytes early, and diverge (or fail) here.
func TestFormatV2_FacadeParity(t *testing.T) {
	ctx := context.Background()
	fs1, fs2 := formatTwinFS(t)

	probes := formatProbes(ctx)

	for _, pr := range probes {
		t.Run(pr.name, func(t *testing.T) {
			var out [2]string
			for i, fsys := range []afero.Fs{fs1, fs2} {
				p, err := New(Options{FS: fsys})
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				got, err := pr.run(p, fsys)
				if err != nil {
					t.Fatalf("%s on 0x0%d cohort: %v", pr.name, i+1, err)
				}
				out[i] = mustJSON(t, pr.name, got)
			}
			if out[0] != out[1] {
				t.Fatalf("%s differs between 0x01 and 0x02:\n 0x01: %s\n 0x02: %s", pr.name, out[0], out[1])
			}
		})
	}

	// FilterToFile copies the source preamble verbatim, so a 0x02 source
	// yields a 0x02 destination whose record region is still correct.
	dst, err := afero.ReadFile(fs2, "filtered.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if dst[encoding.HeaderSize-1] != 0x02 {
		t.Fatalf("filtered copy of a 0x02 cohort has version 0x%02x, want 0x02", dst[encoding.HeaderSize-1])
	}
}

// TestFormatV2_UnsupportedVersionIsCoded: a cohort declaring a version
// this binary does not read (what a pre-0x02 binary sees for every 0x02
// file) surfaces as a coded ENCODING_INVALID naming the version on the
// facade — never a silent misparse.
func TestFormatV2_UnsupportedVersionIsCoded(t *testing.T) {
	ctx := context.Background()
	fs1, _ := formatTwinFS(t)
	raw, err := afero.ReadFile(fs1, "cohort.pulse")
	if err != nil {
		t.Fatal(err)
	}
	raw[encoding.HeaderSize-1] = 0x03
	if err := afero.WriteFile(fs1, "future.pulse", raw, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := New(Options{FS: fs1})
	if err != nil {
		t.Fatal(err)
	}
	_, perr := p.Process(ctx, &Request{
		Cohort:       &types.Cohort{Filename: "future.pulse"},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id"}},
	})
	env, err := p.InspectEnvelope(ctx, "future.pulse", nil)
	if err != nil {
		t.Fatalf("InspectEnvelope: %v", err)
	}
	if len(env.Errors) != 1 || env.Errors[0].Code != string(perrors.ENCODING_INVALID) ||
		env.Errors[0].Details["version"] != byte(0x03) ||
		!strings.Contains(env.Errors[0].Message, "unsupported pulse format version") {
		t.Fatalf("InspectEnvelope errors = %+v, want one ENCODING_INVALID naming version 0x03", env.Errors)
	}
	for name, err := range map[string]error{"Process": perr} {
		if !perrors.HasCode(err, perrors.ENCODING_INVALID) {
			t.Fatalf("%s: err = %v, want coded ENCODING_INVALID", name, err)
		}
		if !strings.Contains(err.Error(), "unsupported pulse format version") {
			t.Errorf("%s: message %q does not say the version is unsupported", name, err.Error())
		}
		if v, ok := versionDetail(err); !ok || v != 0x03 {
			t.Errorf("%s: no coded error in the chain names version 0x03 (got %v, %v)", name, v, ok)
		}
	}
}

// versionDetail walks the error chain for a coded error carrying
// details["version"].
func versionDetail(err error) (byte, bool) {
	for err != nil {
		if ce, ok := err.(*perrors.CodedError); ok {
			if v, ok := ce.Details["version"].(byte); ok {
				return v, true
			}
		}
		err = stderrors.Unwrap(err)
	}
	return 0, false
}

// TestFormatV2_FacadeRefusesUnknownExtension: a 0x02 cohort whose schema
// extension block carries bytes this binary does not understand must be
// refused on EVERY facade read path. A site that dropped the header's
// version would parse the schema block with the 0x01 layout, never reach
// the extension, and succeed — which is exactly the silent failure the
// version threading exists to prevent once the extension carries the
// group descriptor. This is what pins the schema-only read sites, which
// the parity test cannot see while the extension is empty.
func TestFormatV2_FacadeRefusesUnknownExtension(t *testing.T) {
	ctx := context.Background()
	_, fs2 := formatTwinFS(t)
	raw, err := afero.ReadFile(fs2, "cohort.pulse")
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(raw)
	if _, err := encoding.ReadHeader(r); err != nil {
		t.Fatal(err)
	}
	if _, err := encoding.ReadSchema(r, encoding.FormatVersionV1); err != nil {
		t.Fatal(err)
	}
	extAt := len(raw) - r.Len() // start of the u64 extension length
	bad := append([]byte{}, raw[:extAt]...)
	bad = append(bad, 4, 0, 0, 0, 0, 0, 0, 0, 0xDE, 0xAD, 0xBE, 0xEF)
	bad = append(bad, raw[extAt+10:]...)

	for _, pr := range formatProbes(ctx) {
		t.Run(pr.name, func(t *testing.T) {
			fsys := afero.NewMemMapFs()
			if err := afero.WriteFile(fsys, "cohort.pulse", bad, 0o644); err != nil {
				t.Fatal(err)
			}
			p, err := New(Options{FS: fsys})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			got, err := pr.run(p, fsys)
			if err == nil {
				t.Fatalf("%s accepted a 0x02 cohort with an unknown schema extension (got %s)", pr.name, mustJSON(t, pr.name, got))
			}
		})
	}
}

func mustJSONErr(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
