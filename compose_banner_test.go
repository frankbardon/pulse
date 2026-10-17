package pulse_test

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// compose_banner_test.go pins the Compose banner (U47 E3-S3 / #326): a
// `banner` block expands into ordinary crosstab slots (per section, the
// Total block then one slot per group), runs them exactly like their
// hand-written twins on Compose and ComposeParallel, and stitches them
// into ComposedResponse.Banner with per-block letters — the Total block
// never lettered, letters never compared across groups.

const bannerCohortPath = "banner.pulse"

// writeBannerCohort writes 720 respondents: gender (2), age (3), region
// (4), aware (yes / no) and brand (A / B / C, the stub rows). brand
// leans on gender and age so the pairwise tests find real differences.
func writeBannerCohort(t testing.TB, fs afero.Fs, rows int) {
	t.Helper()
	dict := func(vals ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range vals {
			if _, err := d.Add(v); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "gender", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, Dictionary: dict("Male", "Female")},
		{Name: "age", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 1, Dictionary: dict("18-34", "35-54", "55+")},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 2, Dictionary: dict("North", "South", "East", "West")},
		{Name: "aware", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 3, Dictionary: dict("yes", "no")},
		{Name: "brand", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 4, Dictionary: dict("A", "B", "C")},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	stride := schema.RecordByteSize()
	for i := 0; i < rows; i++ {
		g, a, r := i%2, (i/2)%3, (i/6)%4
		rec := make([]byte, stride)
		rec[0], rec[1], rec[2] = byte(g), byte(a), byte(r)
		if (i*7)%5 < 3 {
			rec[3] = 0
		} else {
			rec[3] = 1
		}
		// Brand A leans male and young; C leans older.
		k := (i / 24) % 10
		switch {
		case k < 3+2*(1-g)+a/2:
			rec[4] = 0
		case k < 7:
			rec[4] = 1
		default:
			rec[4] = 2
		}
		buf.Write(rec)
	}
	if err := afero.WriteFile(fs, bannerCohortPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func bannerPulse(t testing.TB, opts pulse.Options) *pulse.Pulse {
	t.Helper()
	fs := afero.NewMemMapFs()
	writeBannerCohort(t, fs, 720)
	opts.FS = fs
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func catGroup(field string) *types.Group {
	return &types.Group{Type: types.GROUP_CATEGORY, Field: field}
}

// bannerLettersOverlay is the banner overlay: a COLUMN-scope pairwise
// prop-z with letters.
func bannerLettersOverlay() types.OverlaySpec {
	return types.OverlaySpec{Name: "sig", Kind: types.OverlayKindPairwisePropZ, Scope: types.OverlayScopeColumn,
		Params: json.RawMessage(`{"letters":{}}`)}
}

// bannerComposed is Total | Gender | Age | Region × two sections: all
// adults (brand share) and the aided-aware (brand share + a second
// measure), every slot column-normalised.
func bannerComposed(mult *types.Multiplicity) *types.ComposedRequest {
	return &types.ComposedRequest{
		Multiplicity: mult,
		Banner: &types.BannerSpec{
			Request: &types.Request{
				Cohort: &types.Cohort{Filename: bannerCohortPath},
				Crosstab: &types.CrosstabSpec{
					Rows:      []*types.Group{catGroup("brand")},
					Cell:      &types.Aggregation{Type: types.AGG_COUNT, Field: "brand", Label: "n"},
					Margins:   types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
					Normalize: types.CrosstabNormalizeColumn,
				},
			},
			Sections: []types.BannerSection{
				{Label: "all"},
				{Label: "aware",
					Filterers: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "aware", Values: []string{"yes"}}},
					Cells: []*types.Aggregation{
						{Type: types.AGG_COUNT, Field: "brand", Label: "share"},
						{Type: types.AGG_COUNT, Field: "region", Label: "share_r"},
					}},
			},
			Groups: []types.BannerGroup{
				{Label: "Gender", Columns: []*types.Group{catGroup("gender")}},
				{Label: "Age", Columns: []*types.Group{catGroup("age")}},
				{Label: "Region", Columns: []*types.Group{catGroup("region")}},
			},
			Total:    true,
			Overlays: []types.OverlaySpec{bannerLettersOverlay()},
		},
	}
}

// handWrittenBanner is the same batch written slot by slot.
func handWrittenBanner(mult *types.Multiplicity) *types.ComposedRequest {
	out := &types.ComposedRequest{Multiplicity: mult}
	type sec struct {
		label string
		filt  []*types.Filterer
		cell  *types.Aggregation
		cells []*types.Aggregation
	}
	secs := []sec{
		{label: "all", cell: &types.Aggregation{Type: types.AGG_COUNT, Field: "brand", Label: "n"}},
		{label: "aware",
			filt: []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "aware", Values: []string{"yes"}}},
			cells: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "brand", Label: "share"},
				{Type: types.AGG_COUNT, Field: "region", Label: "share_r"},
			}},
	}
	groups := []struct {
		label string
		col   string
	}{{"Total", ""}, {"Gender", "gender"}, {"Age", "age"}, {"Region", "region"}}
	for _, s := range secs {
		for _, g := range groups {
			req := &types.Request{
				Label:     s.label + ":" + g.label,
				Cohort:    &types.Cohort{Filename: bannerCohortPath},
				Filterers: s.filt,
				Crosstab: &types.CrosstabSpec{
					Rows:      []*types.Group{catGroup("brand")},
					Cell:      s.cell,
					Cells:     s.cells,
					Margins:   types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
					Normalize: types.CrosstabNormalizeColumn,
				},
			}
			if g.col != "" {
				req.Crosstab.Columns = []*types.Group{catGroup(g.col)}
				req.Overlays = []types.OverlaySpec{bannerLettersOverlay()}
			}
			out.Requests = append(out.Requests, req)
		}
	}
	return out
}

func marshalJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func composeRuns(p *pulse.Pulse) map[string]func(*types.ComposedRequest) (*types.ComposedResponse, error) {
	ctx := context.Background()
	return map[string]func(*types.ComposedRequest) (*types.ComposedResponse, error){
		"Compose": func(c *types.ComposedRequest) (*types.ComposedResponse, error) { return p.Compose(ctx, c) },
		"ComposeParallel": func(c *types.ComposedRequest) (*types.ComposedResponse, error) {
			return p.ComposeParallel(ctx, c, pulse.ComposeOptions{MaxWorkers: 3})
		},
	}
}

// TestComposeBanner_MatchesHandWritten: Total | Gender | Age | Region ×
// two KPI sections with letters equals the hand-written Compose slot for
// slot, on both orchestrators and under a compose-family correction (the
// family spans the banner slots, so the letters read the batch's
// corrected significance). The stitched table copies every slot's cells,
// keys and margins into its block.
func TestComposeBanner_MatchesHandWritten(t *testing.T) {
	p := bannerPulse(t, pulse.Options{})
	for _, mult := range []*types.Multiplicity{nil, {Method: types.MultiplicityMethodHolm, Family: types.MultiplicityFamilyCompose}} {
		for name, run := range composeRuns(p) {
			got, err := run(bannerComposed(mult))
			if err != nil {
				t.Fatalf("%s banner: %v", name, err)
			}
			want, err := run(handWrittenBanner(mult))
			if err != nil {
				t.Fatalf("%s hand-written: %v", name, err)
			}
			if len(got.Responses) != 8 || len(want.Responses) != 8 {
				t.Fatalf("%s: %d / %d responses, want 8", name, len(got.Responses), len(want.Responses))
			}
			for i := range want.Responses {
				if g, w := marshalJSON(t, got.Responses[i]), marshalJSON(t, want.Responses[i]); g != w {
					t.Fatalf("%s mult=%v slot %d differs:\n got %s\nwant %s", name, mult != nil, i, g, w)
				}
			}
			if want.Banner != nil {
				t.Fatalf("hand-written batch carries a banner")
			}
			if got.Banner == nil || len(got.Banner.Sections) != 2 {
				t.Fatalf("%s: banner = %+v, want two sections", name, got.Banner)
			}
			checkStitched(t, got)
		}
	}
}

// checkStitched pins that every block's cells, column keys and margins
// are its slot's, copied into place.
func checkStitched(t *testing.T, resp *types.ComposedResponse) {
	t.Helper()
	bySlot := map[string]*types.Response{}
	for _, r := range resp.Responses {
		bySlot[r.Label] = r
	}
	for _, sec := range resp.Banner.Sections {
		labels := []string{}
		for _, b := range sec.Blocks {
			labels = append(labels, b.Label)
		}
		if want := []string{"Total", "Gender", "Age", "Region"}; !slices.Equal(labels, want) {
			t.Fatalf("section %s blocks %v, want %v", sec.Label, labels, want)
		}
		for _, b := range sec.Blocks {
			m := bySlot[b.Slot].Crosstab.Matrix
			if b.Count != len(m.ColumnKeys) || !reflect.DeepEqual(sec.ColumnKeys[b.Start:b.Start+b.Count], m.ColumnKeys) {
				t.Fatalf("block %s column keys %v, slot %v", b.Slot, sec.ColumnKeys[b.Start:b.Start+b.Count], m.ColumnKeys)
			}
			if !reflect.DeepEqual(b.ColumnHeader, m.ColumnHeader) || !reflect.DeepEqual(b.GrandTotal, m.GrandTotal) {
				t.Fatalf("block %s header / grand total differ from the slot", b.Slot)
			}
			if !reflect.DeepEqual(sec.RowKeys, m.RowKeys) {
				t.Fatalf("block %s row keys %v, section %v", b.Slot, m.RowKeys, sec.RowKeys)
			}
			for r := range m.Cells {
				if !reflect.DeepEqual(sec.Cells[r][b.Start:b.Start+b.Count], m.Cells[r]) {
					t.Fatalf("block %s row %d cells differ", b.Slot, r)
				}
			}
			if !reflect.DeepEqual(b.RowMargins, m.RowMargins) {
				t.Fatalf("block %s row margins differ", b.Slot)
			}
			if len(m.ColumnMargins) > 0 && !reflect.DeepEqual(sec.ColumnMargins[b.Start:b.Start+b.Count], m.ColumnMargins) {
				t.Fatalf("block %s column margins differ", b.Slot)
			}
		}
	}
}

// TestComposeBanner_LettersNeverCrossGroups: the stitched letters run
// on through the group blocks without repeating, each block's columns
// form their own comparison groups, every Higher / Above letter names a
// column of the SAME block, and the Total block carries no letter cell —
// it is compared to nothing, even sitting beside lettered columns. Each
// stitched cell is its slot's letters cell through the banner map.
func TestComposeBanner_LettersNeverCrossGroups(t *testing.T) {
	p := bannerPulse(t, pulse.Options{})
	resp, err := p.Compose(context.Background(), bannerComposed(nil))
	if err != nil {
		t.Fatal(err)
	}
	bySlot := map[string]*types.Response{}
	for _, r := range resp.Responses {
		bySlot[r.Label] = r
	}
	sawAbove := false
	for _, sec := range resp.Banner.Sections {
		if len(sec.Letters) != 1 || sec.Letters[0].Overlay != 0 || sec.Letters[0].Name != "sig" {
			t.Fatalf("section %s letters = %+v, want the one banner overlay", sec.Label, sec.Letters)
		}
		cells := sec.Letters[0].Cells
		blockOf := func(col int) int {
			for i, b := range sec.Blocks {
				if col >= b.Start && col < b.Start+b.Count {
					return i
				}
			}
			return -1
		}
		letterBlock := map[string]int{}
		groupBlock := map[int]int{}
		for r := range cells {
			for c, cell := range cells[r] {
				b := blockOf(c)
				if sec.Blocks[b].Total {
					if cell.Present {
						t.Fatalf("section %s: Total column %d row %d carries a letter %+v", sec.Label, c, r, cell.Value)
					}
					continue
				}
				if !cell.Present {
					t.Fatalf("section %s: group column %d row %d has no letter", sec.Label, c, r)
				}
				v := cell.Value.(types.PairwiseLetterCell)
				if prev, ok := letterBlock[v.Letter]; ok && prev != b {
					t.Fatalf("letter %s repeats across blocks %d and %d", v.Letter, prev, b)
				}
				letterBlock[v.Letter] = b
				if prev, ok := groupBlock[v.Group]; ok && prev != b {
					t.Fatalf("group %d spans blocks %d and %d", v.Group, prev, b)
				}
				groupBlock[v.Group] = b
			}
		}
		for r := range cells {
			for c, cell := range cells[r] {
				if !cell.Present {
					continue
				}
				v := cell.Value.(types.PairwiseLetterCell)
				for _, l := range append(append([]string{}, v.Higher...), v.Above...) {
					if letterBlock[l] != blockOf(c) {
						t.Fatalf("section %s column %d compares against %s of another block", sec.Label, c, l)
					}
				}
				if len(v.Above) > 0 {
					sawAbove = true
				}
			}
		}
		// Gender is A,B; Age C,D,E; Region F..I — and each maps the
		// slot's own letters (A.. restarting per slot) by the block's
		// offset.
		offset := 0
		for _, b := range sec.Blocks {
			if b.Total {
				continue
			}
			shift := func(l string) string { return string(rune(l[0]) + rune(offset)) }
			part := bySlot[b.Slot].Overlays[0].Payload.Parts["letters"].Matrix
			for r := range part.Cells {
				for c := range part.Cells[r] {
					local := part.Cells[r][c].Value.(types.PairwiseLetterCell)
					want := types.PairwiseLetterCell{Letter: shift(local.Letter), Group: groupBlockIndex(sec, b.Slot), Higher: []string{}, Above: []string{}}
					for _, l := range local.Higher {
						want.Higher = append(want.Higher, shift(l))
					}
					for _, l := range local.Above {
						want.Above = append(want.Above, shift(l))
					}
					if got := cells[r][b.Start+c].Value.(types.PairwiseLetterCell); !reflect.DeepEqual(got, want) {
						t.Fatalf("block %s cell [%d][%d] = %+v, want %+v (slot %+v)", b.Slot, r, c, got, want, local)
					}
				}
			}
			offset += b.Count
		}
		first := func(col int) string {
			return cells[0][col].Value.(types.PairwiseLetterCell).Letter
		}
		var got []string
		for c := sec.Blocks[1].Start; c < len(sec.ColumnKeys); c++ {
			got = append(got, first(c))
		}
		if want := []string{"A", "B", "C", "D", "E", "F", "G", "H", "I"}; !slices.Equal(got, want) {
			t.Fatalf("section %s banner letters %v, want %v", sec.Label, got, want)
		}
	}
	if !sawAbove {
		t.Fatal("no column is significantly above another — the cohort does not exercise the letters")
	}
}

// groupBlockIndex is the banner comparison group of a single-group
// block: its position among the lettered (non-Total) blocks.
func groupBlockIndex(sec types.BannerSectionResult, slot string) int {
	g := 0
	for _, b := range sec.Blocks {
		if b.Total {
			continue
		}
		if b.Slot == slot {
			return g
		}
		g++
	}
	return -1
}

// TestComposeBanner_ReturnShapingKeepsTheStitch: a base-request
// `return` that drops the crosstab trims every banner slot on the wire,
// but the banner slots ran unshaped (the rank veto), so the stitched
// banner — pinned outside every `return` — is still whole.
func TestComposeBanner_ReturnShapingKeepsTheStitch(t *testing.T) {
	p := bannerPulse(t, pulse.Options{})
	c := bannerComposed(nil)
	c.Banner.Request.Return = &types.Return{Preset: types.ReturnPresetFull, Exclude: []string{"crosstab", "overlays"}, Precision: 2}
	resp, err := p.Compose(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Responses []map[string]json.RawMessage `json:"responses"`
		Banner    *types.BannerResult          `json:"banner"`
	}
	if err := json.Unmarshal([]byte(marshalJSON(t, resp)), &wire); err != nil {
		t.Fatal(err)
	}
	for i, r := range wire.Responses {
		if _, ok := r["crosstab"]; ok {
			t.Fatalf("slot %d still carries crosstab on the wire", i)
		}
	}
	if wire.Banner == nil || len(wire.Banner.Sections) != 2 || len(wire.Banner.Sections[0].Letters) != 1 {
		t.Fatalf("banner on the wire = %+v, want both sections with letters", wire.Banner)
	}
	full, err := p.Compose(context.Background(), bannerComposed(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resp.Banner, full.Banner) {
		t.Fatal("a slot `return` changed the stitched banner")
	}
}

// TestComposeBanner_RefusalsMatchPredict: every structural fault is
// PULSE_BANNER_INVALID with the same reason from PredictCompose,
// Compose and ComposeParallel; a hidden capability:compose_banner is an
// unknown field on all three; and MaxComposeSlots counts the expanded
// banner slots before any runs.
func TestComposeBanner_RefusalsMatchPredict(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*types.BannerSpec)
		reason string
	}{
		{"no request", func(b *types.BannerSpec) { b.Request = nil }, "request_missing"},
		{"label set", func(b *types.BannerSpec) { b.Request.Label = "x" }, "request_label_set"},
		{"columns set", func(b *types.BannerSpec) { b.Request.Crosstab.Columns = []*types.Group{catGroup("gender")} }, "columns_set"},
		{"long shape", func(b *types.BannerSpec) { b.Request.Crosstab.Shape = types.CrosstabShapeLong }, "shape_long"},
		{"no sections", func(b *types.BannerSpec) { b.Sections = nil }, "sections_empty"},
		{"section label empty", func(b *types.BannerSpec) { b.Sections[1].Label = "" }, "label_empty"},
		{"section label twice", func(b *types.BannerSpec) { b.Sections[1].Label = "all" }, "label_duplicate"},
		{"no groups", func(b *types.BannerSpec) { b.Groups, b.Total = nil, false }, "groups_empty"},
		{"group named Total", func(b *types.BannerSpec) { b.Groups[0].Label = "Total" }, "label_reserved"},
		{"group without columns", func(b *types.BannerSpec) { b.Groups[2].Columns = nil }, "columns_empty"},
	}
	p := bannerPulse(t, pulse.Options{})
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := bannerComposed(nil)
			tc.mutate(c.Banner)
			env, err := p.PredictCompose(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			if len(env.Errors) == 0 || env.Errors[0].Code != string(errors.PULSE_BANNER_INVALID) ||
				env.Errors[0].Details["reason"] != tc.reason {
				t.Fatalf("predict errors = %+v, want PULSE_BANNER_INVALID %s", env.Errors, tc.reason)
			}
			for name, run := range composeRuns(p) {
				_, err := run(c)
				var ce *errors.CodedError
				if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_BANNER_INVALID || ce.Details["reason"] != tc.reason {
					t.Fatalf("%s err = %v, want PULSE_BANNER_INVALID %s", name, err, tc.reason)
				}
			}
		})
	}

	t.Run("slot limit", func(t *testing.T) {
		lp := bannerPulse(t, pulse.Options{Limits: pulse.Limits{MaxComposeSlots: 7}})
		env, err := lp.PredictCompose(ctx, bannerComposed(nil))
		if err != nil {
			t.Fatal(err)
		}
		if len(env.Errors) == 0 || env.Errors[0].Code != string(errors.PULSE_LIMIT_EXCEEDED) {
			t.Fatalf("predict errors = %+v, want PULSE_LIMIT_EXCEEDED", env.Errors)
		}
		for name, run := range composeRuns(lp) {
			if _, err := run(bannerComposed(nil)); !isCode(err, errors.PULSE_LIMIT_EXCEEDED) {
				t.Fatalf("%s err = %v, want PULSE_LIMIT_EXCEEDED", name, err)
			}
		}
	})

	t.Run("hidden capability", func(t *testing.T) {
		hp := bannerPulse(t, pulse.Options{FeatureProfile: bannerlessProfile(t)})
		env, err := hp.PredictCompose(ctx, bannerComposed(nil))
		if err != nil {
			t.Fatal(err)
		}
		if len(env.Errors) == 0 || env.Errors[0].Code != string(errors.PULSE_REQUEST_UNKNOWN_FIELD) {
			t.Fatalf("predict errors = %+v, want PULSE_REQUEST_UNKNOWN_FIELD", env.Errors)
		}
		for name, run := range composeRuns(hp) {
			if _, err := run(bannerComposed(nil)); !isCode(err, errors.PULSE_REQUEST_UNKNOWN_FIELD) {
				t.Fatalf("%s err = %v, want PULSE_REQUEST_UNKNOWN_FIELD", name, err)
			}
		}
	})
}

func isCode(err error, code errors.Code) bool {
	var ce *errors.CodedError
	return stderrors.As(err, &ce) && ce.Code == code
}

// bannerlessProfile enables everything a banner slot needs but
// capability:compose_banner.
func bannerlessProfile(t *testing.T) *pulse.FeatureProfile {
	t.Helper()
	all, err := pulse.InitFeatureProfile("")
	if err != nil {
		t.Fatal(err)
	}
	var keep []string
	for _, f := range all.Features {
		if f != "capability:compose_banner" {
			keep = append(keep, f)
		}
	}
	all.Features = keep
	return all
}

// TestComposeBanner_PredictSummary: predict reports the banner's shape
// and its slot labels without running a slot, and judges each expanded
// slot like a hand-written one.
func TestComposeBanner_PredictSummary(t *testing.T) {
	p := bannerPulse(t, pulse.Options{})
	env, err := p.PredictCompose(context.Background(), bannerComposed(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) > 0 {
		t.Fatalf("predict errors: %+v", env.Errors)
	}
	raw := marshalJSON(t, env.Data)
	var out struct {
		Valid  bool `json:"valid"`
		Banner struct {
			Sections      []string `json:"sections"`
			Groups        []string `json:"groups"`
			Total         bool     `json:"total"`
			ExpandedCount int      `json:"expanded_count"`
			Labels        []string `json:"labels"`
		} `json:"banner"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	want := []string{"all:Total", "all:Gender", "all:Age", "all:Region", "aware:Total", "aware:Gender", "aware:Age", "aware:Region"}
	if !out.Valid || out.Banner.ExpandedCount != 8 || !out.Banner.Total || !slices.Equal(out.Banner.Labels, want) ||
		!slices.Equal(out.Banner.Groups, []string{"Gender", "Age", "Region"}) {
		t.Fatalf("predict banner = %s", raw)
	}
	// A slot fault inside the banner is judged on the expanded slot.
	c := bannerComposed(nil)
	c.Banner.Groups[1].Columns = []*types.Group{catGroup("nope")}
	env, err = p.PredictCompose(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	_, runErr := p.Compose(context.Background(), c)
	if len(env.Errors) == 0 || runErr == nil {
		t.Fatalf("an unknown column field passed: predict %+v, runtime %v", env.Errors, runErr)
	}
	var ce *errors.CodedError
	if !stderrors.As(runErr, &ce) || string(ce.Code) != env.Errors[0].Code {
		t.Fatalf("runtime %v, predict %s", runErr, env.Errors[0].Code)
	}
}

// TestComposeBanner_AbsentIsByteIdentical: a banner-free request and
// response carry no `banner` key and hash as before; a banner moves the
// hash.
func TestComposeBanner_AbsentIsByteIdentical(t *testing.T) {
	hand := handWrittenBanner(nil)
	if strings.Contains(marshalJSON(t, hand), `"banner"`) {
		t.Fatal("a banner-free request marshals a banner key")
	}
	p := bannerPulse(t, pulse.Options{})
	resp, err := p.Compose(context.Background(), hand)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(marshalJSON(t, resp), `"banner"`) {
		t.Fatal("a banner-free response marshals a banner key")
	}
	with := bannerComposed(nil)
	if hand.Hash() == with.Hash() {
		t.Fatal("a banner does not move the request hash")
	}
	if a, b := bannerComposed(nil).Hash(), bannerComposed(nil).Hash(); a != b {
		t.Fatal("banner hash is unstable")
	}
}

// TestComposeBanner_EchoesTheExpandedSlots: with EchoRequest the batch
// echoes the effective request — the banner expanded into labelled
// slots, no `banner`.
func TestComposeBanner_EchoesTheExpandedSlots(t *testing.T) {
	p := bannerPulse(t, pulse.Options{EchoRequest: true})
	resp, err := p.Compose(context.Background(), bannerComposed(nil))
	if err != nil {
		t.Fatal(err)
	}
	echo := resp.NormalizedRequest
	if echo == nil || echo.Banner != nil || len(echo.Requests) != 8 || echo.Requests[5].Label != "aware:Gender" {
		t.Fatalf("echo = %+v", echo)
	}
}
