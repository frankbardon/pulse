package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Shard archives with parent groups (E5-S3). Every fixture is a
// synthetic parent/child cohort: rows fan out under a parent id whose
// block (region — a NULLABLE non-key categorical member — weight and,
// optionally, a set member) is deduped into an indexed group keyed by
// parent, plus a global-constant `src` in a constant group. Each shard
// is written in a FLAT (0x01) form and a GROUPED (0x02) twin, so every
// grouped archive can be checked against the flat archive built from
// the same data: identical answers on every read path is the parity
// the whole story rests on.

var gRegions = []string{"north", "south", "east", "west"}

type gShard struct {
	lo, hi, fanout int
	idBase         int
	regionDict     []string // this shard's own dictionary ORDER
	src            string
	weightShift    int // added to the parent's weight (a key violation when != across shards)
	tagRung        encoding.FieldType
	tagDict        []string
	tagsOf         func(p int) []string
	nullRegion     func(p int) bool
}

func gDict(vals []string) *encoding.Dictionary {
	d := encoding.NewDictionary()
	for _, v := range vals {
		_, _ = d.Add(v)
	}
	return d
}

func gIndex(dict []string, v string) uint64 {
	for i, s := range dict {
		if s == v {
			return uint64(i)
		}
	}
	panic("value " + v + " not in the fixture dictionary")
}

func (g gShard) schema() *encoding.Schema {
	fields := []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "parent", Type: encoding.FieldTypeU32},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Nullable: true, Dictionary: gDict(g.regionDict)},
		{Name: "weight", Type: encoding.FieldTypeF64},
	}
	if g.tagRung != 0 {
		fields = append(fields, encoding.Field{Name: "tags", Type: g.tagRung, Dictionary: gDict(g.tagDict)})
	}
	fields = append(fields,
		encoding.Field{Name: "amount", Type: encoding.FieldTypeF64},
		encoding.Field{Name: "src", Type: encoding.FieldTypeCategoricalU8, Dictionary: gDict([]string{g.src})},
	)
	off := 0
	for i := range fields {
		fields[i].ByteOffset = off
		fields[i].CsvColumnIdx = i
		off += fields[i].Type.ByteSize()
	}
	return &encoding.Schema{Fields: fields}
}

func (g gShard) specs() []encoding.GroupSpec {
	members := []string{"parent", "region", "weight"}
	if g.tagRung != 0 {
		members = append(members, "tags")
	}
	return []encoding.GroupSpec{
		{Kind: encoding.GroupKindIndexed, Members: members, Key: []string{"parent"}},
		{Kind: encoding.GroupKindConstant, Members: []string{"src"}},
	}
}

func (g gShard) isNull(p int) bool {
	if g.nullRegion != nil {
		return g.nullRegion(p)
	}
	return p%7 == 0
}

// build returns the shard's flat bytes, its grouped twin (grouped by
// specs, or by the fixture's own specs when specs is nil) and the row
// count.
func (g gShard) build(t *testing.T, specs []encoding.GroupSpec) (flat, grouped []byte, rows int) {
	t.Helper()
	schema := g.schema()
	var recs [][]uint64
	var parents []int
	for p := g.lo; p < g.hi; p++ {
		for k := 0; k < g.fanout; k++ {
			i := len(recs)
			rec := []uint64{
				uint64(g.idBase + i),
				uint64(p),
				0,
				math.Float64bits(float64(10 + p%9 + g.weightShift)),
			}
			if !g.isNull(p) {
				rec[2] = gIndex(g.regionDict, gRegions[p%4])
			}
			if g.tagRung != 0 {
				var mask uint64
				for _, tg := range g.tagsOf(p) {
					mask |= 1 << gIndex(g.tagDict, tg)
				}
				rec = append(rec, mask)
			}
			rec = append(rec, math.Float64bits(float64(i%37)+0.5), 0)
			recs = append(recs, rec)
			parents = append(parents, p)
		}
	}
	flat = writeNullablePulse(t, schema, recs, func(r, f int) bool { return f == 2 && g.isNull(parents[r]) })
	if specs == nil {
		specs = g.specs()
	}
	var out bytes.Buffer
	if _, n, err := encoding.DedupCohort(&out, bytes.NewReader(flat), specs); err != nil || n != int64(len(recs)) {
		t.Fatalf("DedupCohort: %d rows, %v", n, err)
	}
	return flat, out.Bytes(), len(recs)
}

// The two base shards: B's parents overlap A's (20..29), B's region
// dictionary is in a different ORDER (so both the member categorical and
// the group dictionary must union-merge and remap), and parents 21 and 28
// carry a NULL region in both shards — a null non-key member of an
// overlapping key, the case a remapped null placeholder would refuse.
func gShardA() gShard {
	return gShard{lo: 0, hi: 30, fanout: 12, regionDict: gRegions, src: "batch-a"}
}

func gShardB() gShard {
	return gShard{lo: 20, hi: 45, fanout: 8, idBase: 10000, regionDict: []string{"west", "east", "north", "south"}, src: "batch-a"}
}

// gEnv writes named files into a fresh memmap filesystem.
func gEnv(t *testing.T, files map[string][]byte) (*Service, afero.Fs) {
	t.Helper()
	cfg := fs.NewMemMap()
	for name, b := range files {
		if err := afero.WriteFile(cfg.Fs(), name, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return New(cfg), cfg.Fs()
}

func gRequests(path string) []*types.Request {
	cohort := &types.Cohort{Filename: path}
	return []*types.Request{
		{
			Cohort:       cohort,
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}, {Type: types.AGG_SUM, Field: "weight", Label: "w"}, {Type: types.AGG_SUM, Field: "amount", Label: "amt"}},
		},
		{
			Cohort: cohort,
			Filterers: []*types.Filterer{
				{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"north", "west"}},
				{Type: types.FILTER_RANGE, Field: "weight", Values: []string{"12", "100"}},
			},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "src"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}, {Type: types.AGG_MEDIAN, Field: "amount", Label: "med"}},
		},
		{
			Cohort:       cohort,
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "parent"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}},
		},
	}
}

// gAnswers runs gRequests over path at the given shard-worker count and
// returns one JSON document per request.
func gAnswers(t *testing.T, svc *Service, path string, workers int) []string {
	t.Helper()
	svc.SetShardWorkers(workers)
	defer svc.SetShardWorkers(0)
	var out []string
	for i, req := range gRequests(path) {
		resp, err := svc.Process(context.Background(), req)
		if err != nil {
			t.Fatalf("Process #%d over %s: %v", i, path, err)
		}
		b, err := json.Marshal(struct {
			Data       any
			Components any
		}{resp.Data, resp.Components})
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(b))
	}
	return out
}

func gAssertSameAnswers(t *testing.T, what string, got, want []string) {
	t.Helper()
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: request #%d differs\n got  %s\n want %s", what, i, got[i], want[i])
		}
	}
}

func gReadFile(t *testing.T, fsys afero.Fs, name string) []byte {
	t.Helper()
	b, err := afero.ReadFile(fsys, name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func gVerifyClean(t *testing.T, svc *Service, path string) *VerifyResult {
	t.Helper()
	res, err := svc.VerifyShardArchive(context.Background(), path)
	if err != nil {
		t.Fatalf("VerifyShardArchive: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("verify %s: %d error(s): %v", path, len(res.Errors), res.Errors[0])
	}
	return res
}

func gRegroupWarnings(ws []encoding.CohesionWarning) []encoding.CohesionWarning {
	var out []encoding.CohesionWarning
	for _, w := range ws {
		if w.Code == string(errors.PULSE_SHARD_GROUPS_REWRITTEN) {
			out = append(out, w)
		}
	}
	return out
}

// TestShardGroups_CreateUnionMergesGroupDictionaries is the core case:
// two grouped shards whose group dictionaries AND member categorical
// dictionaries diverge. The archive union-merges both canonical-first,
// the first shard is stored byte-for-byte, the second is renumbered into
// the union, nothing is reported as a layout rewrite (a dictionary
// union is the ordinary cost of an add), `shard verify` is clean and
// reports the group headroom, and every answer — serial and with
// parallel shard workers — equals the flat archive's.
func TestShardGroups_CreateUnionMergesGroupDictionaries(t *testing.T) {
	a1, a2, aRows := gShardA().build(t, nil)
	b1, b2, bRows := gShardB().build(t, nil)
	svc, fsys := gEnv(t, map[string][]byte{"a.pulse": a2, "b.pulse": b2, "fa/a.pulse": a1, "fb/b.pulse": b1})
	ctx := context.Background()

	res, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse", "b.pulse"})
	if err != nil {
		t.Fatalf("CreateShardArchive(grouped): %v", err)
	}
	if len(res.Warnings) != 0 || len(res.Regrouped) != 0 || len(res.Widened) != 0 {
		t.Fatalf("a dictionary union reported %d warning(s) %+v, regrouped %+v", len(res.Warnings), res.Warnings, res.Regrouped)
	}
	if _, err := svc.CreateShardArchive(ctx, "flat.pulse", []string{"fa/a.pulse", "fb/b.pulse"}); err != nil {
		t.Fatalf("CreateShardArchive(flat): %v", err)
	}

	doc := schemaDocFromArchive(t, fsys, "arch.pulse")
	cs := doc.Schema
	if !cs.HasGroups() || cs.GroupEntryCount(0) != 45 || cs.GroupEntryCount(1) != 1 {
		t.Fatalf("canonical groups: has=%v entries=%d/%d, want 45 parents (0..44) and 1 constant", cs.HasGroups(), cs.GroupEntryCount(0), cs.GroupEntryCount(1))
	}
	if doc.AggregateRecordCount != uint64(aRows+bRows) || doc.ShardCount != 2 {
		t.Fatalf("SHRD trailer = %d records / %d shards, want %d / 2", doc.AggregateRecordCount, doc.ShardCount, aRows+bRows)
	}
	if got := cs.Fields[2].Dictionary.Values(); fmt.Sprint(got) != fmt.Sprint(gRegions) {
		t.Fatalf("canonical region dictionary = %v, want the canonical-first union %v", got, gRegions)
	}

	// The seed shard is stored untouched; the second carries the union.
	extract := func(name string) []byte {
		rc, err := svc.ExtractShard(ctx, "arch.pulse", name)
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(rc)
		return buf.Bytes()
	}
	if !bytes.Equal(extract("a.pulse"), a2) {
		t.Fatal("the seed shard was rewritten; a union must never touch a stored shard")
	}
	bs := shardSchemaFromArchive(t, svc, "arch.pulse", "b.pulse")
	if !bytes.Equal(bs.Groups[0].Entries, cs.Groups[0].Entries) {
		t.Fatal("the arriving shard's group dictionary is not the canonical union")
	}

	// E3-S3: the per-shard stride counts are reachable on grouped shards
	// now — pin them to the absolute row counts.
	for name, want := range map[string]int{"a.pulse": aRows, "b.pulse": bRows} {
		n, err := recordCountFromBytes(extract(name), cs)
		if err != nil || n != int64(want) {
			t.Fatalf("recordCountFromBytes(%s) = %d, %v; want %d", name, n, err, want)
		}
	}
	cohort, err := svc.Open(ctx, "arch.pulse")
	if err != nil {
		t.Fatal(err)
	}
	for _, sh := range cohort.Shards() {
		want := map[string]int{"a.pulse": aRows, "b.pulse": bRows}[sh.Filename]
		if sh.RecordCount != int64(want) {
			t.Fatalf("PeekShardRecordCount(%s) = %d, want %d", sh.Filename, sh.RecordCount, want)
		}
	}

	vr := gVerifyClean(t, svc, "arch.pulse")
	if len(vr.GroupIndexHeadroom) != 2 || vr.GroupIndexHeadroom[0].Entries != 45 ||
		vr.GroupIndexHeadroom[0].Capacity != encoding.MaxGroupEntries || vr.GroupIndexHeadroom[0].Headroom != encoding.MaxGroupEntries-45 ||
		vr.GroupIndexHeadroom[1].Kind != "constant" || vr.GroupIndexHeadroom[1].Headroom != 0 {
		t.Fatalf("group headroom = %+v", vr.GroupIndexHeadroom)
	}

	// Every arm against the flat archive's SERIAL answer: Components are
	// keyed to the request, never to a worker count (E6-S3), so the
	// parallel shard reducer must match it too.
	flatSerial := gAnswers(t, svc, "flat.pulse", 1)
	for _, workers := range []int{1, 2} {
		gAssertSameAnswers(t, fmt.Sprintf("grouped archive, %d shard worker(s)", workers),
			gAnswers(t, svc, "arch.pulse", workers), flatSerial)
	}

	// remove + compact keep the canonical (grown) dictionaries and the
	// archive stays verifiable and equal to the flat archive after the
	// same operations.
	for arch, shard := range map[string]string{"arch.pulse": "a.pulse", "flat.pulse": "a.pulse"} {
		if err := svc.RemoveShard(ctx, arch, shard); err != nil {
			t.Fatalf("RemoveShard(%s): %v", arch, err)
		}
		if err := svc.CompactShardArchive(ctx, arch); err != nil {
			t.Fatalf("CompactShardArchive(%s): %v", arch, err)
		}
	}
	gVerifyClean(t, svc, "arch.pulse")
	if doc := schemaDocFromArchive(t, fsys, "arch.pulse"); doc.AggregateRecordCount != uint64(bRows) || doc.Schema.GroupEntryCount(0) != 45 {
		t.Fatalf("after remove+compact: %d records, %d entries", doc.AggregateRecordCount, doc.Schema.GroupEntryCount(0))
	}
	gAssertSameAnswers(t, "after remove+compact", gAnswers(t, svc, "arch.pulse", 1), gAnswers(t, svc, "flat.pulse", 1))
}

// TestShardGroups_CreateAndAddAgree: one rule, no asymmetry. For every
// reconciliation this story defines, creating the archive from both
// shards at once and creating it from the first then adding the second
// produce the SAME archive bytes and the same warnings.
func TestShardGroups_CreateAndAddAgree(t *testing.T) {
	a1, a2, _ := gShardA().build(t, nil)
	b1, b2, _ := gShardB().build(t, nil)
	c := gShardB()
	c.src = "batch-c"
	_, c2, _ := c.build(t, nil)
	cases := []struct{ name, a, b string }{
		{"union", "a2", "b2"},
		{"flat_archive_grouped_arrival", "a1", "b2"},
		{"grouped_archive_flat_arrival", "a2", "b1"},
		{"constant_promoted", "a2", "c2"},
	}
	files := map[string][]byte{"a1": a1, "a2": a2, "b1": b1, "b2": b2, "c2": c2}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, fsys := gEnv(t, map[string][]byte{"x/a.pulse": files[tc.a], "y/b.pulse": files[tc.b]})
			ctx := context.Background()
			created, err := svc.CreateShardArchive(ctx, "both.pulse", []string{"x/a.pulse", "y/b.pulse"})
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			if _, err := svc.CreateShardArchive(ctx, "seq.pulse", []string{"x/a.pulse"}); err != nil {
				t.Fatal(err)
			}
			added, err := svc.AddShard(ctx, "seq.pulse", "y/b.pulse")
			if err != nil {
				t.Fatalf("add: %v", err)
			}
			if !bytes.Equal(gReadFile(t, fsys, "both.pulse"), gReadFile(t, fsys, "seq.pulse")) {
				t.Fatal("create and create+add produced different archives")
			}
			if fmt.Sprint(created.Regrouped) != fmt.Sprint(added.Regrouped) || len(created.Warnings) != len(added.Warnings) {
				t.Fatalf("create reported %+v, add %+v", created.Regrouped, added.Regrouped)
			}
			gVerifyClean(t, svc, "both.pulse")
		})
	}
}

// TestShardGroups_MixedLayouts: a 0x01 shard meeting a 0x02 archive and
// the reverse are both DEFINED — the archive's layout wins, only the
// arriving shard is re-encoded, and the rewrite is reported with a
// mandatory PULSE_SHARD_GROUPS_REWRITTEN warning (archive_rewritten
// false). A grouped arrival declaring DIFFERENT groups is regrouped the
// same way. Every archive answers exactly as the flat archive does.
func TestShardGroups_MixedLayouts(t *testing.T) {
	a1, a2, _ := gShardA().build(t, nil)
	b1, b2, bRows := gShardB().build(t, nil)
	_, bOther, _ := gShardB().build(t, []encoding.GroupSpec{{Kind: encoding.GroupKindIndexed, Members: []string{"parent", "region"}}})
	svc, fsys := gEnv(t, map[string][]byte{
		"g/a.pulse": a2, "f/a.pulse": a1,
		"g/b.pulse": b2, "f/b.pulse": b1, "o/b.pulse": bOther,
	})
	ctx := context.Background()
	if _, err := svc.CreateShardArchive(ctx, "flat.pulse", []string{"f/a.pulse", "f/b.pulse"}); err != nil {
		t.Fatal(err)
	}
	want := gAnswers(t, svc, "flat.pulse", 1)
	wantParallel := gAnswers(t, svc, "flat.pulse", 2)

	for _, tc := range []struct {
		name, seed, arrival, reason string
		grouped                     bool
	}{
		{"flat archive, grouped arrival", "f/a.pulse", "g/b.pulse", RegroupIncomingFlattened, false},
		{"grouped archive, flat arrival", "g/a.pulse", "f/b.pulse", RegroupIncomingRegrouped, true},
		{"grouped archive, differently grouped arrival", "g/a.pulse", "o/b.pulse", RegroupIncomingRegrouped, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{tc.seed}); err != nil {
				t.Fatal(err)
			}
			seedBytes := gReadFile(t, fsys, tc.seed)
			res, err := svc.AddShard(ctx, "arch.pulse", tc.arrival)
			if err != nil {
				t.Fatalf("AddShard: %v", err)
			}
			ws := gRegroupWarnings(res.Warnings)
			if len(ws) != 1 || ws[0].Details["reason"] != tc.reason || ws[0].Details["archive_rewritten"] != false ||
				ws[0].Details["shards_rewritten"] != 1 || ws[0].Details["records_rewritten"] != int64(bRows) {
				t.Fatalf("warnings = %+v, want one %s (archive not rewritten, 1 shard, %d records)", res.Warnings, tc.reason, bRows)
			}
			if len(res.Regrouped) != 1 || res.Regrouped[0].Reason != tc.reason {
				t.Fatalf("Regrouped = %+v", res.Regrouped)
			}
			cs := schemaDocFromArchive(t, fsys, "arch.pulse").Schema
			if cs.HasGroups() != tc.grouped {
				t.Fatalf("archive layout moved: grouped=%v, want %v", cs.HasGroups(), tc.grouped)
			}
			rc, _ := svc.ExtractShard(ctx, "arch.pulse", "a.pulse")
			var seed bytes.Buffer
			_, _ = seed.ReadFrom(rc)
			rc.Close()
			if !bytes.Equal(seed.Bytes(), seedBytes) {
				t.Fatal("the stored shard was rewritten; only the arrival may be")
			}
			gVerifyClean(t, svc, "arch.pulse")
			gAssertSameAnswers(t, tc.name, gAnswers(t, svc, "arch.pulse", 1), want)
			gAssertSameAnswers(t, tc.name+" (parallel)", gAnswers(t, svc, "arch.pulse", 2), wantParallel)
		})
	}
}

// TestShardGroups_ConstantPromotedAcrossArchive: the arriving shard holds
// a different value for a CONSTANT group, so that group can no longer
// be constant. It is promoted to an indexed group across the WHOLE
// archive — every stored shard re-encoded — and the warning says so
// (archive_rewritten true, every shard and record counted). The
// archive stays verifiable and answers exactly as the flat archive.
func TestShardGroups_ConstantPromotedAcrossArchive(t *testing.T) {
	a1, a2, aRows := gShardA().build(t, nil)
	c := gShardB()
	c.src = "batch-c"
	c1, c2, cRows := c.build(t, nil)
	svc, fsys := gEnv(t, map[string][]byte{"a.pulse": a2, "c.pulse": c2, "f/a.pulse": a1, "f/c.pulse": c1})
	ctx := context.Background()
	if _, err := svc.CreateShardArchive(ctx, "flat.pulse", []string{"f/a.pulse", "f/c.pulse"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse"}); err != nil {
		t.Fatal(err)
	}
	stride := schemaDocFromArchive(t, fsys, "arch.pulse").Schema.RecordByteSize()
	res, err := svc.AddShard(ctx, "arch.pulse", "c.pulse")
	if err != nil {
		t.Fatalf("AddShard: %v", err)
	}
	ws := gRegroupWarnings(res.Warnings)
	if len(ws) != 1 || ws[0].Details["reason"] != RegroupConstantPromoted || ws[0].Details["archive_rewritten"] != true ||
		ws[0].Details["shards_rewritten"] != 2 || ws[0].Details["records_rewritten"] != int64(aRows+cRows) {
		t.Fatalf("warnings = %+v", res.Warnings)
	}
	cs := schemaDocFromArchive(t, fsys, "arch.pulse").Schema
	if cs.Groups[1].Kind != encoding.GroupKindIndexed || cs.GroupEntryCount(1) != 2 {
		t.Fatalf("src group: kind %v, %d entries; want indexed with 2", cs.Groups[1].Kind, cs.GroupEntryCount(1))
	}
	if cs.RecordByteSize() != stride+encoding.GroupIndexWidth {
		t.Fatalf("stride %d, want %d + one group index", cs.RecordByteSize(), stride)
	}
	gVerifyClean(t, svc, "arch.pulse")
	gAssertSameAnswers(t, "promoted archive", gAnswers(t, svc, "arch.pulse", 1), gAnswers(t, svc, "flat.pulse", 1))
	gAssertSameAnswers(t, "promoted archive (parallel)", gAnswers(t, svc, "arch.pulse", 2), gAnswers(t, svc, "flat.pulse", 2))
}

// TestShardGroups_KeyViolationRefused: a declared key is a contract, not
// a hint. An arriving shard whose parent carries a different non-key
// member than the archive's entry for the same key is refused with
// PULSE_GROUP_MEMBER_NOT_CONSTANT naming the shard — on create and on
// add — and a refused add leaves the archive byte-identical.
func TestShardGroups_KeyViolationRefused(t *testing.T) {
	_, a2, _ := gShardA().build(t, nil)
	v := gShardB()
	v.weightShift = 1
	_, v2, _ := v.build(t, nil)
	svc, fsys := gEnv(t, map[string][]byte{"a.pulse": a2, "v.pulse": v2})
	ctx := context.Background()
	if _, err := svc.CreateShardArchive(ctx, "both.pulse", []string{"a.pulse", "v.pulse"}); !errors.HasCode(err, errors.PULSE_GROUP_MEMBER_NOT_CONSTANT) {
		t.Fatalf("create err = %v, want PULSE_GROUP_MEMBER_NOT_CONSTANT", err)
	}
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse"}); err != nil {
		t.Fatal(err)
	}
	before := gReadFile(t, fsys, "arch.pulse")
	_, err := svc.AddShard(ctx, "arch.pulse", "v.pulse")
	ce := asCoded(t, err, errors.PULSE_GROUP_MEMBER_NOT_CONSTANT)
	if ce.Details["shard"] != "v.pulse" || ce.Details["field"] != "weight" {
		t.Fatalf("details = %v, want shard v.pulse, field weight", ce.Details)
	}
	if !bytes.Equal(before, gReadFile(t, fsys, "arch.pulse")) {
		t.Fatal("a refused add changed the archive")
	}
}

func asCoded(t *testing.T, err error, code errors.Code) *errors.CodedError {
	t.Helper()
	var ce *errors.CodedError
	if !asCodedError(err, &ce) || ce.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
	return ce
}

// TestShardGroups_IndexSpaceOverflow: a merged group dictionary past the
// u32 index space is PULSE_SHARD_DICT_WIDTH_OVERFLOW — a coded error,
// never a wraparound — on create and on add alike, and a refused add
// leaves the archive byte-identical. MaxGroupEntries is shrunk so the
// 45-entry union overflows while each 25/30-entry shard fits.
func TestShardGroups_IndexSpaceOverflow(t *testing.T) {
	_, a2, _ := gShardA().build(t, nil)
	_, b2, _ := gShardB().build(t, nil)
	prev := encoding.MaxGroupEntries
	encoding.MaxGroupEntries = 40
	defer func() { encoding.MaxGroupEntries = prev }()
	svc, fsys := gEnv(t, map[string][]byte{"a.pulse": a2, "b.pulse": b2})
	ctx := context.Background()
	_, err := svc.CreateShardArchive(ctx, "both.pulse", []string{"a.pulse", "b.pulse"})
	ce := asCoded(t, err, errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW)
	if ce.Details["shard"] != "b.pulse" || ce.Details["capacity"] != uint64(40) {
		t.Fatalf("details = %v", ce.Details)
	}
	if exists, _ := afero.Exists(fsys, "both.pulse"); exists {
		t.Fatal("a refused create wrote an archive")
	}
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse"}); err != nil {
		t.Fatal(err)
	}
	before := gReadFile(t, fsys, "arch.pulse")
	if _, err := svc.AddShard(ctx, "arch.pulse", "b.pulse"); !errors.HasCode(err, errors.PULSE_SHARD_DICT_WIDTH_OVERFLOW) {
		t.Fatalf("add err = %v", err)
	}
	if !bytes.Equal(before, gReadFile(t, fsys, "arch.pulse")) {
		t.Fatal("a refused add changed the archive")
	}
	// At exactly the union size the merge fits.
	encoding.MaxGroupEntries = 45
	if _, err := svc.AddShard(ctx, "arch.pulse", "b.pulse"); err != nil {
		t.Fatalf("add at a ceiling equal to the union: %v", err)
	}
}

// TestShardGroups_ArchiveRewriteIsAtomic: the constant promotion is a
// whole-archive rewrite; a failing rename leaves the original archive
// byte-identical and still openable at its old layout.
func TestShardGroups_ArchiveRewriteIsAtomic(t *testing.T) {
	_, a2, _ := gShardA().build(t, nil)
	c := gShardB()
	c.src = "batch-c"
	_, c2, _ := c.build(t, nil)
	svc, fsys := gEnv(t, map[string][]byte{"a.pulse": a2, "c.pulse": c2})
	ctx := context.Background()
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse"}); err != nil {
		t.Fatal(err)
	}
	before := gReadFile(t, fsys, "arch.pulse")
	failCfg, err := fs.New(fs.WithFs(&failingRenameFs{Fs: fsys}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(failCfg).AddShard(ctx, "arch.pulse", "c.pulse"); err == nil {
		t.Fatal("AddShard with a failing rename: expected an error")
	}
	if !bytes.Equal(before, gReadFile(t, fsys, "arch.pulse")) {
		t.Fatal("a failed archive rewrite left the archive modified")
	}
	if cs := schemaDocFromArchive(t, fsys, "arch.pulse").Schema; cs.Groups[1].Kind != encoding.GroupKindConstant {
		t.Fatal("the rolled-back archive lost its constant group")
	}
	gVerifyClean(t, svc, "arch.pulse")
}

// TestShardGroups_SetWidenOfAGroupMember: a set field that is a group
// MEMBER outgrows its rung across the two shards (6 + 6 distinct tags in
// set_u8). The archive widens to set_u16 — every entry gets wider, so
// every shard is re-encoded — reported by PULSE_SHARD_SET_WIDENED, and
// the archive answers exactly as the flat archive does, set member
// included.
func TestShardGroups_SetWidenOfAGroupMember(t *testing.T) {
	tagsA := []string{"t0", "t1", "t2", "t3", "t4", "t5"}
	tagsB := []string{"t6", "t7", "t8", "t9", "t10", "t11"}
	a := gShardA()
	a.tagRung, a.tagDict = encoding.FieldTypeSetU8, tagsA
	a.tagsOf = func(p int) []string { return []string{tagsA[p%6], tagsA[(p/3)%6]} }
	b := gShard{lo: 45, hi: 60, fanout: 5, idBase: 20000, regionDict: gRegions, src: "batch-a",
		tagRung: encoding.FieldTypeSetU8, tagDict: tagsB}
	b.tagsOf = func(p int) []string { return []string{tagsB[p%6], tagsB[(p/2)%6]} }
	a1, a2, _ := a.build(t, nil)
	b1, b2, _ := b.build(t, nil)
	svc, fsys := gEnv(t, map[string][]byte{"a.pulse": a2, "b.pulse": b2, "f/a.pulse": a1, "f/b.pulse": b1})
	ctx := context.Background()
	if _, err := svc.CreateShardArchive(ctx, "flat.pulse", []string{"f/a.pulse", "f/b.pulse"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse"}); err != nil {
		t.Fatal(err)
	}
	res, err := svc.AddShard(ctx, "arch.pulse", "b.pulse")
	if err != nil {
		t.Fatalf("AddShard: %v", err)
	}
	if len(res.Widened) != 1 || res.Widened[0].To != "set_u16" || !res.Widened[0].ArchiveWidened() {
		t.Fatalf("Widened = %+v", res.Widened)
	}
	cs := schemaDocFromArchive(t, fsys, "arch.pulse").Schema
	if cs.Fields[4].Type != encoding.FieldTypeSetU16 || cs.GroupEntryCount(0) != 45 {
		t.Fatalf("canonical tags=%v entries=%d", cs.Fields[4].Type, cs.GroupEntryCount(0))
	}
	gVerifyClean(t, svc, "arch.pulse")
	req := func(path string) *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: path},
			Groups:       []*types.Group{{Type: types.GROUP_SET_VALUE, Field: "tags"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}},
		}
	}
	var got [2]string
	for i, p := range []string{"flat.pulse", "arch.pulse"} {
		resp, err := svc.Process(ctx, req(p))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(resp.Data)
		got[i] = string(b)
	}
	if got[0] != got[1] {
		t.Fatalf("set member after widen:\n flat    %s\n grouped %s", got[0], got[1])
	}
	gAssertSameAnswers(t, "widened archive", gAnswers(t, svc, "arch.pulse", 1), gAnswers(t, svc, "flat.pulse", 1))
}

// TestShardGroups_VerifyRefusesInconsistentArchives: `shard verify`
// keeps its strict posture. An archive a Pulse writer would never
// produce — a flat shard in a grouped archive (a different physical
// stride), or a grouped shard whose entries are not a prefix of the
// canonical dictionary — is refused.
func TestShardGroups_VerifyRefusesInconsistentArchives(t *testing.T) {
	a1, a2, _ := gShardA().build(t, nil)
	// Same member dictionaries as A, so ONLY the group entries diverge
	// (B's first entry is parent 20, A's is parent 0).
	b := gShardB()
	b.regionDict = gRegions
	_, b2, _ := b.build(t, nil)
	canonical, err := readSinglePulseSchema(a2)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		shard []byte
		code  errors.Code
	}{
		{"flat shard in a grouped archive", a1, errors.PULSE_SHARD_SCHEMA_MISMATCH},
		{"non-prefix group dictionary", b2, errors.PULSE_SHARD_DICT_DIVERGENCE},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := synthesizeArchiveWithRawPayload(t, canonical, []rawShard{{Name: "a.pulse", Payload: a2}, {Name: "x.pulse", Payload: tc.shard}})
			svc, _ := gEnv(t, map[string][]byte{"arch.pulse": data})
			res, err := svc.VerifyShardArchive(context.Background(), "arch.pulse")
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Errors) != 1 || res.Errors[0].Code != tc.code {
				t.Fatalf("errors = %v, want one %s", res.Errors, tc.code)
			}
		})
	}
}

// TestShardGroups_AnchorAndFilterToFile: the anchor form opens a stored
// grouped shard — the untouched seed AND the renumbered arrival — as a
// one-shard cohort, and filter-to-file over the grouped archive and over
// an anchored grouped shard answers exactly as over the flat archive.
func TestShardGroups_AnchorAndFilterToFile(t *testing.T) {
	a1, a2, _ := gShardA().build(t, nil)
	b1, b2, _ := gShardB().build(t, nil)
	svc, _ := gEnv(t, map[string][]byte{"a.pulse": a2, "b.pulse": b2, "f/a.pulse": a1, "f/b.pulse": b1})
	ctx := context.Background()
	for arch, shards := range map[string][]string{"arch.pulse": {"a.pulse", "b.pulse"}, "flat.pulse": {"f/a.pulse", "f/b.pulse"}} {
		if _, err := svc.CreateShardArchive(ctx, arch, shards); err != nil {
			t.Fatal(err)
		}
	}
	for _, sh := range []string{"a.pulse", "b.pulse"} {
		gAssertSameAnswers(t, "anchor "+sh, gAnswers(t, svc, "arch.pulse#"+sh, 1), gAnswers(t, svc, "flat.pulse#"+sh, 1))
	}
	for _, src := range []string{"", "#b.pulse"} {
		for i, arch := range []string{"arch.pulse", "flat.pulse"} {
			out := fmt.Sprintf("out%d.pulse", i)
			if _, err := svc.FilterToFile(ctx, arch+src, out, `region in ["north", "east"] && weight > 11`); err != nil {
				t.Fatalf("FilterToFile(%s%s): %v", arch, src, err)
			}
		}
		gAssertSameAnswers(t, "filter-to-file "+src, gAnswers(t, svc, "out0.pulse", 1), gAnswers(t, svc, "out1.pulse", 1))
		if src == "" {
			gVerifyClean(t, svc, "out0.pulse")
		}
	}
}

// TestShardGroups_FilterPrecomputeEngages (E5-S1 on archives): the shard
// iterator's record and reader share the CANONICAL schema, and the
// parallel shard reducer hands each record its entry indices, so a
// filter over group members is evaluated once per canonical entry — not
// per row — on both arms, and the answer equals the per-row run and the
// flat archive's.
func TestShardGroups_FilterPrecomputeEngages(t *testing.T) {
	a1, a2, aRows := gShardA().build(t, nil)
	b1, b2, bRows := gShardB().build(t, nil)
	svc, _ := gEnv(t, map[string][]byte{"a.pulse": a2, "b.pulse": b2, "f/a.pulse": a1, "f/b.pulse": b1})
	ctx := context.Background()
	for arch, shards := range map[string][]string{"arch.pulse": {"a.pulse", "b.pulse"}, "flat.pulse": {"f/a.pulse", "f/b.pulse"}} {
		if _, err := svc.CreateShardArchive(ctx, arch, shards); err != nil {
			t.Fatal(err)
		}
	}
	req := func(path string) *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: path},
			Filterers:    []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{"north", "west"}}},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}, {Type: types.AGG_SUM, Field: "amount", Label: "amt"}},
		}
	}
	run := func(path string, workers int, precompute bool) (string, int64) {
		prev := processing.SetFilterPrecompute(precompute)
		defer processing.SetFilterPrecompute(prev)
		svc.SetShardWorkers(workers)
		defer svc.SetShardWorkers(0)
		before := processing.FilterPrecomputeStats().EntryEvaluations
		resp, err := svc.Process(ctx, req(path))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(struct{ D, C any }{resp.Data, resp.Components})
		return string(b), processing.FilterPrecomputeStats().EntryEvaluations - before
	}
	for _, workers := range []int{1, 2} {
		want, _ := run("flat.pulse", workers, true)
		got, evals := run("arch.pulse", workers, true)
		if got != want {
			t.Fatalf("workers=%d precomputed:\n got  %s\n want %s", workers, got, want)
		}
		// Serial: one verdict table over the 45 canonical entries. Per
		// shard worker: one table per shard, each bounded by the entries.
		if bound := int64(workers * 45); evals == 0 || evals > bound {
			t.Fatalf("workers=%d: %d entry evaluations over %d rows, want 1..%d", workers, evals, aRows+bRows, bound)
		}
		perRow, evals := run("arch.pulse", workers, false)
		if perRow != want || evals != 0 {
			t.Fatalf("workers=%d per-row: %d evaluations, result %s", workers, evals, perRow)
		}
	}
}

// TestShardGroups_InspectReportsArchiveGroups (E4-S2 on archives):
// inspect's archive arm reports the canonical groups — the dictionaries
// every shard's indices address — against the archive-wide record count,
// header-only.
func TestShardGroups_InspectReportsArchiveGroups(t *testing.T) {
	a1, a2, aRows := gShardA().build(t, nil)
	b1, b2, bRows := gShardB().build(t, nil)
	svc, fsys := gEnv(t, map[string][]byte{"a.pulse": a2, "b.pulse": b2, "f/a.pulse": a1, "f/b.pulse": b1})
	ctx := context.Background()
	for arch, shards := range map[string][]string{"arch.pulse": {"a.pulse", "b.pulse"}, "flat.pulse": {"f/a.pulse", "f/b.pulse"}} {
		if _, err := svc.CreateShardArchive(ctx, arch, shards); err != nil {
			t.Fatal(err)
		}
	}
	env := descriptor.Inspect(bytes.NewReader(gReadFile(t, fsys, "arch.pulse")), nil)
	res := env.Data.(*descriptor.InspectResult)
	if len(env.Errors) != 0 || res.RecordCount != int64(aRows+bRows) || len(res.Groups) != 2 || res.Layout == nil {
		t.Fatalf("inspect: errors %v, records %d, groups %d, layout %v", env.Errors, res.RecordCount, len(res.Groups), res.Layout)
	}
	g := res.Groups[0]
	if g.EntryCount != 45 || g.Ratio != float64(aRows+bRows)/45 || res.Layout.PulseFormatVersion != 2 || res.Fields[2].Group == nil {
		t.Fatalf("group 0 = %+v, layout %+v", g, res.Layout)
	}
	flatRes := descriptor.Inspect(bytes.NewReader(gReadFile(t, fsys, "flat.pulse")), nil).Data.(*descriptor.InspectResult)
	if flatRes.Groups != nil || flatRes.Layout != nil {
		t.Fatal("an ungrouped archive's inspect grew group keys")
	}
	names := []string{}
	for _, sh := range res.Shards {
		names = append(names, sh.Filename)
	}
	sort.Strings(names)
	if fmt.Sprint(names) != "[a.pulse b.pulse]" {
		t.Fatalf("shards = %v", names)
	}
}
