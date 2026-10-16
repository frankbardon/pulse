package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// compose_banner_sections_test.go pins the banner's per-slot label
// scoping and per-section overlays (U47 E4-S8): a base `labels` binding
// rides only the slots that group by its field, and a section's own
// `overlays` replace the banner-level ones on its group slots.

func bannerLabelTables() map[string]pulse.LabelTable {
	return map[string]pulse.LabelTable{
		"gender_names": {Rows: map[string]string{"Male": "Men", "Female": "Women"}},
		"region_names": {Rows: map[string]string{"North": "N", "South": "S", "East": "E", "West": "W"}},
		"brand_names":  {Rows: map[string]string{"A": "Acme", "B": "Bolt", "C": "Crest"}},
		"aware_names":  {Rows: map[string]string{"yes": "Aware", "no": "Unaware"}},
	}
}

func bannerLabels(fields ...string) []*types.LabelBinding {
	var out []*types.LabelBinding
	for _, f := range fields {
		out = append(out, &types.LabelBinding{Field: f, Table: f + "_names"})
	}
	return out
}

func labelWarnings(resp *types.Response) []string {
	var out []string
	for _, w := range resp.Warnings {
		if w.Code == string(errors.PULSE_LABEL_NOT_APPLIED) {
			out = append(out, w.Details["field"].(string))
		}
	}
	return out
}

// TestComposeBanner_LabelsScopedToGroupingSlots: base `labels` on the
// row field and two column fields label every banner column without a
// single PULSE_LABEL_NOT_APPLIED — each slot carries only the bindings
// it groups by, equal slot for slot to the hand-written twin labelled
// that way, on Compose and ComposeParallel — and the stitched banner
// carries the display labels. A binding no slot groups by still warns.
func TestComposeBanner_LabelsScopedToGroupingSlots(t *testing.T) {
	p := bannerPulse(t, pulse.Options{Extensions: pulse.Extensions{LabelTables: bannerLabelTables()}})
	for name, run := range composeRuns(p) {
		c := bannerComposed(nil)
		c.Banner.Request.Labels = bannerLabels("brand", "gender", "region")
		got, err := run(c)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		hand := handWrittenBanner(nil)
		for _, r := range hand.Requests {
			r.Labels = bannerLabels("brand")
			switch {
			case strings.HasSuffix(r.Label, ":Gender"):
				r.Labels = bannerLabels("brand", "gender")
			case strings.HasSuffix(r.Label, ":Region"):
				r.Labels = bannerLabels("brand", "region")
			}
		}
		want, err := run(hand)
		if err != nil {
			t.Fatalf("%s hand-written: %v", name, err)
		}
		for i := range want.Responses {
			if w := labelWarnings(got.Responses[i]); len(w) > 0 {
				t.Fatalf("%s slot %s warns PULSE_LABEL_NOT_APPLIED on %v", name, got.Responses[i].Label, w)
			}
			if g, w := marshalJSON(t, got.Responses[i]), marshalJSON(t, want.Responses[i]); g != w {
				t.Fatalf("%s slot %d differs:\n got %s\nwant %s", name, i, g, w)
			}
		}
		checkStitched(t, got)
		sec := got.Banner.Sections[0]
		keys := marshalJSON(t, sec.ColumnKeys) + marshalJSON(t, sec.RowKeys)
		for _, l := range []string{`"Men"`, `"Women"`, `"N"`, `"W"`, `"Acme"`} {
			if !strings.Contains(keys, l) {
				t.Fatalf("%s stitched keys %s lack display label %s", name, keys, l)
			}
		}
	}

	// `aware` is filtered on, never grouped by: no slot can label it,
	// so it stays on every slot and warns there.
	c := bannerComposed(nil)
	c.Banner.Request.Labels = bannerLabels("gender", "aware")
	got, err := p.Compose(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got.Responses {
		if w := labelWarnings(r); len(w) != 1 || w[0] != "aware" {
			t.Fatalf("slot %s label warnings %v, want only aware", r.Label, w)
		}
	}
}

// looseLettersOverlay is a section overlay: column-scope prop-z letters
// at a looser alpha, under its own name.
func looseLettersOverlay() types.OverlaySpec {
	return types.OverlaySpec{Name: "loose", Kind: types.OverlayKindPairwisePropZ, Scope: types.OverlayScopeColumn,
		Params: json.RawMessage(`{"letters":{"alpha":0.2}}`)}
}

// TestComposeBanner_SectionOverlaysReplace: a section's own overlays
// replace the banner-level ones on its group slots — equal slot for slot
// to the hand-written batch, on Compose and ComposeParallel, with and
// without a compose-family correction (the family counts the section
// overlays' layers, not the replaced ones) — and the section's letters
// are stitched from them (`section: true`), still never crossing groups
// and never lettering the Total block.
func TestComposeBanner_SectionOverlaysReplace(t *testing.T) {
	p := bannerPulse(t, pulse.Options{})
	for _, mult := range []*types.Multiplicity{nil, {Method: types.MultiplicityMethodHolm, Family: types.MultiplicityFamilyCompose}} {
		for name, run := range composeRuns(p) {
			c := bannerComposed(mult)
			c.Banner.Sections[1].Overlays = []types.OverlaySpec{looseLettersOverlay()}
			got, err := run(c)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			hand := handWrittenBanner(mult)
			for _, r := range hand.Requests {
				if strings.HasPrefix(r.Label, "aware:") && len(r.Overlays) > 0 {
					r.Overlays = []types.OverlaySpec{looseLettersOverlay()}
				}
			}
			want, err := run(hand)
			if err != nil {
				t.Fatalf("%s hand-written: %v", name, err)
			}
			for i := range want.Responses {
				if g, w := marshalJSON(t, got.Responses[i]), marshalJSON(t, want.Responses[i]); g != w {
					t.Fatalf("%s mult=%v slot %d differs:\n got %s\nwant %s", name, mult != nil, i, g, w)
				}
			}
			checkStitched(t, got)
			all, aware := got.Banner.Sections[0], got.Banner.Sections[1]
			if len(all.Letters) != 1 || all.Letters[0].Name != "sig" || all.Letters[0].Section {
				t.Fatalf("%s section all letters = %+v, want the banner overlay", name, all.Letters)
			}
			if len(aware.Letters) != 1 || aware.Letters[0].Name != "loose" || !aware.Letters[0].Section || aware.Letters[0].Overlay != 0 {
				t.Fatalf("%s section aware letters = %+v, want the section overlay", name, aware.Letters)
			}
			for r := range aware.Letters[0].Cells {
				for col, cell := range aware.Letters[0].Cells[r] {
					if col < aware.Blocks[1].Start && cell.Present {
						t.Fatalf("%s Total column %d carries a section letter", name, col)
					}
				}
			}
		}
	}
	// Absent section overlays: the wire carries no `section` key.
	resp, err := p.Compose(context.Background(), bannerComposed(nil))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(marshalJSON(t, resp.Banner), `"section"`) {
		t.Fatal("a section-overlay-free banner marshals a section key")
	}
	if strings.Contains(marshalJSON(t, bannerComposed(nil)), `"overlays":null`) {
		t.Fatal("an absent section overlay list marshals")
	}
}

// TestComposeBanner_SectionOverlayRefusalMatchesPredict: a section
// overlay is judged on the expanded slot, so predict and both
// orchestrators refuse a bad one with the same code (ComposeParallel
// under FailFast, which surfaces the slot's own coded error).
func TestComposeBanner_SectionOverlayRefusalMatchesPredict(t *testing.T) {
	p := bannerPulse(t, pulse.Options{})
	c := bannerComposed(nil)
	bad := looseLettersOverlay()
	bad.Params = json.RawMessage(`{"letters":{"alpha":2}}`)
	c.Banner.Sections[1].Overlays = []types.OverlaySpec{bad}
	env, err := p.PredictCompose(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Errors) == 0 || env.Errors[0].Code != string(errors.PULSE_OVERLAY_PARAM_MISSING) {
		t.Fatalf("predict errors = %+v, want PULSE_OVERLAY_PARAM_MISSING", env.Errors)
	}
	ctx := context.Background()
	runs := map[string]func() error{
		"Compose": func() error { _, err := p.Compose(ctx, c); return err },
		"ComposeParallel": func() error {
			_, err := p.ComposeParallel(ctx, c, pulse.ComposeOptions{MaxWorkers: 3, FailFast: true})
			return err
		},
	}
	for name, run := range runs {
		err := run()
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || string(ce.Code) != env.Errors[0].Code {
			t.Fatalf("%s err = %v, predict %s", name, err, env.Errors[0].Code)
		}
	}
}
