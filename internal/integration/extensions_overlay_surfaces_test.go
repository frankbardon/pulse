package integration_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// extensions_overlay_surfaces_test.go (U67 E2-S4): an embedder overlay
// kind is a feature like any extension operator. Registered, it is
// listed by the manifest, the instance payload schema, the ontology and
// the generated reference; a feature profile that omits it hides it on
// every one of those surfaces, and predict and the runtime refuse a spec
// of it exactly as they refuse a never-registered kind.

// overlayNeverKind is registered nowhere.
const overlayNeverKind types.OverlayKind = "OVERLAY_ACME_NEVER"

// surfaceFeatures is the profile every hiding case starts from: the
// hosts and operators the fixtures' requests read plus the SERIES /
// Compose kind OVERLAY_ACME_TREND — but neither OVERLAY_ACME_SHARE
// (crosstab) nor OVERLAY_ACME_RATIO (Compose), nor any built-in kind.
var surfaceFeatures = []string{
	"capability:process", "capability:compose", "capability:crosstab",
	"AGG_COUNT", "AGG_SUM", "GROUP_CATEGORY", string(overlayTrendKind),
}

// openSurfaces is openHostsE2E with an optional feature profile.
func openSurfaces(t *testing.T, fp *pulse.FeatureProfile) (*pulse.Pulse, *types.Cohort) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "e2e.pulse", paritySchema(t), 0, paritySmallRows)
	p, err := pulse.New(pulse.Options{FS: fsys, FeatureProfile: fp, Extensions: pulse.Extensions{Overlays: []pulse.OverlayRegistration{
		shareRegistration(shareOverlay, nil), trendRegistration(), ratioRegistration()}}})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return p, &types.Cohort{Filename: "e2e.pulse"}
}

// surfaceTexts renders every discovery surface of p: the manifest, the
// instance payload schema, the ontology and the generated reference.
func surfaceTexts(t *testing.T, p *pulse.Pulse) map[string]string {
	t.Helper()
	m, err := json.Marshal(p.Manifest(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	schema, err := p.PayloadSchema()
	if err != nil {
		t.Fatal(err)
	}
	onto, err := json.Marshal(p.Ontology())
	if err != nil {
		t.Fatal(err)
	}
	out := afero.NewMemMapFs()
	if err := p.ExportReference(out, "ref", pulse.ExportReferenceOptions{}); err != nil {
		t.Fatalf("ExportReference: %v", err)
	}
	var ref strings.Builder
	if err := afero.Walk(out, "ref", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, err := afero.ReadFile(out, path)
		ref.Write(b)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"manifest": string(m), "payload schema": string(schema), "ontology": string(onto), "reference": ref.String()}
}

// TestExtensions_OverlayDiscoverableOnEverySurface: every registered
// kind is named by the manifest (its extensions.overlays entry in the
// built-in overlay shape), the payload schema's OverlayKind enum, the
// ontology and the generated reference.
func TestExtensions_OverlayDiscoverableOnEverySurface(t *testing.T) {
	t.Parallel()

	p, _ := openSurfaces(t, nil)
	for surface, text := range surfaceTexts(t, p) {
		for _, k := range []types.OverlayKind{overlayShareKind, overlayTrendKind, overlayRatioKind} {
			if !strings.Contains(text, string(k)) {
				t.Errorf("%s does not name %s", surface, k)
			}
		}
	}
	// The kinds are in the feature set the digest covers, on the
	// manifest as on the instance.
	if d := p.Manifest(context.Background()).FeatureSetDigest; d != p.FeatureSetDigest() {
		t.Errorf("manifest digest %s, instance digest %s", d, p.FeatureSetDigest())
	}
	got := p.Manifest(context.Background()).Extensions.Overlays
	if len(got) != 3 || got[0].Kind != overlayRatioKind || got[1].Kind != overlayShareKind || got[2].Kind != overlayTrendKind {
		t.Fatalf("extensions.overlays = %+v, want RATIO, SHARE, TREND", got)
	}
	share := got[1]
	if !slices.Equal(share.Shapes, []types.OverlayShape{types.OverlayShapeMatrix}) ||
		!slices.Equal(share.Scopes, []types.OverlayScope{types.OverlayScopeCell}) ||
		!slices.Equal(share.RefKinds, []string{"Margin"}) || !share.Buffered ||
		len(share.Params) != 1 || share.Params[0].Name != "scale" || share.Params[0].Type != "float" {
		t.Errorf("share entry = %+v", share)
	}
	if trend := got[2]; !slices.Equal(trend.Scopes, []types.OverlayScope{types.OverlayScopeCell, types.OverlayScopeGroup, types.OverlayScopeTotal}) ||
		len(trend.RefKinds) != 0 {
		t.Errorf("trend entry = %+v", trend)
	}
}

// TestExtensions_OverlayHiddenByFeatureProfile: a profile omitting a
// registered kind hides it on every discovery surface, and a spec of it
// is refused at predict and at runtime alike exactly as a
// never-registered kind is — on Request.Overlays, on
// ComposedRequest.Overlays, and through ApplySeriesOverlays (a hidden
// built-in SERIES kind too).
func TestExtensions_OverlayHiddenByFeatureProfile(t *testing.T) {
	t.Parallel()

	p, cohort := openSurfaces(t, &pulse.FeatureProfile{Features: surfaceFeatures})
	for surface, text := range surfaceTexts(t, p) {
		for _, k := range []types.OverlayKind{overlayShareKind, overlayRatioKind} {
			if strings.Contains(text, string(k)) {
				t.Errorf("%s names the hidden %s", surface, k)
			}
		}
	}
	m := p.Manifest(context.Background())
	if len(m.Extensions.Overlays) != 1 || m.Extensions.Overlays[0].Kind != overlayTrendKind {
		t.Errorf("extensions.overlays = %+v, want only the enabled TREND", m.Extensions.Overlays)
	}

	ctx := context.Background()
	// same holds the hidden kind's refusal to the never-registered
	// kind's: one code, one message and details up to the kind name.
	same := func(arm string, kind types.OverlayKind, hidden, never *descriptor.EnvelopeEntry, hiddenErr, neverErr error) {
		t.Helper()
		sameRefusal(t, arm+"/hidden", hidden, hiddenErr, perr.PULSE_OVERLAY_KIND_UNKNOWN)
		sameRefusal(t, arm+"/never", never, neverErr, perr.PULSE_OVERLAY_KIND_UNKNOWN)
		a, _ := json.Marshal(hidden)
		b, _ := json.Marshal(never)
		if x, y := strings.ReplaceAll(string(a), string(kind), "K"), strings.ReplaceAll(string(b), string(overlayNeverKind), "K"); x != y {
			t.Errorf("%s: hidden %s, never-registered %s", arm, x, y)
		}
	}

	process := func(kind types.OverlayKind) (*descriptor.EnvelopeEntry, error) {
		req := overlayCrosstabRequest(cohort, types.OverlaySpec{Kind: kind, Scope: types.OverlayScopeCell})
		pr, err := p.Predict(ctx, req)
		if err != nil {
			t.Fatalf("Predict: %v", err)
		}
		if len(pr.Errors) == 0 {
			t.Fatalf("predict accepted %s", kind)
		}
		_, err = p.Process(ctx, req)
		return pr.Errors[0], err
	}
	hp, he := process(overlayShareKind)
	np, ne := process(overlayNeverKind)
	same("process", overlayShareKind, hp, np, he, ne)

	compose := func(kind types.OverlayKind) (*descriptor.EnvelopeEntry, error) {
		req := composeWith(cohort, types.OverlayShapeMatrix, types.ComposeOverlaySpec{Name: "r", Kind: kind, Scope: types.OverlayScopeCell, Reference: "a", Targets: []string{"b"}})
		env, err := p.PredictCompose(ctx, req)
		if err != nil {
			t.Fatalf("PredictCompose: %v", err)
		}
		if len(env.Errors) == 0 {
			t.Fatalf("predict-compose accepted %s", kind)
		}
		_, err = p.Compose(ctx, req)
		return env.Errors[0], err
	}
	hc, hce := compose(overlayRatioKind)
	nc, nce := compose(overlayNeverKind)
	same("compose", overlayRatioKind, hc, nc, hce, nce)

	// ApplySeriesOverlays has no predict twin: the hidden built-in
	// SERIES kind is refused like the never-registered one — under a
	// scope its own SERIES rule would refuse, so no rule of the hidden
	// kind is consulted first.
	series := func(kind types.OverlayKind) error {
		_, err := p.ApplySeriesOverlays(ctx, &pulse.SeriesOverlayRequest{
			Points:   []pulse.SeriesPoint{{Key: types.AxisKey{"a"}, Value: 1, Present: true}, {Key: types.AxisKey{"b"}, Value: 2, Present: true}},
			Overlays: []types.OverlaySpec{{Kind: kind, Scope: types.OverlayScopeRow}},
		})
		return err
	}
	var hs, ns *perr.CodedError
	if !stderrors.As(series(types.OverlayKindDeltaVsPrior), &hs) || !stderrors.As(series(overlayNeverKind), &ns) ||
		hs.Code != perr.PULSE_OVERLAY_KIND_UNKNOWN || hs.Code != ns.Code ||
		strings.ReplaceAll(hs.Message, string(types.OverlayKindDeltaVsPrior), "K") != strings.ReplaceAll(ns.Message, string(overlayNeverKind), "K") {
		t.Fatalf("ApplySeriesOverlays hidden %v, never-registered %v; want both PULSE_OVERLAY_KIND_UNKNOWN", hs, ns)
	}
}

// TestExtensions_OverlayFeatureDependencies: an extension kind's host
// group is the hosts its handlers run on — a profile enabling a
// crosstab kind without capability:crosstab, or a Compose kind without
// capability:compose, is PULSE_FEATURE_PROFILE_DEPENDENCY — and its
// DependsOn names a known feature or pulse.New refuses it.
func TestExtensions_OverlayFeatureDependencies(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name     string
		features []string
	}{
		{"crosstab kind without crosstab", []string{"capability:process", string(overlayShareKind)}},
		{"compose kind without compose", []string{"capability:process", "capability:crosstab", string(overlayRatioKind)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), FeatureProfile: &pulse.FeatureProfile{Features: c.features},
				Extensions: pulse.Extensions{Overlays: []pulse.OverlayRegistration{shareRegistration(shareOverlay, nil), ratioRegistration()}}})
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_FEATURE_PROFILE_DEPENDENCY {
				t.Fatalf("pulse.New = %v, want PULSE_FEATURE_PROFILE_DEPENDENCY", err)
			}
		})
	}
	reg := ratioRegistration()
	reg.DependsOn = []string{"AGG_NOPE"}
	_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: pulse.Extensions{Overlays: []pulse.OverlayRegistration{reg}}})
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_FEATURE_PROFILE_UNKNOWN {
		t.Fatalf("pulse.New with DependsOn AGG_NOPE = %v, want PULSE_FEATURE_PROFILE_UNKNOWN", err)
	}
	reg.DependsOn = []string{"AGG_SUM"}
	if _, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), FeatureProfile: &pulse.FeatureProfile{Features: []string{"capability:compose", string(overlayRatioKind)}},
		Extensions: pulse.Extensions{Overlays: []pulse.OverlayRegistration{reg}}}); err == nil {
		t.Fatal("a profile enabling the kind without its DependsOn AGG_SUM was accepted")
	}
}

// TestExtensions_ApplySeriesOverlaysRunsExtensionKinds: the caller-held
// series path runs an extension kind's Series handler over the points —
// the layer a grouped Process lands over the same values — and refuses
// a spec its declaration (scope) or Validate hook refuses with the
// refusal the grouped Process raises.
func TestExtensions_ApplySeriesOverlaysRunsExtensionKinds(t *testing.T) {
	t.Parallel()

	p, cohort := openSurfaces(t, nil)
	ctx := context.Background()
	spec := types.OverlaySpec{Kind: overlayTrendKind, Scope: types.OverlayScopeGroup, Params: json.RawMessage(`{"scale":100}`)}
	resp := processE2E(t, p, overlaySeriesRequest(cohort, spec))
	points := make([]pulse.SeriesPoint, len(resp.Data))
	for i, row := range resp.Data {
		points[i] = pulse.SeriesPoint{Key: types.AxisKey{row["region"]}, Value: e2eFloat(t, row["s"]), Present: true}
	}
	got, err := p.ApplySeriesOverlays(ctx, &pulse.SeriesOverlayRequest{Points: points, Overlays: []types.OverlaySpec{spec}})
	if err != nil {
		t.Fatalf("ApplySeriesOverlays: %v", err)
	}
	if len(got.Layers) != 1 {
		t.Fatalf("layers = %+v", got.Layers)
	}
	a, _ := json.Marshal(got.Layers[0].Payload)
	b, _ := json.Marshal(resp.Overlays[0].Payload)
	if string(a) != string(b) || got.Layers[0].Kind != overlayTrendKind {
		t.Errorf("caller-held layer %s (%s), grouped Process layer %s", a, got.Layers[0].Kind, b)
	}

	for _, c := range []struct {
		name string
		mut  func(*types.OverlaySpec)
		code perr.Code
	}{
		{"scope", func(s *types.OverlaySpec) { s.Scope = types.OverlayScopeRow }, perr.PULSE_OVERLAY_SCOPE_UNSUPPORTED},
		{"validate", func(s *types.OverlaySpec) { s.Params = json.RawMessage(`{"scale":-2}`) }, perr.PULSE_EXTENSION_SPEC_REFUSED},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := spec
			c.mut(&s)
			_, want := p.Process(ctx, overlaySeriesRequest(cohort, s))
			_, err := p.ApplySeriesOverlays(ctx, &pulse.SeriesOverlayRequest{Points: points, Overlays: []types.OverlaySpec{s}})
			var we, ge *perr.CodedError
			if !stderrors.As(want, &we) || !stderrors.As(err, &ge) || ge.Code != c.code || we.Code != ge.Code || we.Message != ge.Message {
				t.Fatalf("ApplySeriesOverlays %v, grouped Process %v; want both %s", err, want, c.code)
			}
		})
	}
}

// TestCompose_SchemaGatePrecedesKeySetMatchesPredict: two Compose slots
// whose grouper kinds AND keys differ are PULSE_OVERLAY_SCHEMA_DIVERGENT
// at predict and at runtime alike — the runtime's structural schema
// match runs before its key-set alignment, and predict's differently
// named SERIES key rule after its schema match — on MATRIX and SERIES
// slots, for a built-in and an extension kind; and two crosstab slots
// whose cell aggregations write different payloads (a label list vs one
// number) are refused by the cell-shape arm on both sides.
func TestCompose_SchemaGatePrecedesKeySetMatchesPredict(t *testing.T) {
	t.Parallel()

	p, cohort := openHostsE2E(t)
	ctx := context.Background()
	region := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
	day := []*types.Group{{Type: types.GROUP_DATE, Field: "day"}}
	sum := func() *types.Aggregation { return &types.Aggregation{Type: types.AGG_SUM, Field: "qty", Label: "v"} }
	xtab := func(label string, rows []*types.Group, cell *types.Aggregation) *types.Request {
		return &types.Request{Label: label, Cohort: cohort, Crosstab: &types.CrosstabSpec{Rows: rows, Cell: cell}}
	}
	grouped := func(label string, groups []*types.Group) *types.Request {
		return &types.Request{Label: label, Cohort: cohort, Groups: groups, Aggregations: []*types.Aggregation{sum()}}
	}
	cases := []struct {
		name  string
		kinds []types.OverlayKind
		slots []*types.Request
	}{
		{"matrix kinds and keys", []types.OverlayKind{types.OverlayKindIndexVsRef, overlayRatioKind},
			[]*types.Request{xtab("a", region, sum()), xtab("b", day, sum())}},
		{"series kinds and key names", []types.OverlayKind{types.OverlayKindIndexVsRef, overlayRatioKind},
			[]*types.Request{grouped("a", region), grouped("b", day)}},
		{"cell shape", []types.OverlayKind{overlayRatioKind},
			[]*types.Request{xtab("a", region, &types.Aggregation{Type: types.AGG_SET_UNION, Field: "tags", Label: "v"}), xtab("b", region, sum())}},
	}
	for _, c := range cases {
		for _, kind := range c.kinds {
			t.Run(c.name+"/"+string(kind), func(t *testing.T) {
				t.Parallel()
				req := &types.ComposedRequest{Requests: c.slots, Overlays: []types.ComposeOverlaySpec{
					{Name: "o", Kind: kind, Scope: types.OverlayScopeCell, Reference: "a", Targets: []string{"b"}}}}
				env, err := p.PredictCompose(ctx, req)
				if err != nil {
					t.Fatalf("PredictCompose: %v", err)
				}
				if len(env.Errors) == 0 {
					t.Fatal("predict-compose accepted the slots")
				}
				_, err = p.Compose(ctx, req)
				sameRefusal(t, "compose", env.Errors[0], err, perr.PULSE_OVERLAY_SCHEMA_DIVERGENT)
			})
		}
	}
}
