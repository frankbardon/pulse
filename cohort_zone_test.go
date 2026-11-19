package pulse

import (
	"bytes"
	"context"
	stderrors "errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/synth"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// zoneSweepSchema: a datetime, the categorical zone field `tz`, a
// categorical `region` (rake margin, dedup key), a set (widen) and an
// id. ZoneField is the caller's.
func zoneSweepSchema(zone string) encoding.Schema {
	return encoding.Schema{Fields: []encoding.Field{
		{Name: "at", Type: encoding.FieldTypeDateTime, Description: "Event instant"},
		{Name: "tz", Type: encoding.FieldTypeCategoricalU8, Description: "IANA zone of the event"},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Description: "Sales region"},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Description: "Selected tags"},
		{Name: "id", Type: encoding.FieldTypeU32, Description: "Record id"},
	}, ZoneField: zone}
}

func zoneSweepRows(from, n int) []CohortRow {
	zones := []string{"Europe/Berlin", "America/New_York", "UTC"}
	regions := []string{"north", "south"}
	out := make([]CohortRow, n)
	for i := range out {
		k := from + i
		out[i] = CohortRow{int64(1_700_000_000 + 5_400*k), zones[k%3], regions[k%2], []string{"a"}, uint64(k)}
	}
	return out
}

// zoneOfCohort is the default zone field Inspect reports for path.
func zoneOfCohort(t *testing.T, p *Pulse, path string) string {
	t.Helper()
	ins, err := p.Inspect(context.Background(), path)
	if err != nil {
		t.Fatalf("Inspect %s: %v", path, err)
	}
	return ins.ZoneField
}

// TestCohortZone_EveryRewriterPreservesDefault is the preservation
// sweep: every writer that builds a cohort FROM a declared cohort keeps
// its default zone field — the builder (single file, sharded output,
// anchored append), dedup (in place and --out), materialize (plain,
// renamed column, sharded output), filter-to-file, widen, rake, synth
// augment (tagged and synthetic-only), shard create / add (append and
// rewrite) / remove / compact / extract, and the set writer itself. A
// rewriter added later is held to the same rule by
// TestCohortZone_SchemaLiteralsCarryZoneField.
func TestCohortZone_EveryRewriterPreservesDefault(t *testing.T) {
	t.Parallel()

	p, fsys := memBuilderEngine(t)
	ctx := context.Background()
	buildCohort(t, p, "src.pulse", zoneSweepSchema("tz"), CohortBuilderOptions{}, zoneSweepRows(0, 12))
	buildCohort(t, p, "more.pulse", zoneSweepSchema("tz"), CohortBuilderOptions{}, zoneSweepRows(12, 6))
	buildCohort(t, p, "extra.pulse", zoneSweepSchema("tz"), CohortBuilderOptions{}, zoneSweepRows(18, 4))
	buildCohort(t, p, "split.pulse", zoneSweepSchema("tz"), CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 5}}, zoneSweepRows(0, 12))
	copyFile := func(src, dst string) {
		if err := afero.WriteFile(fsys, dst, readFile(t, fsys, src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	src := &types.Cohort{Filename: "src.pulse"}

	copyFile("src.pulse", "dedup_inplace.pulse")
	ires, err := p.Dedup(ctx, "dedup_inplace.pulse", DedupOptions{Groups: []pio.GroupDecl{{Key: []string{"id"}, Members: []string{"region"}}}})
	must("dedup in place", err)
	if !ires.Rewritten || len(ires.Groups) == 0 || ires.Groups[0].Verdict == "dropped_too_narrow" {
		t.Fatalf("dedup in place did not regroup: %+v", ires)
	}
	dres, err := p.Dedup(ctx, "src.pulse", DedupOptions{Out: "dedup_out.pulse", ElideConstants: true,
		Groups: []pio.GroupDecl{{Members: []string{"at", "tz", "region"}}}})
	must("dedup --out", err)
	if !dres.Rewritten {
		t.Fatalf("dedup --out wrote nothing: %+v", dres)
	}
	_, err = p.Materialize(ctx, &Request{Cohort: src, Features: []*types.Feature{{Type: types.FEAT_LOG, Field: "id", Label: "log_id"}}},
		"materialized.pulse", MaterializeOptions{})
	must("materialize", err)
	_, err = p.Materialize(ctx, &Request{Cohort: src, Output: &types.MaterializeOutput{
		Columns: []types.MaterializeOutputColumn{{From: "at"}, {From: "tz", As: "zone"}, {From: "id"}},
		Shards:  &types.MaterializeShards{MaxRecords: 5}}}, "materialized_split.pulse", MaterializeOptions{})
	must("materialize renamed + sharded", err)
	_, err = p.FilterToFile(ctx, "src.pulse", "filtered.pulse", "id >= 3")
	must("filter-to-file", err)
	copyFile("src.pulse", "widened.pulse")
	_, err = p.WidenSetField(ctx, "widened.pulse", "tags", "set_u16")
	must("widen", err)
	_, err = p.Rake(ctx, RakeRequest{Cohort: "src.pulse", Out: "raked.pulse",
		Margins: []RakeMargin{{Field: "region", Targets: map[string]float64{"north": .5, "south": .5}}}})
	must("rake", err)
	prof, err := p.Profile(ctx, "src.pulse", ProfileOptions{})
	must("profile", err)
	spec, _ := synth.SpecFromProfile(prof, 6)
	_, err = p.Synth(ctx, spec, "augmented.pulse", SynthOptions{Seed: 3, SourceCohort: "src.pulse"})
	must("synth augment", err)
	_, err = p.Synth(ctx, spec, "synthetic_only.pulse", SynthOptions{Seed: 3, SourceCohort: "src.pulse", SyntheticOnly: true})
	must("synth --synthetic-only", err)

	_, err = p.CreateShardArchive(ctx, "arch.pulse", []string{"src.pulse"})
	must("shard create", err)
	_, err = p.AddShard(ctx, "arch.pulse", "more.pulse")
	must("shard add (append)", err)
	_, err = p.AddShardWithOptions(ctx, "arch.pulse", "extra.pulse", AddShardOptions{Rewrite: true})
	must("shard add (rewrite)", err)
	buildCohort(t, p, "arch.pulse#anchored.pulse", zoneSweepSchema("tz"), CohortBuilderOptions{}, zoneSweepRows(22, 3))
	_, err = p.RemoveShard(ctx, "arch.pulse", "more.pulse")
	must("shard remove", err)
	must("shard compact", p.CompactShardArchive(ctx, "arch.pulse"))
	rc, err := p.ExtractShard(ctx, "arch.pulse", "extra.pulse")
	must("shard extract", err)
	extracted, err := io.ReadAll(rc)
	_ = rc.Close()
	must("shard extract read", err)
	must("write extracted", afero.WriteFile(fsys, "extracted.pulse", extracted, 0o644))
	ver, err := p.VerifyShardArchive(ctx, "arch.pulse")
	if err != nil || len(ver.Errors) != 0 {
		t.Fatalf("verify after every shard op: %v %+v", err, ver)
	}

	want := map[string]string{
		"src.pulse": "tz", "split.pulse": "tz", "dedup_inplace.pulse": "tz", "dedup_out.pulse": "tz",
		"materialized.pulse": "tz", "materialized_split.pulse": "zone", "filtered.pulse": "tz",
		"widened.pulse": "tz", "raked.pulse": "tz", "augmented.pulse": "tz", "synthetic_only.pulse": "tz",
		"arch.pulse": "tz", "arch.pulse#extra.pulse": "tz", "arch.pulse#anchored.pulse": "tz",
		"extracted.pulse": "tz",
	}
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if got := zoneOfCohort(t, p, name); got != want[name] {
			t.Errorf("%s declares default zone field %q, want %q", name, got, want[name])
		}
	}
}

// TestCohortZone_SetClearRoundTrip: set then clear returns the original
// bytes; the result reports both sides; a re-set is a no-op; the
// point-lookup index the rewrite stales is listed, never rebuilt.
func TestCohortZone_SetClearRoundTrip(t *testing.T) {
	t.Parallel()

	p, fsys := memBuilderEngine(t)
	ctx := context.Background()
	buildCohort(t, p, "c.pulse", zoneSweepSchema(""), CohortBuilderOptions{}, zoneSweepRows(0, 6))
	if _, err := p.BuildIndex(ctx, "c.pulse", []string{"id"}); err != nil {
		t.Fatal(err)
	}
	orig := readFile(t, fsys, "c.pulse")
	set, err := p.SetCohortZoneField(ctx, "c.pulse", "tz")
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if !set.Changed || set.ZoneField != "tz" || set.PreviousZoneField != "" || set.Layout != "single_file" ||
		len(set.Warnings) != 0 || len(set.InvalidatedSidecars) != 1 || set.InvalidatedSidecars[0].Kind != SidecarKindPointLookupIndex {
		t.Fatalf("set result %+v", set)
	}
	if got := zoneOfCohort(t, p, "c.pulse"); got != "tz" {
		t.Fatalf("inspect after set: %q", got)
	}
	again, err := p.SetCohortZoneField(ctx, "c.pulse", "tz")
	if err != nil || again.Changed || len(again.InvalidatedSidecars) != 0 {
		t.Fatalf("re-set = %+v, %v; want an unchanged no-op", again, err)
	}
	cleared, err := p.ClearCohortZoneField(ctx, "c.pulse")
	if err != nil || !cleared.Changed || cleared.PreviousZoneField != "tz" || cleared.ZoneField != "" {
		t.Fatalf("clear = %+v, %v", cleared, err)
	}
	if !bytes.Equal(readFile(t, fsys, "c.pulse"), orig) {
		t.Fatal("set then clear is not byte-identical to the original cohort")
	}
	if _, err := p.SetCohortZoneField(ctx, "c.pulse", ""); !isZoneOption(err) {
		t.Fatalf("empty field: %v, want SERVICE_VALIDATION option zone_field", err)
	}
	if _, err := p.SetCohortZoneField(ctx, "c.pulse", "region"); !errors.HasCode(err, errors.PULSE_TIMEZONE_UNKNOWN) {
		t.Fatalf("non-zone entries: %v, want PULSE_TIMEZONE_UNKNOWN", err)
	}
	if !bytes.Equal(readFile(t, fsys, "c.pulse"), orig) {
		t.Fatal("a refused set changed the cohort")
	}
}

// TestCohortZone_BuilderRefusesBadDefault: the builder judges a declared
// default up front with the import coding.
func TestCohortZone_BuilderRefusesBadDefault(t *testing.T) {
	t.Parallel()

	p, _ := memBuilderEngine(t)
	if _, err := p.NewCohortBuilder(context.Background(), "x.pulse", zoneSweepSchema("id"), CohortBuilderOptions{}); !isZoneOption(err) {
		t.Fatalf("non-categorical default: %v, want SERVICE_VALIDATION option zone_field", err)
	}
}

func isZoneOption(err error) bool {
	var ce *errors.CodedError
	return stderrors.As(err, &ce) && ce.Code == errors.SERVICE_VALIDATION && ce.Details["option"] == "zone_field"
}

// zoneLiteralExempt lists every production encoding.Schema composite
// literal that does NOT carry ZoneField, keyed "<path>:<func>", each
// with why it has no source default to carry. Every other literal must
// set the ZoneField key: a new rewriter that clones a schema without it
// fails TestCohortZone_SchemaLiteralsCarryZoneField.
var zoneLiteralExempt = map[string]string{
	"extensions_regression.go:probeRegressionSchema":     "extension probe schema, no cohort",
	"extensions_probe.go:probeAggregators":               "extension probe schema, no cohort",
	"extensions_probe.go:probeGroupers":                  "extension probe schema, no cohort",
	"extensions_probe.go:probeAttributes":                "extension probe schema, no cohort",
	"extensions_probe.go:probeTests":                     "extension probe schema, no cohort",
	"extensions_probe.go:probeFeatures":                  "extension probe schema, no cohort",
	"extensions_probe_dictionary.go:probeAttributeCodes": "extension probe schema, no cohort",
	"extensions_probe_matrix.go:probeMatrixSchema":       "extension probe schema, no cohort",
	"rake.go:rake": "empty schema for a refusal-only Compile",
	"internal/optfixture/optfixture.go:Schema":                       "fixture authoring a fresh cohort",
	"internal/mmmfixture/mmmfixture.go:Schema":                       "fixture authoring a fresh cohort",
	"internal/mmmfixture/joint.go:JointSchema":                       "fixture authoring a fresh cohort",
	"internal/tools/refcohorts/main.go:schemaFor":                    "reference-cohort tool authoring a fresh cohort",
	"internal/shardfixtures/shardfixtures.go:Build":                  "fixture authoring a fresh archive",
	"internal/io/import.go:Run":                                      "import buffer; the declared default is stamped by withZoneField",
	"internal/io/convert.go:offerConvertSource":                      "SPSS recipient row description, not a cohort writer",
	"internal/io/import_detect.go:measuredPass":                      "all-nullable constancy twin, never written",
	"internal/io/infer.go:InferSchemaWithOptions":                    "schema inferred from a tabular source (the default is the job's)",
	"internal/io/schema_file.go:SchemaFromFields":                    "a schema file declares no default (--zone-field does)",
	"internal/io/export_vectors.go:expandedSchema":                   "export column layout, not a cohort",
	"internal/io/arrow/arrow.go:InferPulseSchema":                    "schema inferred from an Arrow source",
	"internal/io/parquet/parquet.go:InferPulseSchema":                "schema inferred from a Parquet source",
	"internal/io/spss/mapping.go:schema":                             "schema inferred from an SPSS source",
	"internal/vectors/chain_columns.go:ChainStageSchema":             "chain stage >= 1 inherits no default (U75 Z6)",
	"internal/synth/writer.go:buildSchema":                           "plain synthesis from a spec, no source cohort",
	"internal/synth/fidelity.go:SyntheticAsCategoricalSchema":        "fidelity comparison view, never written",
	"internal/synth/regression_record.go:viewSchema":                 "regression record view, no source cohort",
	"internal/descriptor/materialize_predict.go:validateMaterialize": "description-quality check only",
	"encoding/schema.go:readFieldDescriptors":                        "the reader; the ZONE section is read after the descriptors",
}

// TestCohortZone_SchemaLiteralsCarryZoneField is the forward guard of the
// preservation sweep: every production `encoding.Schema{...}` composite
// literal (in package encoding, `Schema{...}`) sets the ZoneField key or
// is exempt by name with a reason. A rewriter added later that clones a
// schema field-by-field and forgets the default fails here.
func TestCohortZone_SchemaLiteralsCarryZoneField(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	var missing []string
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "node_modules", "embeddersmoke", "docs", ".planning", ".claude":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		inEncoding := f.Name.Name == "encoding" && filepath.Dir(path) == "encoding"
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok || !isSchemaType(lit.Type, inEncoding) {
					return true
				}
				for _, el := range lit.Elts {
					if kv, ok := el.(*ast.KeyValueExpr); ok {
						if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "ZoneField" {
							return true
						}
					}
				}
				key := filepath.ToSlash(path) + ":" + fn.Name.Name
				seen[key] = true
				if _, ok := zoneLiteralExempt[key]; !ok {
					missing = append(missing, key+" ("+fset.Position(lit.Pos()).String()+")")
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range missing {
		t.Errorf("encoding.Schema literal without ZoneField: %s — carry the source's default (ZoneField: src.ZoneField) or add it to zoneLiteralExempt with the reason it has none", m)
	}
	for key := range zoneLiteralExempt {
		if !seen[key] {
			t.Errorf("zoneLiteralExempt names %s, which holds no ZoneField-less schema literal any more — drop the entry", key)
		}
	}
}

func isSchemaType(e ast.Expr, inEncoding bool) bool {
	switch x := e.(type) {
	case *ast.SelectorExpr:
		id, ok := x.X.(*ast.Ident)
		return ok && id.Name == "encoding" && x.Sel.Name == "Schema"
	case *ast.Ident:
		return inEncoding && x.Name == "Schema"
	}
	return false
}
