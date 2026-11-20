package descriptor

import (
	"bytes"
	"reflect"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
)

func unionDict(t *testing.T, vals ...string) *encoding.Dictionary {
	t.Helper()
	d := encoding.NewDictionary()
	for _, v := range vals {
		if _, err := d.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

// unionSchema is a laid-out schema: amount f64, region categorical_u8
// over regions, tags set_u8 over tags.
func unionSchema(t *testing.T, regions, tags []string) *encoding.Schema {
	t.Helper()
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "amount", Type: encoding.FieldTypeF64, Description: "Order amount in dollars"},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Description: "Sales region of the order", Dictionary: unionDict(t, regions...)},
		{Name: "tags", Type: encoding.FieldTypeSetU8, Description: "Tags applied to the order", Dictionary: unionDict(t, tags...)},
	}}
	encx.LayoutFields(s)
	return s
}

// unionCohortBytes is a single-file cohort: schema (any format version)
// and n zero-filled records.
func unionCohortBytes(t *testing.T, s *encoding.Schema, n int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := encx.WritePreamble(&buf, s); err != nil {
		t.Fatalf("WritePreamble: %v", err)
	}
	buf.Write(make([]byte, n*s.RecordByteSize()))
	return buf.Bytes()
}

func unionCodeOf(err error) errors.Code {
	if ce, ok := err.(*errors.CodedError); ok {
		return ce.Code
	}
	return ""
}

func TestUnionShapeRefusal(t *testing.T) {
	t.Parallel()
	member := func(file, label string) types.UnionMember {
		return types.UnionMember{Cohort: &types.Cohort{Filename: file}, Label: label}
	}
	tooMany := make([]types.UnionMember, encx.MaxArchiveShards+1)
	for i := range tooMany {
		tooMany[i] = member("m.pulse", "m"+strconv.Itoa(i))
	}
	cases := []struct {
		name   string
		req    *types.Request
		code   errors.Code
		reason string
	}{
		{"absent", &types.Request{Cohort: &types.Cohort{Filename: "a.pulse"}}, "", ""},
		{"valid", &types.Request{Union: []types.UnionMember{member("a.pulse", "a"), member("b.pulse", "b")}}, "", ""},
		{"source field without union", &types.Request{Cohort: &types.Cohort{Filename: "a.pulse"}, UnionSourceField: "src"},
			errors.PULSE_UNION_SOURCE_FIELD_INVALID, "without_union"},
		{"cohort and union", &types.Request{Cohort: &types.Cohort{Filename: "a.pulse"}, Union: []types.UnionMember{member("b.pulse", "b")}},
			errors.PULSE_UNION_COHORT_CONFLICT, ""},
		{"member without cohort", &types.Request{Union: []types.UnionMember{member("a.pulse", "a"), {Label: "b"}}},
			errors.PULSE_UNION_MEMBER_INVALID, ""},
		{"member with empty filename", &types.Request{Union: []types.UnionMember{member("", "a")}},
			errors.PULSE_UNION_MEMBER_INVALID, ""},
		{"empty label", &types.Request{Union: []types.UnionMember{member("a.pulse", "")}},
			errors.PULSE_UNION_LABEL_INVALID, "empty"},
		{"duplicate label", &types.Request{Union: []types.UnionMember{member("a.pulse", "x"), member("b.pulse", "y"), member("c.pulse", "x")}},
			errors.PULSE_UNION_LABEL_INVALID, "duplicate"},
		{"too many members", &types.Request{Union: tooMany}, errors.PULSE_UNION_MEMBER_LIMIT, ""},
	}
	for _, tc := range cases {
		err := UnionShapeRefusal(tc.req)
		if got := unionCodeOf(err); got != tc.code || (tc.code == "") != (err == nil) {
			t.Errorf("%s: code = %q (%v), want %q", tc.name, got, err, tc.code)
			continue
		}
		if tc.reason != "" {
			if r := err.(*errors.CodedError).Details["reason"]; r != tc.reason {
				t.Errorf("%s: reason = %v, want %s", tc.name, r, tc.reason)
			}
		}
	}
	dup := UnionShapeRefusal(cases[7].req).(*errors.CodedError)
	if dup.Details["member"] != 2 || dup.Details["first"] != 0 {
		t.Errorf("duplicate details = %v, want member 2, first 0", dup.Details)
	}
}

// TestMergeUnion_MirrorsArchiveRefusals: every schema pair a shard
// archive refuses, a union refuses as PULSE_UNION_SCHEMA_MISMATCH
// carrying the archive refusal's own code and details
// (details.dimension included).
func TestMergeUnion_MirrorsArchiveRefusals(t *testing.T) {
	t.Parallel()
	base := func() *encoding.Schema { return unionSchema(t, []string{"n", "s"}, []string{"a"}) }
	typeDiff := base()
	typeDiff.Fields[0].Type = encoding.FieldTypeF32
	encx.LayoutFields(typeDiff)
	nullable := base()
	nullable.Fields[0].Nullable = true
	zoned := func(zone string) *encoding.Schema {
		s := base()
		s.Fields = append(s.Fields, encoding.Field{Name: "tz", Type: encoding.FieldTypeCategoricalU8, Description: "IANA zone of the order", Dictionary: unionDict(t, "UTC")})
		encx.LayoutFields(s)
		s.ZoneField = zone
		return s
	}
	extra := base()
	extra.Fields = append(extra.Fields, encoding.Field{Name: "x", Type: encoding.FieldTypeU8, Description: "Extra unsigned column"})
	encx.LayoutFields(extra)
	many := func(prefix string) *encoding.Schema {
		vals := make([]string, 200)
		for i := range vals {
			vals[i] = prefix + strconv.Itoa(i)
		}
		return unionSchema(t, vals, []string{"a"})
	}
	cases := []struct {
		name      string
		a, b      *encoding.Schema
		archive   errors.Code
		dimension string
	}{
		{"type", base(), typeDiff, errors.PULSE_SHARD_SCHEMA_MISMATCH, ""},
		{"nullability", base(), nullable, errors.PULSE_SHARD_SCHEMA_MISMATCH, "nullable"},
		{"zone field", zoned(""), zoned("tz"), errors.PULSE_SHARD_SCHEMA_MISMATCH, "zone_field"},
		{"field count", base(), extra, errors.PULSE_SHARD_SCHEMA_MISMATCH, ""},
		{"categorical width", many("p"), many("q"), errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW, ""},
	}
	for _, tc := range cases {
		_, _, archiveErr := encx.MergeUnionSchema(tc.a, tc.b)
		if unionCodeOf(archiveErr) != tc.archive {
			t.Fatalf("%s: archive merge = %v, want %s", tc.name, archiveErr, tc.archive)
		}
		_, _, err := MergeUnion([]UnionMemberSchema{{Label: "src", Schema: tc.a}, {Label: "twin", Schema: tc.b}}, "_source")
		ce, ok := err.(*errors.CodedError)
		if !ok || ce.Code != errors.PULSE_UNION_SCHEMA_MISMATCH {
			t.Fatalf("%s: MergeUnion = %v, want PULSE_UNION_SCHEMA_MISMATCH", tc.name, err)
		}
		if ce.Details["archive_code"] != string(tc.archive) || ce.Details["member"] != 1 || ce.Details["label"] != "twin" {
			t.Errorf("%s: details = %v", tc.name, ce.Details)
		}
		for k, v := range archiveErr.(*errors.CodedError).Details {
			if !reflect.DeepEqual(ce.Details[k], v) {
				t.Errorf("%s: details[%s] = %v, archive %v", tc.name, k, ce.Details[k], v)
			}
		}
		if tc.dimension != "" && ce.Details["dimension"] != tc.dimension {
			t.Errorf("%s: dimension = %v, want %s", tc.name, ce.Details["dimension"], tc.dimension)
		}
	}
}

// The shard-archive merge's own verdict on the same pairs: the union
// merge refuses exactly what ValidateStructuralCohesion refuses on the
// structural rules.
func TestMergeUnionSchema_StructuralVerdictIsTheArchives(t *testing.T) {
	t.Parallel()
	a := unionSchema(t, []string{"n"}, []string{"a"})
	b := unionSchema(t, []string{"n"}, []string{"a"})
	b.Fields[1].Name = "area"
	_, wantErr := encx.ValidateStructuralCohesion(a, b)
	_, _, gotErr := encx.MergeUnionSchema(a, b)
	if wantErr == nil || gotErr == nil || gotErr.Error() != wantErr.Error() {
		t.Fatalf("union merge = %v, archive cohesion = %v", gotErr, wantErr)
	}
}

func TestMergeUnion_WidensSetsUnionsDictsAddsSource(t *testing.T) {
	t.Parallel()
	tagsA := []string{"a0", "a1", "a2", "a3", "a4", "a5"}
	tagsB := []string{"b0", "b1", "b2", "b3", "b4", "b5"}
	a := unionSchema(t, []string{"n", "s"}, tagsA)
	b := unionSchema(t, []string{"s", "e"}, tagsB)
	b.Fields[0].Description = "Order amount, twin copy"
	aType, bDict := a.Fields[2].Type, b.Fields[1].Dictionary.Values()
	merged, warnings, err := MergeUnion([]UnionMemberSchema{{Label: "src", Schema: a}, {Label: "twin", Schema: b}}, "_source")
	if err != nil {
		t.Fatalf("MergeUnion: %v", err)
	}
	if got := merged.Fields[1].Dictionary.Values(); !reflect.DeepEqual(got, []string{"n", "s", "e"}) {
		t.Errorf("region dictionary = %v, want canonical-first union", got)
	}
	if merged.Fields[2].Type != encoding.FieldTypeSetU16 || merged.Fields[2].Dictionary.Count() != 12 {
		t.Errorf("tags = %s with %d entries, want set_u16 with 12", merged.Fields[2].Type, merged.Fields[2].Dictionary.Count())
	}
	src := merged.Fields[len(merged.Fields)-1]
	if src.Name != "_source" || src.Type != encoding.FieldTypeCategoricalU8 || src.Nullable ||
		!reflect.DeepEqual(src.Dictionary.Values(), []string{"src", "twin"}) {
		t.Errorf("source field = %+v (%v)", src, src.Dictionary.Values())
	}
	if want := merged.Fields[2].ByteOffset + merged.FieldWidth(2); src.ByteOffset != want {
		t.Errorf("source offset = %d, want %d", src.ByteOffset, want)
	}
	if len(warnings) != 1 || warnings[0].Code != string(errors.PULSE_SHARD_DESCRIPTION_DIVERGENCE) || warnings[0].Details["member"] != 1 {
		t.Errorf("warnings = %+v, want one description divergence on member 1", warnings)
	}
	if a.Fields[2].Type != aType || len(a.Fields) != 3 || !reflect.DeepEqual(b.Fields[1].Dictionary.Values(), bDict) {
		t.Error("a member schema was modified")
	}
}

func TestMergeUnion_SourceFieldRules(t *testing.T) {
	t.Parallel()
	s := unionSchema(t, []string{"n"}, []string{"a"})
	_, _, err := MergeUnion([]UnionMemberSchema{{Label: "a", Schema: s}, {Label: "b", Schema: s}}, "region")
	if ce, ok := err.(*errors.CodedError); !ok || ce.Code != errors.PULSE_UNION_SOURCE_FIELD_INVALID || ce.Details["reason"] != "collision" {
		t.Fatalf("collision = %v, want PULSE_UNION_SOURCE_FIELD_INVALID collision", err)
	}
	members := make([]UnionMemberSchema, 300)
	for i := range members {
		members[i] = UnionMemberSchema{Label: "m" + strconv.Itoa(i), Schema: s}
	}
	merged, _, err := MergeUnion(members, "wave")
	if err != nil {
		t.Fatal(err)
	}
	if f := merged.Fields[len(merged.Fields)-1]; f.Name != "wave" || f.Type != encoding.FieldTypeCategoricalU16 || f.Dictionary.Count() != 300 {
		t.Errorf("source field = %s %s (%d labels), want wave categorical_u16 (300)", f.Name, f.Type, f.Dictionary.Count())
	}
	_, _, err = MergeUnion([]UnionMemberSchema{{Label: "a", Schema: s, Shards: encx.MaxArchiveShards}, {Label: "b", Schema: s, Shards: 1}}, "_source")
	if ce, ok := err.(*errors.CodedError); !ok || ce.Code != errors.PULSE_UNION_MEMBER_LIMIT || ce.Details["shard_count"] != encx.MaxArchiveShards+1 {
		t.Fatalf("flattened shard ceiling = %v, want PULSE_UNION_MEMBER_LIMIT shard_count %d", err, encx.MaxArchiveShards+1)
	}
}

// TestPredictUnion_Figures: the union is predicted over the merged
// schema (source field included) with the members' summed record
// count, and its verdicts equal predicting the same request over ONE
// cohort carrying that merged schema and that many records.
func TestPredictUnion_Figures(t *testing.T) {
	t.Parallel()
	a := unionSchema(t, []string{"n", "s"}, []string{"a"})
	b := unionSchema(t, []string{"s", "e"}, []string{"b"})
	shards := []struct {
		Name    string
		NRecord int
	}{{"w1.pulse", 2}, {"w2.pulse", 4}}
	arch := buildShardArchiveBytes(t, b, shards)
	req := &types.Request{
		Union: []types.UnionMember{
			{Cohort: &types.Cohort{Filename: "a.pulse"}, Label: "source"},
			{Cohort: &types.Cohort{Filename: "b.pulse"}, Label: "twin"},
			{Cohort: &types.Cohort{Filename: "arch.pulse"}, Label: "waves"},
		},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "_source"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "amount", Label: "total"}},
	}
	members := []UnionSource{
		{Label: "source", Cohort: req.Union[0].Cohort, R: bytes.NewReader(unionCohortBytes(t, a, 3))},
		{Label: "twin", Cohort: req.Union[1].Cohort, R: bytes.NewReader(unionCohortBytes(t, b, 5))},
		{Label: "waves", Cohort: req.Union[2].Cohort, R: bytes.NewReader(arch)},
	}
	env := PredictUnion(members, req, &PredictOptions{})
	res := env.Data.(*descriptor.PredictResult)
	if len(env.Errors) > 0 || !res.Valid {
		t.Fatalf("union predict refused: %+v", env.Errors)
	}
	for _, w := range env.Warnings {
		if w.Details["field"] == "_source" {
			t.Errorf("the source field draws a warning: %+v", w)
		}
	}
	if res.RecordCount != 14 {
		t.Errorf("RecordCount = %d, want 14", res.RecordCount)
	}
	wantMembers := []descriptor.UnionMemberPredict{
		{Label: "source", Cohort: "a.pulse", RecordCount: 3, Shards: 1},
		{Label: "twin", Cohort: "b.pulse", RecordCount: 5, Shards: 1},
		{Label: "waves", Cohort: "arch.pulse", RecordCount: 6, Shards: 2},
	}
	if !reflect.DeepEqual(res.Union, wantMembers) {
		t.Errorf("Union = %+v, want %+v", res.Union, wantMembers)
	}
	if want := []string{"amount", "region", "tags", "_source"}; !reflect.DeepEqual(res.SchemaInfo.Fields, want) {
		t.Errorf("schema fields = %v, want %v", res.SchemaInfo.Fields, want)
	}

	// The oracle: one cohort with the merged schema and 14 records.
	merged, _, err := MergeUnion([]UnionMemberSchema{{Label: "source", Schema: a}, {Label: "twin", Schema: b}, {Label: "waves", Schema: b, Shards: 2}}, "_source")
	if err != nil {
		t.Fatal(err)
	}
	single := *req
	single.Union, single.Cohort = nil, &types.Cohort{Filename: "merged.pulse"}
	oracle := Predict(bytes.NewReader(unionCohortBytes(t, merged, 14)), &single, &PredictOptions{}).Data.(*descriptor.PredictResult)
	if res.Streamable != oracle.Streamable || !reflect.DeepEqual(res.StreamableReasons, oracle.StreamableReasons) {
		t.Errorf("streamable = %v %v, oracle %v %v", res.Streamable, res.StreamableReasons, oracle.Streamable, oracle.StreamableReasons)
	}
	if !reflect.DeepEqual(res.Sizes, oracle.Sizes) {
		t.Errorf("sizes = %+v, oracle %+v", res.Sizes, oracle.Sizes)
	}
	if !reflect.DeepEqual(res.Groups, oracle.Groups) {
		t.Errorf("groups = %+v, oracle %+v", res.Groups, oracle.Groups)
	}

	// A field no member carries is still refused over the merged schema.
	bad := *req
	bad.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "_origin"}}
	for i := range members {
		members[i].R.(*bytes.Reader).Seek(0, 0) //nolint:errcheck // a bytes.Reader seek to 0 cannot fail
	}
	if env := PredictUnion(members, &bad, &PredictOptions{}); len(env.Errors) == 0 {
		t.Error("an unknown group field over a union was not refused")
	}
}

func TestPredictUnion_Refusals(t *testing.T) {
	t.Parallel()
	a := unionSchema(t, []string{"n"}, []string{"a"})
	nullable := unionSchema(t, []string{"n"}, []string{"a"})
	nullable.Fields[0].Nullable = true
	src := func(label string, s *encoding.Schema) UnionSource {
		return UnionSource{Label: label, Cohort: &types.Cohort{Filename: label + ".pulse"}, R: bytes.NewReader(unionCohortBytes(t, s, 1))}
	}
	req := func(labels ...string) *types.Request {
		r := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Label: "n"}}}
		for _, l := range labels {
			r.Union = append(r.Union, types.UnionMember{Cohort: &types.Cohort{Filename: l + ".pulse"}, Label: l})
		}
		return r
	}
	codeIn := func(env *descriptor.Envelope) string {
		if len(env.Errors) == 0 {
			return ""
		}
		return env.Errors[0].Code
	}
	if got := codeIn(PredictUnion([]UnionSource{src("a", a), src("b", nullable)}, req("a", "b"), nil)); got != string(errors.PULSE_UNION_SCHEMA_MISMATCH) {
		t.Errorf("nullability mismatch = %q", got)
	}
	dup := req("a", "a")
	if got := codeIn(PredictUnion(nil, dup, nil)); got != string(errors.PULSE_UNION_LABEL_INVALID) {
		t.Errorf("duplicate label = %q", got)
	}
	collide := req("a", "b")
	collide.UnionSourceField = "amount"
	if got := codeIn(PredictUnion([]UnionSource{src("a", a), src("b", a)}, collide, nil)); got != string(errors.PULSE_UNION_SOURCE_FIELD_INVALID) {
		t.Errorf("collision = %q", got)
	}
	broken := UnionSource{Label: "b", Cohort: &types.Cohort{Filename: "b.pulse"}, R: bytes.NewReader([]byte("not a cohort"))}
	env := PredictUnion([]UnionSource{src("a", a), broken}, req("a", "b"), nil)
	if len(env.Errors) == 0 || env.Errors[0].Details["member"] != 1 || env.Errors[0].Details["label"] != "b" {
		t.Errorf("unreadable member = %+v, want an error located at member 1", env.Errors)
	}

	// Predict over one source never reads a union as that cohort.
	single := unionCohortBytes(t, a, 1)
	if got := codeIn(Predict(bytes.NewReader(single), req("a", "b"), &PredictOptions{})); got != string(errors.PULSE_UNION_UNSUPPORTED) {
		t.Errorf("Predict(single source, union) = %q, want PULSE_UNION_UNSUPPORTED", got)
	}
	both := req("b")
	both.Cohort = &types.Cohort{Filename: "a.pulse"}
	if got := codeIn(Predict(bytes.NewReader(single), both, &PredictOptions{})); got != string(errors.PULSE_UNION_COHORT_CONFLICT) {
		t.Errorf("Predict(cohort+union) = %q, want PULSE_UNION_COHORT_CONFLICT", got)
	}
}
