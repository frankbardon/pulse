package pulse

import (
	"context"
	stderrors "errors"
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// The output slot's parent groups (types.MaterializeOutput.Groups):
// inherited from the source when absent, declared over written names
// when present, none when []. Every grouped output decodes exactly as
// its ungrouped twin.

// regionPriceGroup is the source group the tests build: key region,
// member price (constant per region, so the group survives the gate).
var regionPriceGroup = []pio.GroupDecl{{Key: []string{"region"}, Members: []string{"price"}}}

// renamedOutput writes id, region as area, spend, price as unit_price
// and the two derived columns, with groups as given.
func renamedOutput(groups []types.MaterializeGroup) *types.MaterializeOutput {
	return &types.MaterializeOutput{
		Columns: []types.MaterializeOutputColumn{
			{From: "id"}, {From: "region", As: "area"}, {From: "spend"}, {From: "price", As: "unit_price"},
			{From: "LOG_spend"}, {From: "rn"},
		},
		Groups: groups,
	}
}

// headerVersion is the format byte of the single-file cohort at path.
func headerVersion(t *testing.T, fsys afero.Fs, path string) byte {
	t.Helper()
	b, err := afero.ReadFile(fsys, path)
	if err != nil {
		t.Fatal(err)
	}
	return b[8]
}

// groupFieldNames lists each group of s as its member names, in order.
func groupFieldNames(s *encoding.Schema) [][]string {
	var out [][]string
	for _, g := range s.Groups {
		var names []string
		for _, m := range g.Members {
			names = append(names, s.Fields[m.Field].Name)
		}
		out = append(out, names)
	}
	return out
}

// matWarningCodes lists the codes of ws, in order.
func matWarningCodes(ws []*perrors.CodedError) []string {
	var out []string
	for _, w := range ws {
		out = append(out, string(w.Code))
	}
	return out
}

// TestMaterializeOutputGroups_AbsentOutputStaysUngrouped: a grouped
// source with no output block writes the 0x01 file the ungrouped source
// writes, byte for byte — inheritance needs an output block.
func TestMaterializeOutputGroups_AbsentOutputStaysUngrouped(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	// Two instances, one source name: a derived column's description
	// names its source cohort.
	pf, fsysA := matPulse(t, Options{})
	pg, fsysB := matPulse(t, Options{})
	buildMatSource(t, pf, "src.pulse", CohortBuilderOptions{})
	buildMatSource(t, pg, "src.pulse", CohortBuilderOptions{Groups: regionPriceGroup})
	if _, err := pf.Materialize(ctx, matOutputReq(nil), "out.pulse", MaterializeOptions{}); err != nil {
		t.Fatal(err)
	}
	res, err := pg.Materialize(ctx, matOutputReq(nil), "out.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Groups) != 0 {
		t.Errorf("Groups = %+v, want none without an output block", res.Groups)
	}
	a, _ := afero.ReadFile(fsysA, "out.pulse")
	b, _ := afero.ReadFile(fsysB, "out.pulse")
	if b[8] != encoding.FormatVersionV1 || string(a) != string(b) {
		t.Fatalf("grouped source without output wrote %d bytes (format %#x), want the flat source's %d bytes", len(b), b[8], len(a))
	}
}

// TestMaterializeOutputGroups_InheritWithRename: the source's group is
// declared again over its members' written names (region → area, price →
// unit_price, area still the key) — a 0x02 cohort whose rows equal the
// groups: [] twin's (a 0x01 file) row for row, the writer's verdict in
// MaterializeResult.Groups and no warning.
func TestMaterializeOutputGroups_InheritWithRename(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{Groups: regionPriceGroup})

	res, err := p.Materialize(ctx, matOutputReq(renamedOutput(nil)), "grouped.pulse", MaterializeOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", matWarningCodes(res.Warnings))
	}
	if len(res.Groups) != 1 || res.Groups[0].Verdict != encx.GroupVerdictAdmitted ||
		!slices.Equal(res.Groups[0].Key, []string{"area"}) || !slices.Equal(res.Groups[0].Fields, []string{"area", "unit_price"}) {
		t.Fatalf("Groups = %+v, want group 1 [key: area] over area, unit_price, admitted", res.Groups)
	}
	if v := headerVersion(t, fsys, "grouped.pulse"); v != encoding.FormatVersionV2 {
		t.Fatalf("grouped output format %#x, want 0x02", v)
	}
	gs, grows := readAll(t, p, "grouped.pulse")
	if got := groupFieldNames(gs); !reflect.DeepEqual(got, [][]string{{"area", "unit_price"}}) {
		t.Fatalf("written groups = %v, want [[area unit_price]]", got)
	}
	if m := gs.Groups[0].Members; !m[0].Key || m[1].Key {
		t.Errorf("written group members = %+v, want area the key and unit_price determined", m)
	}

	twin, err := p.Materialize(ctx, matOutputReq(renamedOutput([]types.MaterializeGroup{})), "flat.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(twin.Groups) != 0 || len(twin.Warnings) != 0 {
		t.Errorf("groups: [] result = %+v, want no group and no warning", twin)
	}
	if v := headerVersion(t, fsys, "flat.pulse"); v != encoding.FormatVersionV1 {
		t.Fatalf("groups: [] output format %#x, want 0x01", v)
	}
	fs, frows := readAll(t, p, "flat.pulse")
	if fs.HasGroups() || !slices.Equal(fieldNames(fs), fieldNames(gs)) {
		t.Fatalf("twin fields %v (grouped %t), want %v ungrouped", fieldNames(fs), fs.HasGroups(), fieldNames(gs))
	}
	if !reflect.DeepEqual(grows, frows) {
		t.Fatalf("grouped rows differ from the ungrouped twin\n got %v\nwant %v", grows, frows)
	}
}

// TestMaterializeOutputGroups_ChangedMemberNotInherited: a source group
// whose member is dropped, retyped, recoded or bucketed is left out —
// a 0x01 output, one PULSE_MATERIALIZE_GROUP_NOT_INHERITED warning
// naming the member and the change, predict reporting the same verdict
// and warning — and Strict fails it before anything is written.
func TestMaterializeOutputGroups_ChangedMemberNotInherited(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{Groups: regionPriceGroup})
	north := "N"
	for _, tc := range []struct {
		name, member, change string
		out                  *types.MaterializeOutput
	}{
		{"dropped", "price", "dropped", &types.MaterializeOutput{Exclude: []string{"price"}}},
		{"retyped", "price", "retyped", &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
			{From: "region"}, {From: "price", Type: "f64"}}}},
		{"recoded", "region", "recoded", &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
			{From: "region", Recode: &types.MaterializeRecode{Map: map[string]*string{"north": &north}}}, {From: "price"}}}},
		{"bucketed", "price", "bucketed", &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
			{From: "region"}, {From: "price", Bucket: &types.MaterializeBucket{Ranges: []types.MaterializeBucketRange{
				{Label: "low", End: []byte("100")}, {Label: "high", Start: []byte("100")}}}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := tc.name + ".pulse"
			res, err := p.Materialize(ctx, matOutputReq(tc.out), out, MaterializeOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Groups) != 0 {
				t.Errorf("Groups = %+v, want none", res.Groups)
			}
			if v := headerVersion(t, fsys, out); v != encoding.FormatVersionV1 {
				t.Errorf("format %#x, want 0x01", v)
			}
			var w *perrors.CodedError
			for _, x := range res.Warnings {
				if x.Code == perrors.PULSE_MATERIALIZE_GROUP_NOT_INHERITED {
					w = x
				}
			}
			if w == nil || w.Details["member"] != tc.member || w.Details["change"] != tc.change ||
				!reflect.DeepEqual(w.Details["fields"], []string{"region", "price"}) {
				t.Fatalf("warnings = %v, want PULSE_MATERIALIZE_GROUP_NOT_INHERITED for %s %s", res.Warnings, tc.member, tc.change)
			}

			env, err := p.PredictMaterialize(ctx, matOutputReq(tc.out))
			if err != nil {
				t.Fatal(err)
			}
			pr := env.Data.(*descriptor.MaterializePredictResult)
			if pr.Container == nil || len(pr.Container.Groups) != 1 {
				t.Fatalf("predict container = %+v, want one group", pr.Container)
			}
			g := pr.Container.Groups[0]
			if g.Origin != "inherited" || g.Verdict != "not_inherited" || g.Member != tc.member || g.Change != tc.change ||
				g.Reason != string(perrors.PULSE_MATERIALIZE_GROUP_NOT_INHERITED) || g.Label != w.Details["group"] {
				t.Errorf("predict group = %+v, want the run's not_inherited verdict (%v)", g, w.Details)
			}
			var pw []string
			for _, x := range env.Warnings {
				pw = append(pw, x.Code)
			}
			if !slices.Equal(pw, matWarningCodes(res.Warnings)) {
				t.Errorf("predict warnings %v, run warnings %v", pw, matWarningCodes(res.Warnings))
			}

			strictOut := tc.name + "-strict.pulse"
			_, err = p.Materialize(ctx, matOutputReq(tc.out), strictOut, MaterializeOptions{Strict: true})
			if !perrors.HasCode(err, perrors.PULSE_MATERIALIZE_GROUP_NOT_INHERITED) {
				t.Fatalf("Strict err = %v, want PULSE_MATERIALIZE_GROUP_NOT_INHERITED", err)
			}
			if ok, _ := afero.Exists(fsys, strictOut); ok {
				t.Errorf("Strict refusal left %s behind", strictOut)
			}
		})
	}

	// groups: [] opts out of inheritance: no group, no warning.
	res, err := p.Materialize(ctx, matOutputReq(&types.MaterializeOutput{Exclude: []string{"price"}, Groups: []types.MaterializeGroup{}}),
		"optout.pulse", MaterializeOptions{Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 || len(res.Groups) != 0 || headerVersion(t, fsys, "optout.pulse") != encoding.FormatVersionV1 {
		t.Errorf("groups: [] = %+v, want an ungrouped 0x01 cohort and no warning", res)
	}
}

// TestMaterializeOutputGroups_Declared: output.groups declares groups over
// written names — a group the gate admits is written (0x02, rows equal
// to the ungrouped twin), a group no wider than its index is dropped
// with PULSE_GROUP_TOO_NARROW (fatal under Strict, nothing written),
// and the declaration replaces inheritance.
func TestMaterializeOutputGroups_Declared(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	// id is a u32: no wider than the 4-byte index, so never formed.
	out := renamedOutput([]types.MaterializeGroup{
		{Key: []string{"area"}, Members: []string{"unit_price"}},
		{Members: []string{"id"}},
	})
	res, err := p.Materialize(ctx, matOutputReq(out), "decl.pulse", MaterializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Groups) != 2 || res.Groups[0].Verdict != encx.GroupVerdictAdmitted || res.Groups[1].Verdict != encx.GroupVerdictDroppedTooNarrow {
		t.Fatalf("Groups = %+v, want admitted then dropped_too_narrow", res.Groups)
	}
	if !slices.Equal(matWarningCodes(res.Warnings), []string{string(perrors.PULSE_GROUP_TOO_NARROW)}) {
		t.Errorf("warnings = %v, want one PULSE_GROUP_TOO_NARROW", matWarningCodes(res.Warnings))
	}
	gs, grows := readAll(t, p, "decl.pulse")
	if got := groupFieldNames(gs); !reflect.DeepEqual(got, [][]string{{"area", "unit_price"}}) {
		t.Fatalf("written groups = %v, want [[area unit_price]]", got)
	}
	if _, err := p.Materialize(ctx, matOutputReq(renamedOutput([]types.MaterializeGroup{})), "flat.pulse", MaterializeOptions{}); err != nil {
		t.Fatal(err)
	}
	_, frows := readAll(t, p, "flat.pulse")
	if !reflect.DeepEqual(grows, frows) {
		t.Fatalf("declared-group rows differ from the ungrouped twin")
	}

	// Predict: the same labels and verdicts, the narrow group's warning.
	env, err := p.PredictMaterialize(ctx, matOutputReq(out))
	if err != nil {
		t.Fatal(err)
	}
	pr := env.Data.(*descriptor.MaterializePredictResult)
	if len(pr.Container.Groups) != 2 {
		t.Fatalf("predict groups = %+v, want 2", pr.Container.Groups)
	}
	for i, g := range pr.Container.Groups {
		want := "planned"
		if res.Groups[i].Verdict == encx.GroupVerdictDroppedTooNarrow {
			want = encx.GroupVerdictDroppedTooNarrow
		}
		if g.Origin != "declared" || g.Label != res.Groups[i].Label || g.Verdict != want {
			t.Errorf("predict group %d = %+v, want %s %s", i, g, res.Groups[i].Label, want)
		}
	}
	if len(env.Warnings) != 1 || env.Warnings[0].Code != string(perrors.PULSE_GROUP_TOO_NARROW) {
		t.Errorf("predict warnings = %+v, want one PULSE_GROUP_TOO_NARROW", env.Warnings)
	}
	// The keyed declared group carries the key check as a risk.
	var keyRisk bool
	for _, r := range pr.Risks {
		keyRisk = keyRisk || (r.Code == string(perrors.PULSE_GROUP_MEMBER_NOT_CONSTANT) && r.Path == "output.groups[0]")
	}
	if !keyRisk {
		t.Errorf("risks = %+v, want the key check on output.groups[0]", pr.Risks)
	}

	_, err = p.Materialize(ctx, matOutputReq(out), "strict.pulse", MaterializeOptions{Strict: true})
	if !perrors.HasCode(err, perrors.PULSE_GROUP_TOO_NARROW) {
		t.Fatalf("Strict err = %v, want PULSE_GROUP_TOO_NARROW", err)
	}
	if ok, _ := afero.Exists(fsys, "strict.pulse"); ok {
		t.Error("Strict refusal left strict.pulse behind")
	}
}

// TestMaterializeOutputGroups_DeclarationRefusedPredictEqualsRun: a
// declaration the writer refuses — an unknown (or pre-rename) name, a
// field in two groups, a field named twice in one group, an empty group — is
// refused before anything is written with the writer's code, and
// predict refuses it with the same code and details (path included).
func TestMaterializeOutputGroups_DeclarationRefusedPredictEqualsRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, fsys := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{})
	for _, tc := range []struct {
		name   string
		groups []types.MaterializeGroup
		code   perrors.Code
		path   string
	}{
		{"unknown", []types.MaterializeGroup{{Key: []string{"area"}, Members: []string{"nope"}}}, perrors.PULSE_GROUP_FIELD_UNKNOWN, "output.groups[0]"},
		{"source_name", []types.MaterializeGroup{{Key: []string{"region"}, Members: []string{"unit_price"}}}, perrors.PULSE_GROUP_FIELD_UNKNOWN, "output.groups[0]"},
		{"conflict", []types.MaterializeGroup{{Members: []string{"area", "unit_price"}}, {Members: []string{"unit_price", "spend"}}}, perrors.PULSE_GROUP_FIELD_CONFLICT, "output.groups[1]"},
		{"named_twice", []types.MaterializeGroup{{Key: []string{"area"}, Members: []string{"unit_price"}}, {Key: []string{"spend"}, Members: []string{"spend"}}}, perrors.PULSE_GROUP_DECLARATION_INVALID, "output.groups[1]"},
		{"empty", []types.MaterializeGroup{{}}, perrors.PULSE_GROUP_DECLARATION_INVALID, "output.groups[0]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := matOutputReq(renamedOutput(tc.groups))
			out := tc.name + ".pulse"
			_, err := p.Materialize(ctx, req, out, MaterializeOptions{})
			var ce *perrors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != tc.code || ce.Details["path"] != tc.path {
				t.Fatalf("run err = %v, want %s at %s", err, tc.code, tc.path)
			}
			if ok, _ := afero.Exists(fsys, out); ok {
				t.Errorf("refusal left %s behind", out)
			}
			env, err := p.PredictMaterialize(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) != 1 || env.Errors[0].Code != string(tc.code) || !reflect.DeepEqual(env.Errors[0].Details, ce.Details) {
				t.Fatalf("predict errors = %+v, want the run's %s %v", env.Errors, ce.Code, ce.Details)
			}
		})
	}
}

// TestMaterializeOutputGroups_WithShards: groups and shards together —
// an inherited group over a shard archive output and a declared one with
// elision too — are decided once over every row: the archive is grouped,
// cut at max_records, and reads back as the ungrouped single file does.
func TestMaterializeOutputGroups_WithShards(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, _ := matPulse(t, Options{})
	buildMatSource(t, p, "src.pulse", CohortBuilderOptions{Groups: regionPriceGroup})
	if _, err := p.Materialize(ctx, matOutputReq(renamedOutput([]types.MaterializeGroup{})), "flat.pulse", MaterializeOptions{}); err != nil {
		t.Fatal(err)
	}
	_, flatRows := readAll(t, p, "flat.pulse")

	inherit := renamedOutput(nil)
	inherit.Shards = &types.MaterializeShards{MaxRecords: 5}
	declared := renamedOutput([]types.MaterializeGroup{{Key: []string{"area"}, Members: []string{"unit_price"}}})
	declared.Shards = &types.MaterializeShards{MaxRecords: 4}
	declared.ElideConstants = true
	for name, out := range map[string]*types.MaterializeOutput{"inherited": inherit, "declared": declared} {
		target := name + ".pulse"
		res, err := p.Materialize(ctx, matOutputReq(out), target, MaterializeOptions{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(res.Groups) != 1 || res.Groups[0].Verdict != encx.GroupVerdictAdmitted {
			t.Errorf("%s: Groups = %+v, want one admitted group", name, res.Groups)
		}
		want := (12 + out.Shards.MaxRecords - 1) / out.Shards.MaxRecords
		if len(res.Shards) != want {
			t.Errorf("%s: %d shards, want %d", name, len(res.Shards), want)
		}
		schema, rows := readAll(t, p, target)
		if got := groupFieldNames(schema); !reflect.DeepEqual(got, [][]string{{"area", "unit_price"}}) {
			t.Errorf("%s: archive groups = %v, want [[area unit_price]]", name, got)
		}
		if !reflect.DeepEqual(rows, flatRows) {
			t.Errorf("%s: archive rows differ from the ungrouped single file", name)
		}
	}
}
