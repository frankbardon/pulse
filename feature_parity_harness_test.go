package pulse

// The hidden-name parity harness: a table of fixture profile ×
// operator category × public entry point. For every cell it runs the
// entry point once with a built-in the fixture HIDES and once with a
// never-registered name, both on the profiled instance, and requires
// the two outcomes to be byte-identical after substituting the names.
// It also runs the hidden name on an unprofiled instance and requires
// THAT outcome to differ — so a cell that would pass for an unrelated
// reason (the request failing before the name is ever resolved) is
// reported as vacuous instead of silently passing.
//
// Extending it: add a parityCategory (with an applier for each payload
// the category can ride) or a parityEntryPoint (with the payload it
// drives). Every existing cell keeps running; a category an entry
// point has no slot for is skipped by the entry point, not by the
// test.

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"os"
	"sort"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// parityFields names the source fields an applier may reference: a
// numeric and a categorical one. Chain stages >= 1 hand the applier
// the previous stage's output columns instead of the cohort's.
type parityFields struct {
	num, cat string
}

// parityCategory is one operator category the harness exercises.
type parityCategory struct {
	// name labels the category in subtest names.
	name string
	// candidates are runnable built-ins of the category; a fixture
	// cell uses the first one the fixture hides and is skipped when
	// it hides none.
	candidates []string
	// never is a name no build ever registers in the category.
	never string
	// request places op in a Request slot; nil when the category has
	// no Request slot.
	request func(r *types.Request, op string, f parityFields)
	// facet places op in a FacetRequest; nil when the category has no
	// FacetRequest slot.
	facet func(r *types.FacetRequest, op string, f parityFields)
	// filterToFile places op in a FilterToFileRequest; nil when the
	// category has no slot there.
	filterToFile func(r *FilterToFileRequest, op string, f parityFields)
	// compose places op in a ComposedRequest's own slot (its
	// Overlays); nil when the category has none. The ComposedRequest
	// arrives with two base slots labelled "a" and "b".
	compose func(r *ComposedRequest, op string, f parityFields)
	// chain places op in a ChainRequest's own slot (its Overlays); nil
	// when the category has none. The ChainRequest arrives with one
	// base stage.
	chain func(r *ChainRequest, op string, f parityFields)
	// neverOK names why a never-registered name may SUCCEED in this
	// category (the outcome is still compared byte for byte); empty
	// requires the never-registered outcome to be an error.
	neverOK string
}

// parityHost is one instance plus the cohort the harness drives.
type parityHost struct {
	p      *Pulse
	cohort string
	fields parityFields
	// base builds the request the operator under test is added to:
	// only operators the instance offers, so the base never fails on
	// its own.
	base func() *types.Request
	// chainBase is a mergeable stage-0 request whose output columns
	// a later stage can read, or nil when the instance offers no such
	// stage.
	chainBase func() *types.Request
	// chainFields are chainBase's output columns.
	chainFields parityFields
}

// parityEntryPoint is one public entry point. run reports the outcome
// bytes, or applicable=false when the entry point has no slot for the
// category (a Watch, a field-only Facet, a non-filter FilterToFile).
type parityEntryPoint struct {
	name string
	run  func(t *testing.T, h *parityHost, c parityCategory, op string) (outcome []byte, applicable bool)
	// vacuousOK names why a cell may be vacuous for this entry point —
	// the request is refused before the name resolves for EVERY name
	// (e.g. the chain gate excludes the slot). Keyed by category name
	// or by operator name.
	vacuousOK map[string]string
}

// parityOutcome is the canonical bytes of an entry point's result: the
// {code, message, details} of a coded error, the text of a plain
// error, or the JSON of a successful result.
func parityOutcome(v any, err error) []byte {
	if err != nil {
		var ce *perr.CodedError
		if stderrors.As(err, &ce) {
			b, mErr := json.Marshal(map[string]any{"code": ce.Code, "message": ce.Message, "details": ce.Details})
			if mErr != nil {
				return []byte("unmarshalable error: " + err.Error())
			}
			return b
		}
		return []byte("error: " + err.Error())
	}
	b, mErr := json.Marshal(v)
	if mErr != nil {
		return []byte("unmarshalable result: " + mErr.Error())
	}
	return append([]byte("ok: "), b...)
}

func parityIsError(outcome []byte) bool {
	return !strings.HasPrefix(string(outcome), "ok: ")
}

// parityCategories is every category the harness drives: the registry
// categories plus the regression and per-host overlay categories
// (feature_parity_overlay_test.go).
var parityCategories = append(append([]parityCategory(nil), registryParityCategories...), regOverlayParityCategories...)

// registryParityCategories are the seven registry categories (tests
// split into their two registries), plus the aggregator and grouper
// again in the crosstab slots, which resolve them at their own sites.
var registryParityCategories = []parityCategory{
	{
		name:       "aggregator",
		candidates: []string{"AGG_MAX", "AGG_MIN"},
		never:      "AGG_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			r.Aggregations = append(r.Aggregations, &types.Aggregation{Type: types.AggregationType(op), Field: f.num, Label: "probe"})
		},
	},
	{
		name:       "grouper",
		candidates: []string{"GROUP_RANGE", "GROUP_ROUNDED"},
		never:      "GROUP_NEVER_REGISTERED",
		// Prepended: the grouper under test is the request's primary
		// grouper, the one every path resolves.
		request: func(r *types.Request, op string, f parityFields) {
			r.Groups = append([]*types.Group{{Type: types.GroupType(op), Field: f.num, Interval: 10}}, r.Groups...)
		},
	},
	{
		name:       "filterer",
		candidates: []string{"FILTER_EXCLUDE", "FILTER_INCLUDE"},
		never:      "FILTER_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			r.Filterers = append(r.Filterers, &types.Filterer{Type: types.FiltererType(op), Field: f.cat, Values: []string{"south"}})
		},
		facet: func(r *types.FacetRequest, op string, f parityFields) {
			r.Filterers = append(r.Filterers, &types.Filterer{Type: types.FiltererType(op), Field: f.cat, Values: []string{"south"}})
		},
		filterToFile: func(r *FilterToFileRequest, op string, f parityFields) {
			r.Filterers = append(r.Filterers, &types.Filterer{Type: types.FiltererType(op), Field: f.cat, Values: []string{"south"}})
		},
	},
	{
		name:       "attribute",
		candidates: []string{"ATTR_FORMULA"},
		never:      "ATTR_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			r.Attributes = append(r.Attributes, &types.Attribute{Type: types.AttributeType(op), Field: f.num, Label: "probe", Expression: f.num + " * 2"})
		},
	},
	{
		name:       "window",
		candidates: []string{"WIN_RANK", "WIN_ROW_NUMBER"},
		never:      "WIN_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			r.Windows = append(r.Windows, &types.Window{Type: types.WindowType(op), Label: "probe", OrderBy: []types.OrderKey{{Field: f.cat}}})
		},
	},
	{
		name:       "feature",
		candidates: []string{"FEAT_LOG"},
		never:      "FEAT_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			r.Features = append(r.Features, &types.Feature{Type: types.FeatureType(op), Field: f.num, Label: "probe"})
		},
	},
	{
		name:       "row_test",
		candidates: []string{"TEST_SHAPIRO_WILK", "TEST_KS"},
		never:      "TEST_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			r.Tests = append(r.Tests, &types.Test{Type: types.TestType(op), Field: f.num, Label: "probe"})
		},
	},
	{
		name:       "post_test",
		candidates: []string{"TEST_TREND"},
		never:      "TEST_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			r.PostTests = append(r.PostTests, &types.Test{Type: types.TestType(op), Field: "n", Label: "probe", OrderBy: []types.OrderKey{{Field: f.cat}}})
		},
	},
	{
		// The grouper as a crosstab column axis (buffered and fused
		// crosstab resolve it at their own sites).
		name:       "crosstab_axis",
		candidates: []string{"GROUP_RANGE", "GROUP_ROUNDED"},
		never:      "GROUP_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			r.Groups, r.Aggregations = nil, nil
			r.Crosstab = &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: f.cat}},
				Columns: []*types.Group{{Type: types.GroupType(op), Field: f.num, Interval: 10}},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: f.num},
			}
		},
	},
	{
		// The aggregator as the crosstab cell.
		name:       "crosstab_cell",
		candidates: []string{"AGG_MAX", "AGG_MIN"},
		never:      "AGG_NEVER_REGISTERED",
		request: func(r *types.Request, op string, f parityFields) {
			r.Groups, r.Aggregations = nil, nil
			r.Crosstab = &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: f.cat}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: f.cat}},
				Cell:    &types.Aggregation{Type: types.AggregationType(op), Field: f.num},
			}
		},
	},
}

// chainExcluded is the reason the ProcessChain stage gate refuses a
// slot for every name: windows, features, tests and post-tests are not
// mergeable, so no name in them is ever resolved.
const chainExcluded = "the chain stage gate excludes the slot for every name"

var chainVacuous = map[string]string{
	"window": chainExcluded, "feature": chainExcluded,
	"row_test": chainExcluded, "post_test": chainExcluded,
	"crosstab_axis":    "a crosstab stage has no aggregator slot, so the chain stage gate refuses it for every name",
	"crosstab_cell":    "a crosstab stage has no aggregator slot, so the chain stage gate refuses it for every name",
	"GROUP_ROUNDED":    "GROUP_ROUNDED is not mergeable, so the chain stage gate refuses it like any unregistered grouper",
	"regression":       chainExcluded,
	"overlay_crosstab": "a crosstab stage has no aggregator slot, so the chain stage gate refuses it for every name",
	"overlay_formula":  "a crosstab stage has no aggregator slot, so the chain stage gate refuses it for every name",
	"overlay_series":   "the stage-1 request is ungrouped, so the SERIES fold never reads the kind",
}

// parityEntryPoints is every entry point the harness drives: the
// Request-, Facet- and FilterToFile-carrying ones plus the entry points
// whose own top-level slot carries the name (ComposedRequest.Overlays,
// ChainRequest.Overlays; feature_parity_overlay_test.go).
var parityEntryPoints = append(append([]parityEntryPoint(nil), requestParityEntryPoints...), overlayParityEntryPoints...)

// requestParityEntryPoints are the public entry points that carry a
// Request, FacetRequest or FilterToFileRequest.
var requestParityEntryPoints = []parityEntryPoint{
	{name: "Process", run: func(t *testing.T, h *parityHost, c parityCategory, op string) ([]byte, bool) {
		req, ok := h.request(c, op)
		if !ok {
			return nil, false
		}
		resp, err := h.p.Process(context.Background(), req)
		return parityOutcome(resp, err), true
	}},
	{name: "ProcessStream", run: func(t *testing.T, h *parityHost, c parityCategory, op string) ([]byte, bool) {
		req, ok := h.request(c, op)
		if !ok {
			return nil, false
		}
		ctx := context.Background()
		it, err := h.p.ProcessStream(ctx, req)
		if err != nil {
			return parityOutcome(nil, err), true
		}
		defer it.Close()
		var rows []Row
		for {
			row, more, nErr := it.Next(ctx)
			if nErr != nil {
				return parityOutcome(nil, nErr), true
			}
			if !more {
				break
			}
			rows = append(rows, row)
		}
		return parityOutcome(rows, nil), true
	}},
	{name: "Compose", run: func(t *testing.T, h *parityHost, c parityCategory, op string) ([]byte, bool) {
		req, ok := h.request(c, op)
		if !ok {
			return nil, false
		}
		resp, err := h.p.Compose(context.Background(), &ComposedRequest{Requests: []*Request{req}})
		return parityOutcome(resp, err), true
	}},
	{name: "ComposeParallel", run: func(t *testing.T, h *parityHost, c parityCategory, op string) ([]byte, bool) {
		req, ok := h.request(c, op)
		if !ok {
			return nil, false
		}
		resp, err := h.p.ComposeParallel(context.Background(), &ComposedRequest{Requests: []*Request{req}}, ComposeOptions{MaxWorkers: 2})
		return parityOutcome(resp, err), true
	}},
	{name: "ProcessChain/stage0", vacuousOK: chainVacuous, run: func(t *testing.T, h *parityHost, c parityCategory, op string) ([]byte, bool) {
		req, ok := h.request(c, op)
		if !ok {
			return nil, false
		}
		resp, err := h.p.ProcessChain(context.Background(), &ChainRequest{
			Cohort: &types.Cohort{Filename: h.cohort},
			Stages: []*types.ChainStage{{Name: "probe", Request: req}},
		})
		return parityOutcome(resp, err), true
	}},
	{name: "ProcessChain/stage1", vacuousOK: chainVacuous, run: func(t *testing.T, h *parityHost, c parityCategory, op string) ([]byte, bool) {
		if c.request == nil || h.chainBase == nil {
			return nil, false
		}
		stage1 := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: h.chainFields.num, Label: "total"}}}
		if !h.p.svc.InstanceSnapshot().Enabled(string(types.AGG_SUM)) {
			return nil, false
		}
		c.request(stage1, op, h.chainFields)
		resp, err := h.p.ProcessChain(context.Background(), &ChainRequest{
			Cohort: &types.Cohort{Filename: h.cohort},
			Stages: []*types.ChainStage{{Name: "base", Request: h.chainBase()}, {Name: "probe", Request: stage1}},
		})
		return parityOutcome(resp, err), true
	}},
	{name: "FacetSchema", run: func(t *testing.T, h *parityHost, c parityCategory, op string) ([]byte, bool) {
		if c.facet == nil {
			return nil, false
		}
		req := &types.FacetRequest{Cohort: &types.Cohort{Filename: h.cohort}, Fields: []string{h.fields.num}}
		c.facet(req, op, h.fields)
		resp, err := h.p.FacetSchema(context.Background(), req)
		return parityOutcome(resp, err), true
	}},
	{name: "FacetSchema/additive", run: func(t *testing.T, h *parityHost, c parityCategory, op string) ([]byte, bool) {
		if c.facet == nil {
			return nil, false
		}
		req := &types.FacetRequest{
			Cohort:         &types.Cohort{Filename: h.cohort},
			Fields:         []string{h.fields.num},
			AdditiveFields: []string{h.fields.num},
		}
		c.facet(req, op, h.fields)
		resp, err := h.p.FacetSchema(context.Background(), req)
		return parityOutcome(resp, err), true
	}},
	// Facet (field-only) and Watch (a path) carry no operator name, so
	// no category applies; they are listed so the matrix names every
	// entry point the contract does.
	{name: "Facet", run: func(*testing.T, *parityHost, parityCategory, string) ([]byte, bool) { return nil, false }},
	{name: "Watch", run: func(*testing.T, *parityHost, parityCategory, string) ([]byte, bool) { return nil, false }},
	{name: "FilterToFile", run: func(t *testing.T, h *parityHost, c parityCategory, op string) ([]byte, bool) {
		if c.filterToFile == nil {
			return nil, false
		}
		req := &FilterToFileRequest{SourcePath: h.cohort, OutputDir: "out"}
		c.filterToFile(req, op, h.fields)
		resp, err := h.p.FilterToFileWithRequest(context.Background(), req)
		if resp != nil {
			// The output path embeds a predicate hash, which differs
			// by name; the row count is the observable result.
			return parityOutcome(resp.RowCount, err), true
		}
		return parityOutcome(nil, err), true
	}},
}

// request builds the base request plus op in category c's slot.
func (h *parityHost) request(c parityCategory, op string) (*types.Request, bool) {
	if c.request == nil {
		return nil, false
	}
	req := h.base()
	req.Cohort = &types.Cohort{Filename: h.cohort}
	c.request(req, op, h.fields)
	return req, true
}

const parityCohort = "parity.pulse"

// parityFS writes the harness cohort: a numeric and a categorical
// column, enough rows for the tests to be computable.
func parityFS(t *testing.T) afero.Fs {
	t.Helper()
	fsys := afero.NewMemMapFs()
	var rows [][]string
	regions := []string{"north", "south", "east", "west"}
	for i := 0; i < 40; i++ {
		rows = append(rows, []string{regions[i%4], strings.TrimSpace(jsonNumber(float64(10 + (i*7)%53)))})
	}
	createTestPulseFile(t, fsys, parityCohort, []string{"region", "age"}, rows)
	return fsys
}

func jsonNumber(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

// newParityHost builds an instance over fsys with the named fixture
// profile ("" = no profile) and derives its base requests from the
// operators it offers.
func newParityHost(t *testing.T, fsys afero.Fs, fixture string) *parityHost {
	t.Helper()
	opts := Options{FS: fsys}
	if fixture != "" {
		raw, err := os.ReadFile(featureSetFixtureDir + fixture + ".json")
		if err != nil {
			t.Fatalf("read fixture %s: %v", fixture, err)
		}
		fp, err := ParseFeatureProfile(raw)
		if err != nil {
			t.Fatalf("parse fixture %s: %v", fixture, err)
		}
		opts.FeatureProfile = fp
	}
	p, err := New(opts)
	if err != nil {
		t.Fatalf("New(%s): %v", fixture, err)
	}
	inst := p.svc.InstanceSnapshot()
	h := &parityHost{p: p, cohort: parityCohort, fields: parityFields{num: "age", cat: "region"}}
	h.base = func() *types.Request {
		req := &types.Request{}
		if inst.Enabled(string(types.GROUP_CATEGORY)) {
			req.Groups = append(req.Groups, &types.Group{Type: types.GROUP_CATEGORY, Field: "region"})
		}
		if inst.Enabled(string(types.AGG_COUNT)) {
			req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_COUNT, Field: "age", Label: "n"})
		}
		return req
	}
	if inst.Enabled(string(types.GROUP_CATEGORY)) && inst.Enabled(string(types.AGG_COUNT)) {
		h.chainBase = func() *types.Request {
			return &types.Request{
				Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "age", Label: "n"}},
			}
		}
		h.chainFields = parityFields{num: "n", cat: "region"}
	}
	return h
}

// hiddenCandidate returns the first candidate the instance hides.
func hiddenCandidate(h *parityHost, c parityCategory) (string, bool) {
	for _, n := range c.candidates {
		if h.p.svc.InstanceSnapshot().Hidden(n) {
			return n, true
		}
	}
	return "", false
}

// runHiddenParity drives the whole matrix over the given fixtures,
// categories and entry points. It is the harness entry later stories
// extend: pass a wider category or entry-point table.
func runHiddenParity(t *testing.T, fixtures []string, categories []parityCategory, entries []parityEntryPoint) {
	t.Helper()
	fsys := parityFS(t)
	unscoped := newParityHost(t, fsys, "")
	applicable := 0
	for _, fixture := range fixtures {
		host := newParityHost(t, fsys, fixture)
		// The unprofiled control runs the SAME base the fixture
		// offers, so a vacuous cell is one the name never reaches.
		control := *unscoped
		control.base = host.base
		control.chainBase = host.chainBase
		control.chainFields = host.chainFields
		for _, c := range categories {
			hidden, ok := hiddenCandidate(host, c)
			if !ok {
				continue
			}
			for _, ep := range entries {
				t.Run(fixture+"/"+c.name+"/"+ep.name, func(t *testing.T) {
					got, ok := ep.run(t, host, c, hidden)
					if !ok {
						t.Skipf("%s has no %s slot", ep.name, c.name)
					}
					applicable++
					want, _ := ep.run(t, host, c, c.never)
					if !parityIsError(want) && c.neverOK == "" {
						t.Fatalf("never-registered %s succeeded: %s", c.never, want)
					}
					if subst := strings.ReplaceAll(string(got), hidden, c.never); subst != string(want) {
						t.Errorf("hidden %s diverges from never-registered %s\nhidden: %s\nnever:  %s", hidden, c.never, got, want)
					}
					open, _ := ep.run(t, &control, c, hidden)
					if strings.ReplaceAll(string(open), hidden, c.never) == string(want) {
						reason, ok := ep.vacuousOK[c.name]
						if !ok {
							reason, ok = ep.vacuousOK[hidden]
						}
						if !ok && host.chainBase == nil && strings.HasPrefix(ep.name, "ProcessChain") {
							reason, ok = "the fixture offers no mergeable stage-0 base", true
						}
						if ok {
							t.Logf("vacuous by design: %s", reason)
							return
						}
						if fixture == "empty" {
							t.Logf("vacuous: the empty fixture offers no base operators")
							return
						}
						t.Errorf("vacuous cell: %s resolves the same unprofiled — the name is never reached\noutcome: %s", hidden, open)
					}
				})
			}
		}
	}
	if applicable == 0 {
		t.Fatal("no applicable cell ran")
	}
}

// TestHiddenOperatorParity: across every fixture profile, every
// registry operator category and every public entry point, a hidden
// built-in is indistinguishable from a never-registered name.
func TestHiddenOperatorParity(t *testing.T) {
	fixtures := append([]string(nil), featureSetFixtures...)
	sort.Strings(fixtures)
	runHiddenParity(t, fixtures, parityCategories, parityEntryPoints)
}
