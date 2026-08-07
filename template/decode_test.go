package template_test

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/template"
	"github.com/frankbardon/pulse/types"
)

// targetTmpl builds a template for an arbitrary target. renderTmpl (in
// render_test.go) hardcodes TargetRequest, which is right for the render
// walk's tests and wrong for the decode matrix.
func targetTmpl(target template.Target, vars []*template.Variable, body string) *template.Template {
	return &template.Template{
		Name:      "decode/fixture",
		Target:    target,
		Variables: vars,
		Body:      json.RawMessage(body),
	}
}

// populated reports which Rendered typed pointers are non-nil, by field
// name, in declaration order. It reflects rather than enumerating by hand
// so a sixth target added later without a matching arm here is caught by
// the "exactly one" assertion instead of silently skipped.
func populated(t *testing.T, r *template.Rendered) []string {
	t.Helper()
	v := reflect.ValueOf(*r)
	typ := v.Type()
	var out []string
	for i := range typ.NumField() {
		f := typ.Field(i)
		if f.Type.Kind() != reflect.Ptr {
			continue // Target and JSON are not typed request pointers
		}
		if !v.Field(i).IsNil() {
			out = append(out, f.Name)
		}
	}
	return out
}

// assertOnlyField asserts that want is the single populated typed pointer.
func assertOnlyField(t *testing.T, r *template.Rendered, want string) {
	t.Helper()
	got := populated(t, r)
	if len(got) != 1 || got[0] != want {
		t.Fatalf("populated typed pointers = %v, want exactly [%s]", got, want)
	}
}

// decodeRow is one case in the target matrix.
type decodeRow struct {
	name      string
	target    template.Target
	body      string
	supplied  map[string]any
	vars      []*template.Variable
	wantField string
	verify    func(t *testing.T, r *template.Rendered)
}

// TestRender_TargetMatrix is the decode contract: every target decodes
// into its own request type, populates exactly that one pointer, and
// leaves the other four nil.
func TestRender_TargetMatrix(t *testing.T) {
	rows := []decodeRow{
		{
			name:   "request",
			target: template.TargetRequest,
			vars:   decls(varDecl("metric", template.VarField)),
			body: `{"cohort":{"filename":"sales.pulse"},
			        "aggregations":[{"type":"AGG_SUM","field":{"$var":"metric"}}]}`,
			supplied:  map[string]any{"metric": "revenue"},
			wantField: "Request",
			verify: func(t *testing.T, r *template.Rendered) {
				req := r.Request
				if req.Cohort == nil || req.Cohort.Filename != "sales.pulse" {
					t.Errorf("Cohort = %+v, want filename sales.pulse", req.Cohort)
				}
				if len(req.Aggregations) != 1 {
					t.Fatalf("len(Aggregations) = %d, want 1", len(req.Aggregations))
				}
				if got := req.Aggregations[0]; got.Type != types.AGG_SUM || got.Field != "revenue" {
					t.Errorf("Aggregations[0] = %+v, want AGG_SUM over revenue", got)
				}
			},
		},
		{
			name:   "composed",
			target: template.TargetComposed,
			vars:   decls(varDecl("metric", template.VarField)),
			body: `{"requests":[
			         {"label":"a","cohort":{"filename":"sales.pulse"},
			          "aggregations":[{"type":"AGG_COUNT","field":{"$var":"metric"}}]},
			         {"label":"b","cohort":{"filename":"sales.pulse"},
			          "aggregations":[{"type":"AGG_SUM","field":{"$var":"metric"}}]}]}`,
			supplied:  map[string]any{"metric": "revenue"},
			wantField: "Composed",
			verify: func(t *testing.T, r *template.Rendered) {
				if len(r.Composed.Requests) != 2 {
					t.Fatalf("len(Requests) = %d, want 2", len(r.Composed.Requests))
				}
				if got := r.Composed.Requests[1].Aggregations[0].Field; got != "revenue" {
					t.Errorf("Requests[1].Aggregations[0].Field = %q, want %q", got, "revenue")
				}
			},
		},
		{
			name:   "chain",
			target: template.TargetChain,
			vars:   decls(varDecl("metric", template.VarField)),
			body: `{"cohort":{"filename":"sales.pulse"},
			        "stages":[{"name":"total",
			                   "request":{"aggregations":[{"type":"AGG_SUM","field":{"$var":"metric"}}]}}]}`,
			supplied:  map[string]any{"metric": "revenue"},
			wantField: "Chain",
			verify: func(t *testing.T, r *template.Rendered) {
				if len(r.Chain.Stages) != 1 {
					t.Fatalf("len(Stages) = %d, want 1", len(r.Chain.Stages))
				}
				st := r.Chain.Stages[0]
				if st.Name != "total" || st.Request == nil {
					t.Fatalf("Stages[0] = %+v, want a named stage carrying a request", st)
				}
				if got := st.Request.Aggregations[0].Field; got != "revenue" {
					t.Errorf("Stages[0].Request.Aggregations[0].Field = %q, want %q", got, "revenue")
				}
			},
		},
		{
			name:   "facet",
			target: template.TargetFacet,
			vars:   decls(listDecl("fields", template.VarString)),
			body: `{"cohort":{"filename":"sales.pulse"},
			        "fields":{"$var":"fields"},"discrete_top_k":5}`,
			supplied:  map[string]any{"fields": []string{"region", "tier"}},
			wantField: "Facet",
			verify: func(t *testing.T, r *template.Rendered) {
				want := []string{"region", "tier"}
				if !reflect.DeepEqual(r.Facet.Fields, want) {
					t.Errorf("Fields = %v, want %v", r.Facet.Fields, want)
				}
				if r.Facet.DiscreteTopK != 5 {
					t.Errorf("DiscreteTopK = %d, want 5", r.Facet.DiscreteTopK)
				}
			},
		},
		{
			name:      "sample",
			target:    template.TargetSample,
			vars:      decls(varDecl("n", template.VarInteger)),
			body:      `{"cohort":{"filename":"sales.pulse"},"n":{"$var":"n"}}`,
			supplied:  map[string]any{"n": 25},
			wantField: "Sample",
			verify: func(t *testing.T, r *template.Rendered) {
				if r.Sample.N != 25 {
					t.Errorf("N = %d, want 25", r.Sample.N)
				}
			},
		},
	}

	// Every target must appear in the matrix — a sixth added later with
	// no row here fails this check rather than going untested.
	if len(rows) != len(template.AllTargets()) {
		t.Fatalf("matrix covers %d targets, want %d", len(rows), len(template.AllTargets()))
	}

	for _, tt := range rows {
		t.Run(tt.name, func(t *testing.T) {
			tm := targetTmpl(tt.target, tt.vars, tt.body)
			got, err := template.Render(tm, tt.supplied)
			if err != nil {
				t.Fatalf("Render() = %v, want nil", err)
			}
			if got.Target != tt.target {
				t.Errorf("Target = %q, want %q", got.Target, tt.target)
			}
			assertOnlyField(t, got, tt.wantField)

			// JSON carries exactly what RenderJSON produced.
			want, err := template.RenderJSON(tm, tt.supplied)
			if err != nil {
				t.Fatalf("RenderJSON() = %v, want nil", err)
			}
			if string(got.JSON) != string(want) {
				t.Errorf("JSON =\n  %s\nwant\n  %s", got.JSON, want)
			}
			if !json.Valid(got.JSON) {
				t.Errorf("JSON is not valid JSON: %s", got.JSON)
			}
			tt.verify(t, got)
		})
	}
}

// TestRendered_Typed asserts the Typed accessor returns the pointer the
// Target selects, and nil for the zero value.
func TestRendered_Typed(t *testing.T) {
	tests := []struct {
		name string
		body string
		tgt  template.Target
		want any
	}{
		{"request", `{"cohort":{"filename":"a.pulse"}}`, template.TargetRequest, &types.Request{}},
		{"composed", `{"requests":[]}`, template.TargetComposed, &types.ComposedRequest{}},
		{"chain", `{"cohort":{"filename":"a.pulse"},"stages":[]}`, template.TargetChain, &types.ChainRequest{}},
		{"facet", `{"fields":[]}`, template.TargetFacet, &types.FacetRequest{}},
		{"sample", `{"n":1}`, template.TargetSample, &types.SampleRequest{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := template.Render(targetTmpl(tt.tgt, nil, tt.body), nil)
			if err != nil {
				t.Fatalf("Render() = %v, want nil", err)
			}
			got := r.Typed()
			if got == nil {
				t.Fatal("Typed() = nil, want the populated request pointer")
			}
			if reflect.TypeOf(got) != reflect.TypeOf(tt.want) {
				t.Errorf("Typed() type = %T, want %T", got, tt.want)
			}
		})
	}

	t.Run("zero value", func(t *testing.T) {
		var r template.Rendered
		if got := r.Typed(); got != nil {
			t.Errorf("Typed() = %v, want nil", got)
		}
	})

	t.Run("nil receiver", func(t *testing.T) {
		var r *template.Rendered
		if got := r.Typed(); got != nil {
			t.Errorf("Typed() = %v, want nil", got)
		}
	})
}

// TestRender_UnknownFieldNamesTheField is the papercut guard. Strict
// decode is harsher than the rest of Pulse, which tolerates unknown
// fields — that tolerance is how the examples/ `_meta` block survives at
// execution. The message has to name the offending key, or a body pasted
// from an examples/ file fails with no clue why.
func TestRender_UnknownFieldNamesTheField(t *testing.T) {
	tests := []struct {
		name      string
		target    template.Target
		body      string
		wantField string
	}{
		{
			name:      "examples _meta block at the root",
			target:    template.TargetRequest,
			body:      `{"_meta":{"operators":["AGG_SUM"]},"cohort":{"filename":"a.pulse"}}`,
			wantField: "_meta",
		},
		{
			name:      "typo on a request slot",
			target:    template.TargetRequest,
			body:      `{"cohort":{"filename":"a.pulse"},"aggregation":[]}`,
			wantField: "aggregation",
		},
		{
			name:      "unknown key nested inside an operator",
			target:    template.TargetRequest,
			body:      `{"aggregations":[{"type":"AGG_SUM","field":"x","bucket":10}]}`,
			wantField: "bucket",
		},
		{
			name:      "unknown key on a non-request target",
			target:    template.TargetFacet,
			body:      `{"fields":["region"],"top_k":5}`,
			wantField: "top_k",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tm := targetTmpl(tt.target, nil, tt.body)
			got, err := template.Render(tm, nil)
			if err == nil {
				t.Fatalf("Render() = %+v, want %s", got, perr.PULSE_TEMPLATE_RENDER_INVALID)
			}
			if got != nil {
				t.Errorf("Render() returned %+v alongside an error", got)
			}
			if !perr.HasCode(err, perr.PULSE_TEMPLATE_RENDER_INVALID) {
				t.Fatalf("Render() = %v, want %s", err, perr.PULSE_TEMPLATE_RENDER_INVALID)
			}
			ce := codedError(t, err)
			if !strings.Contains(ce.Message, tt.wantField) {
				t.Errorf("message %q does not name the offending field %q", ce.Message, tt.wantField)
			}
			if got := ce.Details["field"]; got != tt.wantField {
				t.Errorf("details[\"field\"] = %v, want %q", got, tt.wantField)
			}
			if got := ce.Details[perr.DetailTemplate]; got != tm.Name {
				t.Errorf("details[%q] = %v, want %q", perr.DetailTemplate, got, tm.Name)
			}
			if got := ce.Details["target"]; got != string(tt.target) {
				t.Errorf("details[\"target\"] = %v, want %q", got, tt.target)
			}
			if ce.Cause == nil {
				t.Error("coded error carries no cause; the decoder fault should be wrapped")
			}
		})
	}
}

// TestRender_StructuralMismatch covers the other half of strict decode: a
// well-formed JSON value of the wrong shape for the slot it landed in.
func TestRender_StructuralMismatch(t *testing.T) {
	tests := []struct {
		name     string
		target   template.Target
		vars     []*template.Variable
		body     string
		supplied map[string]any
	}{
		{
			name:   "string where an array belongs",
			target: template.TargetRequest,
			body:   `{"cohort":{"filename":"a.pulse"},"aggregations":"AGG_SUM"}`,
		},
		{
			name:   "object where a string belongs",
			target: template.TargetRequest,
			body:   `{"cohort":{"filename":{"dir":"a"}}}`,
		},
		{
			name:     "a substituted string lands in a numeric slot",
			target:   template.TargetSample,
			vars:     decls(varDecl("n", template.VarString)),
			body:     `{"n":{"$var":"n"}}`,
			supplied: map[string]any{"n": "twenty"},
		},
		{
			name:     "a substituted list lands in a scalar slot",
			target:   template.TargetRequest,
			vars:     decls(listDecl("metric", template.VarString)),
			body:     `{"aggregations":[{"type":"AGG_SUM","field":{"$var":"metric"}}]}`,
			supplied: map[string]any{"metric": []string{"a", "b"}},
		},
		{
			name:   "array where an object belongs",
			target: template.TargetChain,
			body:   `{"cohort":{"filename":"a.pulse"},"stages":[{"request":[]}]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tm := targetTmpl(tt.target, tt.vars, tt.body)
			got, err := template.Render(tm, tt.supplied)
			if err == nil {
				t.Fatalf("Render() = %+v, want %s", got, perr.PULSE_TEMPLATE_RENDER_INVALID)
			}
			if !perr.HasCode(err, perr.PULSE_TEMPLATE_RENDER_INVALID) {
				t.Fatalf("Render() = %v, want %s", err, perr.PULSE_TEMPLATE_RENDER_INVALID)
			}
			ce := codedError(t, err)
			if strings.TrimSpace(ce.Message) == "" {
				t.Error("coded error carries an empty message")
			}
			if got := ce.Details[perr.DetailTemplate]; got != tm.Name {
				t.Errorf("details[%q] = %v, want %q", perr.DetailTemplate, got, tm.Name)
			}
			if got := ce.Details["target"]; got != string(tt.target) {
				t.Errorf("details[\"target\"] = %v, want %q", got, tt.target)
			}
			// A structural fault is not an unknown-field fault; the
			// field detail must not be invented.
			if _, ok := ce.Details["field"]; ok {
				t.Errorf("details carries a %q key for a structural fault: %v", "field", ce.Details)
			}
		})
	}
}

// TestRender_Fixture is the end-to-end epic check: the on-disk fixture
// plus a caller variable map produces a populated *types.Request with the
// substituted values sitting in the right slots.
func TestRender_Fixture(t *testing.T) {
	tmpl, err := template.Parse(readFixture(t))
	if err != nil {
		t.Fatalf("Parse(fixture) = %v, want nil", err)
	}

	t.Run("defaults only", func(t *testing.T) {
		got, err := template.Render(tmpl, map[string]any{"metric": "revenue"})
		if err != nil {
			t.Fatalf("Render() = %v, want nil", err)
		}
		assertOnlyField(t, got, "Request")
		req := got.Request

		if req.Label != "all regions since 2024-01-01" {
			t.Errorf("Label = %q, want the interpolated default", req.Label)
		}
		if req.Cohort == nil || req.Cohort.Filename != "sales.pulse" {
			t.Errorf("Cohort = %+v, want filename sales.pulse", req.Cohort)
		}
		if len(req.Aggregations) != 1 || req.Aggregations[0].Field != "revenue" {
			t.Fatalf("Aggregations = %+v, want one AGG_SUM over revenue", req.Aggregations)
		}
		if req.Aggregations[0].Type != types.AGG_SUM {
			t.Errorf("Aggregations[0].Type = %q, want %q", req.Aggregations[0].Type, types.AGG_SUM)
		}
		// The `segments` guard drops the second grouper.
		if len(req.Groups) != 1 {
			t.Fatalf("len(Groups) = %d, want 1 (the guarded grouper must be dropped)", len(req.Groups))
		}
		if req.Groups[0].Type != types.GROUP_RANGE || req.Groups[0].Interval != 10 {
			t.Errorf("Groups[0] = %+v, want GROUP_RANGE at the default interval 10", req.Groups[0])
		}
	})

	t.Run("caller-supplied", func(t *testing.T) {
		got, err := template.Render(tmpl, map[string]any{
			"metric":   "revenue",
			"bucket":   25,
			"label":    "north and south",
			"segments": []string{"north", "south"},
		})
		if err != nil {
			t.Fatalf("Render() = %v, want nil", err)
		}
		req := got.Request
		if req.Label != "north and south since 2024-01-01" {
			t.Errorf("Label = %q, want the interpolated caller value", req.Label)
		}
		if len(req.Groups) != 2 {
			t.Fatalf("len(Groups) = %d, want 2 (the guard resolved, so the grouper survives)", len(req.Groups))
		}
		if req.Groups[0].Interval != 25 {
			t.Errorf("Groups[0].Interval = %v, want 25", req.Groups[0].Interval)
		}
		g := req.Groups[1]
		if g.Type != types.GROUP_CATEGORY || g.Field != "region" {
			t.Errorf("Groups[1] = %+v, want GROUP_CATEGORY over region", g)
		}
		if want := []string{"north", "south"}; !reflect.DeepEqual(g.Include, want) {
			t.Errorf("Groups[1].Include = %v, want %v", g.Include, want)
		}
	})
}

// TestRender_ResolutionFaultsSurface asserts Render does not swallow or
// re-code the faults RenderJSON raises. Render is a decode step bolted
// onto the render walk, not a second validation pass.
func TestRender_ResolutionFaultsSurface(t *testing.T) {
	tests := []struct {
		name     string
		vars     []*template.Variable
		body     string
		supplied map[string]any
		want     perr.Code
	}{
		{
			name: "required variable missing",
			vars: decls(&template.Variable{Name: "metric", Type: template.VarField, Required: true}),
			body: `{"aggregations":[{"type":"AGG_SUM","field":{"$var":"metric"}}]}`,
			want: perr.PULSE_TEMPLATE_VAR_MISSING,
		},
		{
			name:     "unknown supplied variable",
			vars:     decls(varDecl("metric", template.VarField)),
			body:     `{"aggregations":[{"type":"AGG_SUM","field":{"$var":"metric"}}]}`,
			supplied: map[string]any{"metrc": "revenue"},
			want:     perr.PULSE_TEMPLATE_VAR_UNKNOWN,
		},
		{
			name:     "supplied value contradicts the declared type",
			vars:     decls(varDecl("metric", template.VarField)),
			body:     `{"aggregations":[{"type":"AGG_SUM","field":{"$var":"metric"}}]}`,
			supplied: map[string]any{"metric": 7},
			want:     perr.PULSE_TEMPLATE_VAR_TYPE,
		},
		{
			name: "declared but unresolved marker",
			vars: decls(varDecl("metric", template.VarField)),
			body: `{"aggregations":[{"type":"AGG_SUM","field":{"$var":"metric"}}]}`,
			want: perr.PULSE_TEMPLATE_UNRESOLVED,
		},
		{
			name: "undeclared marker is an author error",
			vars: nil,
			body: `{"aggregations":[{"type":"AGG_SUM","field":{"$var":"metric"}}]}`,
			want: perr.PULSE_TEMPLATE_INVALID,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := template.Render(targetTmpl(template.TargetRequest, tt.vars, tt.body), tt.supplied)
			if !perr.HasCode(err, tt.want) {
				t.Fatalf("Render() = (%+v, %v), want %s", got, err, tt.want)
			}
			if got != nil {
				t.Errorf("Render() returned %+v alongside an error", got)
			}
		})
	}
}

// TestRender_UnknownTargetIsRejected asserts an unrecognised target never
// reaches the decode switch as a silent no-op.
func TestRender_UnknownTargetIsRejected(t *testing.T) {
	tests := []struct {
		name   string
		target template.Target
	}{
		{"empty", ""},
		{"unrecognised", "REQUEST"},
		{"plausible but wrong", "process"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tm := targetTmpl(tt.target, nil, `{"cohort":{"filename":"a.pulse"}}`)
			got, err := template.Render(tm, nil)
			if !perr.HasCode(err, perr.PULSE_TEMPLATE_TARGET_UNKNOWN) {
				t.Fatalf("Render() = (%+v, %v), want %s", got, err, perr.PULSE_TEMPLATE_TARGET_UNKNOWN)
			}
		})
	}
}

// TestRender_NilTemplate asserts the entry point is nil-safe.
func TestRender_NilTemplate(t *testing.T) {
	got, err := template.Render(nil, nil)
	if !perr.HasCode(err, perr.PULSE_TEMPLATE_INVALID) {
		t.Fatalf("Render(nil) = (%+v, %v), want %s", got, err, perr.PULSE_TEMPLATE_INVALID)
	}
	if got != nil {
		t.Errorf("Render(nil) returned %+v alongside an error", got)
	}
}

// fsCapablePackages are the standard-library and third-party packages
// that can open a file, a directory, or a socket. None of them may be a
// direct import of any package on the render path.
//
// Since the store landed, template/ as a whole does reach the filesystem —
// discovering template files on disk is the store's entire job. The
// invariant is therefore enforced per FILE rather than per package: only
// the files on fsCapableFiles may name one of these, and the render path's
// files still may not.
var fsCapablePackages = map[string]bool{
	"os":                       true,
	"os/exec":                  true,
	"io/fs":                    true,
	"io/ioutil":                true,
	"path/filepath":            true,
	"net":                      true,
	"net/http":                 true,
	"embed":                    true,
	"github.com/spf13/afero":   true,
	modulePrefix + "/fs":       true,
	modulePrefix + "/encoding": true,
	modulePrefix + "/io":       true,
}

// packageImportsByFile parses every non-test .go file in dir and returns
// its direct imports, keyed by base file name. It reads the source rather
// than asking `go list` because the invariant it feeds is per-file: the
// package as a whole is allowed to reach the filesystem, and only the store
// is allowed to be the file that does.
func packageImportsByFile(t *testing.T, dir string) map[string][]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading package dir %s: %v", dir, err)
	}
	out := make(map[string][]string)
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		imports := make([]string, 0, len(f.Imports))
		for _, spec := range f.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("unquoting import %s in %s: %v", spec.Path.Value, name, err)
			}
			imports = append(imports, path)
		}
		out[name] = imports
	}
	if len(out) == 0 {
		t.Fatalf("no non-test Go files found in %s — the filesystem lock cannot inspect an empty package", dir)
	}
	return out
}

// directImports returns a package's non-test import list.
func directImports(t *testing.T, pkg string) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-f", `{{join .Imports "\n"}}`, pkg).Output()
	if err != nil {
		t.Fatalf("go list -f Imports %s failed: %v", pkg, err)
	}
	var imports []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			imports = append(imports, line)
		}
	}
	if len(imports) == 0 {
		t.Fatalf("go list returned no imports for %s", pkg)
	}
	return imports
}

// fsCapableFiles are the template package's own source files permitted to
// name a filesystem API. Template discovery reads directories and files, so
// the store gets the exception — and it is the ONLY one. Adding a file here
// is a deliberate act: it says "this file may open things", and everything
// off the list stays structurally incapable of it.
var fsCapableFiles = map[string]bool{
	"store.go": true,
}

// TestRender_NoFilesystemAccess is the "render never opens a cohort file"
// lock, asserted two ways.
//
// Structurally: no file on the render path — and neither of the two
// packages template/ is allowed to depend on — directly imports anything
// that can open a file. A render path that cannot name a filesystem API
// cannot use one, and the import firewall
// (TestTemplatePackage_ImportBoundary) already bars the packages that could
// reach one indirectly. The store is the sole exception, listed on
// fsCapableFiles, and it sits entirely outside the render path: it loads
// documents, and hands Render the same in-memory Template a caller could
// have built in Go.
//
// Behaviourally: rendering a template whose cohort names a file that does
// not exist succeeds, and creates nothing on disk. Field existence and
// operator/type compatibility stay Predict's job — Render checks the
// document, not the data.
func TestRender_NoFilesystemAccess(t *testing.T) {
	t.Run("no filesystem-capable import on the render path", func(t *testing.T) {
		for file, imports := range packageImportsByFile(t, ".") {
			if fsCapableFiles[file] {
				continue
			}
			for _, imp := range imports {
				if fsCapablePackages[imp] {
					t.Errorf("template/%s directly imports %q, which can open a file; "+
						"the render path must stay filesystem-free (only %v may reach the filesystem)",
						file, imp, slices.Sorted(maps.Keys(fsCapableFiles)))
				}
			}
		}
		for _, pkg := range []string{
			modulePrefix + "/types",
			modulePrefix + "/errors",
		} {
			for _, imp := range directImports(t, pkg) {
				if fsCapablePackages[imp] {
					t.Errorf("%s directly imports %q, which can open a file; "+
						"the render path must stay filesystem-free", pkg, imp)
				}
			}
		}
	})

	t.Run("a cohort that does not exist still renders", func(t *testing.T) {
		dir := t.TempDir()
		missing := filepath.Join(dir, "does-not-exist.pulse")

		tm := targetTmpl(template.TargetRequest,
			decls(varDecl("path", template.VarString)),
			`{"cohort":{"filename":{"$var":"path"}},
			  "aggregations":[{"type":"AGG_COUNT","field":"revenue"}]}`)

		got, err := template.Render(tm, map[string]any{"path": missing})
		if err != nil {
			t.Fatalf("Render() = %v, want nil — render must not open the cohort", err)
		}
		if got.Request.Cohort.Filename != missing {
			t.Errorf("Cohort.Filename = %q, want %q", got.Request.Cohort.Filename, missing)
		}
		if _, err := os.Stat(missing); !os.IsNotExist(err) {
			t.Errorf("os.Stat(%q) = %v, want a not-exist error — render created a file", missing, err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("reading temp dir: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("render touched the filesystem: %v", entries)
		}
	})
}
