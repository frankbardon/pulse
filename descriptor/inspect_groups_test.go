package descriptor

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// groupedInspectFixture builds a synthetic 0x02 cohort of `rows` rows:
// a parent block (order_id key, region categorical, order_total) that
// changes every `fanout` rows, a per-row qty, and a tenant column that
// never changes. The parent block goes in an indexed group keyed on
// order_id and tenant in a constant group. It returns the grouped bytes
// and the schema DedupCohort wrote.
func groupedInspectFixture(t *testing.T, rows, fanout int, regions ...string) ([]byte, *encoding.Schema) {
	t.Helper()
	if len(regions) == 0 {
		regions = []string{"north", "south", "east", "west"}
	}
	flat := &encoding.Schema{Fields: []encoding.Field{
		{Name: "order_id", Type: encoding.FieldTypeU32, Description: "Synthetic parent key"},
		{Name: "region", ByteOffset: 4, Type: encoding.FieldTypeCategoricalU8, Description: "Synthetic parent region", Dictionary: makeDictionary(t, regions...)},
		{Name: "order_total", ByteOffset: 5, Type: encoding.FieldTypeU64, Description: "Synthetic parent total"},
		{Name: "qty", ByteOffset: 13, Type: encoding.FieldTypeU16, Description: "Synthetic per-row quantity"},
		{Name: "tenant", ByteOffset: 15, Type: encoding.FieldTypeU8, Description: "Synthetic constant tenant"},
	}}
	var buf bytes.Buffer
	if err := encoding.WritePreamble(&buf, flat); err != nil {
		t.Fatalf("WritePreamble: %v", err)
	}
	for r := 0; r < rows; r++ {
		p := uint64(r / fanout)
		vals := []uint64{p, p % uint64(len(regions)), p * 100, uint64(r), 7}
		for i, f := range flat.Fields {
			if err := encoding.WriteFieldValue(&buf, f.Type, vals[i]); err != nil {
				t.Fatalf("WriteFieldValue(%s): %v", f.Name, err)
			}
		}
	}
	var out bytes.Buffer
	schema, n, err := encoding.DedupCohort(&out, bytes.NewReader(buf.Bytes()), []encoding.GroupSpec{
		{Kind: encoding.GroupKindIndexed, Members: []string{"order_id", "region", "order_total"}, Key: []string{"order_id"}},
		{Kind: encoding.GroupKindConstant, Members: []string{"tenant"}},
	})
	if err != nil {
		t.Fatalf("DedupCohort: %v", err)
	}
	if n != int64(rows) {
		t.Fatalf("DedupCohort wrote %d rows, want %d", n, rows)
	}
	return out.Bytes(), schema
}

func inspectGrouped(t *testing.T, data []byte, opts *InspectOptions) *InspectResult {
	t.Helper()
	env := InspectFromBytes(data, opts)
	if len(env.Errors) != 0 {
		t.Fatalf("inspect errors: %+v", env.Errors)
	}
	if len(env.Warnings) != 0 {
		t.Fatalf("inspect warnings on a clean grouped cohort: %+v", env.Warnings)
	}
	return env.Data.(*InspectResult)
}

// TestInspect_GroupFigures pins every per-group figure on a cohort whose
// numbers are known by construction: 12 rows over 4 parents (ratio 3),
// a 13-byte entry (u32 + categorical_u8 + u64), one constant entry.
func TestInspect_GroupFigures(t *testing.T) {
	data, schema := groupedInspectFixture(t, 12, 3)
	res := inspectGrouped(t, data, nil)

	if res.RecordCount != 12 {
		t.Fatalf("record_count = %d, want 12", res.RecordCount)
	}
	if res.Layout == nil {
		t.Fatal("a 0x02 cohort reported no layout")
	}
	if res.Layout.PulseFormatVersion != int(encoding.FormatVersionV2) {
		t.Errorf("pulse_format_version = %d, want %d", res.Layout.PulseFormatVersion, encoding.FormatVersionV2)
	}
	// Physical: one u32 index + qty u16. Logical: 4+1+8+2+1.
	if res.Layout.PhysicalRecordStride != 6 || res.Layout.LogicalRecordStride != 16 {
		t.Errorf("strides physical=%d logical=%d, want 6 and 16", res.Layout.PhysicalRecordStride, res.Layout.LogicalRecordStride)
	}
	if len(res.Groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(res.Groups))
	}

	g := res.Groups[0]
	if g.Label != "group 1 [key: order_id]" || g.Kind != "indexed" {
		t.Errorf("group 0 label/kind = %q/%q", g.Label, g.Kind)
	}
	if strings.Join(g.Key, ",") != "order_id" ||
		strings.Join(g.Members, ",") != "region,order_total" ||
		strings.Join(g.Fields, ",") != "order_id,region,order_total" {
		t.Errorf("group 0 key/members/fields = %v / %v / %v", g.Key, g.Members, g.Fields)
	}
	if g.EntryCount != 4 || g.EntryWidth != 13 || g.DictionaryBytes != 52 {
		t.Errorf("group 0 entries=%d width=%d dict=%d, want 4, 13, 52", g.EntryCount, g.EntryWidth, g.DictionaryBytes)
	}
	if g.DictionaryBytes != int64(len(schema.Groups[0].Entries)) {
		t.Errorf("dictionary_bytes %d is not the resident dictionary %d", g.DictionaryBytes, len(schema.Groups[0].Entries))
	}
	if g.Ratio != 3 || g.RatioFloor != encoding.DefaultDedupRatioFloor || g.Verdict != encoding.GroupVerdictAdmitted || g.GrowsFile {
		t.Errorf("group 0 ratio=%v floor=%v verdict=%s grows=%v", g.Ratio, g.RatioFloor, g.Verdict, g.GrowsFile)
	}
	// Same arithmetic as the import gate, over the same header facts.
	want := encoding.AssessGroup(schema, 0, 12, encoding.DefaultDedupRatioFloor)
	if g.ByteDelta != want.ByteDelta || g.BreakEvenRatio != want.BreakEvenRatio ||
		g.MemberRowBytes != want.MemberRowBytes || g.IndexWidth != want.IndexWidth {
		t.Errorf("group 0 figures %+v disagree with AssessGroup %+v", g, want)
	}

	c := res.Groups[1]
	if c.Kind != "constant" || c.EntryCount != 1 || c.IndexWidth != 0 || c.Ratio != 12 || c.Verdict != encoding.GroupVerdictAdmitted {
		t.Errorf("constant group = %+v", c)
	}
	// grows_file is the raw byte fact for every kind, verdict or not: at
	// 12 rows the constant group's descriptor outweighs what it saves.
	if c.GrowsFile != (c.ByteDelta >= 0) || !c.GrowsFile {
		t.Errorf("constant group grows_file=%v with byte_delta %d", c.GrowsFile, c.ByteDelta)
	}

	marks := map[string]*InspectFieldGroup{}
	for _, f := range res.Fields {
		marks[f.Name] = f.Group
	}
	if m := marks["order_id"]; m == nil || m.Group != 0 || !m.Key || m.Kind != "indexed" {
		t.Errorf("order_id marker = %+v", m)
	}
	if m := marks["region"]; m == nil || m.Group != 0 || m.Key {
		t.Errorf("region marker = %+v", m)
	}
	if m := marks["tenant"]; m == nil || m.Group != 1 || m.Kind != "constant" {
		t.Errorf("tenant marker = %+v", m)
	}
	if marks["qty"] != nil {
		t.Errorf("row field qty carries a group marker: %+v", marks["qty"])
	}
}

// TestInspect_GroupFiguresReadNoRecord proves the figures are
// header-only: overwriting every record byte with garbage changes no
// reported figure (the record count comes from the LENGTH).
func TestInspect_GroupFiguresReadNoRecord(t *testing.T) {
	data, schema := groupedInspectFixture(t, 12, 3)
	clean := mustMarshal(t, InspectFromBytes(data, nil))

	payload := 12 * schema.RecordByteSize()
	garbled := append([]byte(nil), data...)
	for i := len(garbled) - payload; i < len(garbled); i++ {
		garbled[i] = 0xFF
	}
	if got := mustMarshal(t, InspectFromBytes(garbled, nil)); got != clean {
		t.Fatalf("inspect read a record:\n clean:   %s\n garbled: %s", clean, got)
	}
}

// TestInspect_GroupLowRatioVerdict: a parent block that never repeats
// (ratio 1) is reported low_ratio and grows the file — as a figure,
// never an envelope warning.
func TestInspect_GroupLowRatioVerdict(t *testing.T) {
	data, _ := groupedInspectFixture(t, 8, 1)
	res := inspectGrouped(t, data, nil)
	g := res.Groups[0]
	if g.Ratio != 1 || g.Verdict != encoding.GroupVerdictLowRatio || !g.GrowsFile || g.ByteDelta < 0 {
		t.Errorf("never-repeating group = ratio %v verdict %s grows %v delta %d; want 1, low_ratio, true, ≥0",
			g.Ratio, g.Verdict, g.GrowsFile, g.ByteDelta)
	}
}

// TestInspect_GroupFiguresIgnoreDictionaryLimit: member-field
// dictionaries truncate exactly as before (DictionaryLimit / FullDict),
// and the group figures — which never list entries — are identical
// under every limit.
func TestInspect_GroupFiguresIgnoreDictionaryLimit(t *testing.T) {
	regions := make([]string, 150)
	for i := range regions {
		regions[i] = "r" + string(rune('A'+i%26)) + string(rune('a'+i/26))
	}
	data, _ := groupedInspectFixture(t, 300, 2, regions...)

	def := inspectGrouped(t, data, nil)
	full := inspectGrouped(t, data, &InspectOptions{FullDict: true})
	region := def.Fields[1]
	if !region.Dictionary.Truncated || len(region.Dictionary.Values) != DefaultDictionaryLimit || region.Dictionary.TotalEntries != 150 {
		t.Errorf("default member dictionary = %d values of %d, truncated %v", len(region.Dictionary.Values), region.Dictionary.TotalEntries, region.Dictionary.Truncated)
	}
	if full.Fields[1].Dictionary.Truncated || len(full.Fields[1].Dictionary.Values) != 150 {
		t.Errorf("FullDict member dictionary truncated: %d values", len(full.Fields[1].Dictionary.Values))
	}
	if a, b := mustMarshal(t, def.Groups), mustMarshal(t, full.Groups); a != b {
		t.Errorf("group figures depend on the dictionary limit:\n default:  %s\n fulldict: %s", a, b)
	}
	if def.Groups[0].EntryCount != 150 || def.Groups[0].Ratio != 2 {
		t.Errorf("group 0 entries=%d ratio=%v, want 150 and 2", def.Groups[0].EntryCount, def.Groups[0].Ratio)
	}
}

// TestInspect_V1CohortHasNoGroupKeys: a 0x01 cohort reports no groups
// rather than erroring, and its JSON carries none of the 0x02 keys —
// the pre-0x02 shape, byte for byte (inspect.json pins the rest).
func TestInspect_V1CohortHasNoGroupKeys(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "score", Type: encoding.FieldTypeF64, Description: "Student test score value"},
	}}
	env := InspectFromBytes(buildCohortBytes(t, schema, 4), nil)
	if len(env.Errors) != 0 {
		t.Fatalf("errors: %+v", env.Errors)
	}
	res := env.Data.(*InspectResult)
	if res.Layout != nil || res.Groups != nil || res.Fields[0].Group != nil {
		t.Errorf("0x01 cohort reported 0x02 layout: %+v", res)
	}
	body := mustMarshal(t, env)
	for _, key := range []string{`"layout"`, `"groups"`, `"group"`} {
		if strings.Contains(body, key) {
			t.Errorf("0x01 inspect JSON carries %s: %s", key, body)
		}
	}
}

// TestInspectGroupedGolden pins the full grouped inspect envelope.
func TestInspectGroupedGolden(t *testing.T) {
	data, _ := groupedInspectFixture(t, 12, 3)
	out, err := json.MarshalIndent(InspectFromBytes(data, nil), "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent: %v", err)
	}
	compareGolden(t, "inspect_grouped.json", out)
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
