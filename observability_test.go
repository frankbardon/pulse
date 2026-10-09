package pulse

import (
	"context"
	stderrors "errors"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/io/csv"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// hookEvent is one recorded hook call.
type hookEvent struct {
	start bool
	info  observe.OperationInfo
	res   observe.OperationResult
	// marker is the ctx value the hook saw under obsCtxKey.
	marker any
}

type obsCtxKey struct{}

// hookRecorder records every start/end call. Start stores a marker in
// the ctx it returns so End (and the work) can prove they saw it.
type hookRecorder struct {
	mu     sync.Mutex
	events []hookEvent
}

func (r *hookRecorder) hooks() *observe.Hooks {
	return &observe.Hooks{
		OnOperationStart: func(ctx context.Context, info observe.OperationInfo) context.Context {
			r.mu.Lock()
			r.events = append(r.events, hookEvent{start: true, info: info, marker: ctx.Value(obsCtxKey{})})
			r.mu.Unlock()
			return context.WithValue(ctx, obsCtxKey{}, info.ID)
		},
		OnOperationEnd: func(ctx context.Context, info observe.OperationInfo, res observe.OperationResult) {
			r.mu.Lock()
			r.events = append(r.events, hookEvent{info: info, res: res, marker: ctx.Value(obsCtxKey{})})
			r.mu.Unlock()
		},
	}
}

// topEvents keeps the top-level operations' events.
func topEvents(evs []hookEvent) []hookEvent {
	var out []hookEvent
	for _, e := range evs {
		if e.info.Scope == observe.ScopeTop {
			out = append(out, e)
		}
	}
	return out
}

func (r *hookRecorder) take() []hookEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.events
	r.events = nil
	return out
}

const obsCohort = "obs.pulse"

func obsFixture(t *testing.T, opts Options) (*Pulse, afero.Fs) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	createTestPulseFile(t, fsys, obsCohort, []string{"id", "region", "amount"}, [][]string{
		{"1", "north", "10.5"}, {"2", "south", "20.25"}, {"3", "north", "7"},
	})
	opts.FS = fsys
	p, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return p, fsys
}

func obsRequest() *Request {
	return &Request{
		Cohort:       &types.Cohort{Filename: obsCohort},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id"}},
	}
}

// drainStream waits for a StreamResult to finish so no goroutine leaks
// past the test.
func drainStream(sr StreamResult[Row]) {
	if sr.Chunks == nil {
		return
	}
	for range sr.Chunks {
	}
	<-sr.Done
}

// obsCall is one instrumented facade method and the kind it reports.
type obsCall struct {
	method string
	kind   observe.OperationKind
	call   func(ctx context.Context, p *Pulse) error
}

func obsCalls() []obsCall {
	wbuf := func() pio.Writer { return csv.NewWriterToBuffer() }
	return []obsCall{
		{"Open", observe.OpOpen, func(ctx context.Context, p *Pulse) error { _, err := p.Open(ctx, obsCohort); return err }},
		{"Process", observe.OpProcess, func(ctx context.Context, p *Pulse) error { _, err := p.Process(ctx, obsRequest()); return err }},
		{"ProcessStream", observe.OpProcessStream, func(ctx context.Context, p *Pulse) error {
			it, err := p.ProcessStream(ctx, obsRequest())
			if it != nil {
				_ = it.Close()
			}
			return err
		}},
		{"ProcessStreamResult", observe.OpProcessStream, func(ctx context.Context, p *Pulse) error {
			sr, err := p.ProcessStreamResult(ctx, obsRequest())
			drainStream(sr)
			return err
		}},
		{"Compose", observe.OpCompose, func(ctx context.Context, p *Pulse) error {
			_, err := p.Compose(ctx, &ComposedRequest{Requests: []*Request{obsRequest()}})
			return err
		}},
		{"ComposeParallel", observe.OpComposeParallel, func(ctx context.Context, p *Pulse) error {
			_, err := p.ComposeParallel(ctx, &ComposedRequest{Requests: []*Request{obsRequest()}}, ComposeOptions{})
			return err
		}},
		{"ProcessChain", observe.OpProcessChain, func(ctx context.Context, p *Pulse) error {
			r := obsRequest()
			_, err := p.ProcessChain(ctx, &ChainRequest{Cohort: r.Cohort, Stages: []*ChainStage{{Request: r}}})
			return err
		}},
		{"Facet", observe.OpFacet, func(ctx context.Context, p *Pulse) error { _, err := p.Facet(ctx, obsCohort, "region"); return err }},
		{"FacetSchema", observe.OpFacetSchema, func(ctx context.Context, p *Pulse) error {
			_, err := p.FacetSchema(ctx, &FacetRequest{Cohort: &types.Cohort{Filename: obsCohort}})
			return err
		}},
		{"Lookup", observe.OpLookup, func(ctx context.Context, p *Pulse) error {
			_, err := p.Lookup(ctx, &LookupRequest{Cohort: &types.Cohort{Filename: obsCohort}})
			return err
		}},
		{"CountRecords", observe.OpCountRecords, func(ctx context.Context, p *Pulse) error { _, err := p.CountRecords(ctx, obsCohort); return err }},
		{"Inspect", observe.OpInspect, func(ctx context.Context, p *Pulse) error { _, err := p.Inspect(ctx, obsCohort); return err }},
		{"InspectEnvelope", observe.OpInspect, func(ctx context.Context, p *Pulse) error {
			_, err := p.InspectEnvelope(ctx, obsCohort, nil)
			return err
		}},
		{"InspectBytes", observe.OpInspect, func(ctx context.Context, p *Pulse) error { _, err := p.InspectBytes(ctx, []byte("x"), nil); return err }},
		{"Predict", observe.OpPredict, func(ctx context.Context, p *Pulse) error { _, err := p.Predict(ctx, obsRequest()); return err }},
		{"PredictBytes", observe.OpPredict, func(ctx context.Context, p *Pulse) error {
			_, err := p.PredictBytes(ctx, []byte("x"), obsRequest())
			return err
		}},
		{"Manifest", observe.OpManifest, func(ctx context.Context, p *Pulse) error { p.Manifest(ctx); return nil }},
		{"ManifestForIntent", observe.OpManifest, func(ctx context.Context, p *Pulse) error {
			_, err := p.ManifestForIntent(ctx, "compare_groups")
			return err
		}},
		{"PayloadSchema", observe.OpPayloadSchema, func(ctx context.Context, p *Pulse) error { _, err := p.PayloadSchema(); return err }},
		{"Import", observe.OpImport, func(ctx context.Context, p *Pulse) error {
			_, err := p.Import(ctx, pio.NewImportJob(newMockReader([]string{"a"}, [][]string{{"1"}}), "imported.pulse"))
			return err
		}},
		{"ImportTransfer", observe.OpImport, func(ctx context.Context, p *Pulse) error {
			_, err := p.ImportTransfer(ctx, &pio.TransferImportJob{Source: "missing.pulse.zst", Output: "out.pulse"})
			return err
		}},
		{"ImportFile", observe.OpImport, func(ctx context.Context, p *Pulse) error {
			_, err := p.ImportFile(ctx, ImportSpec{SourcePath: "missing.csv"})
			return err
		}},
		{"Export", observe.OpExport, func(ctx context.Context, p *Pulse) error {
			_, err := p.Export(ctx, pio.NewExportJob(obsCohort, wbuf()))
			return err
		}},
		{"ExportTransfer", observe.OpExport, func(ctx context.Context, p *Pulse) error {
			_, err := p.ExportTransfer(ctx, &pio.TransferExportJob{Source: obsCohort, Output: "obs.pulse.zst"})
			return err
		}},
		{"Convert", observe.OpConvert, func(ctx context.Context, p *Pulse) error {
			_, err := p.Convert(ctx, &pio.ConvertJob{Source: newMockReader([]string{"a"}, [][]string{{"1"}}), Target: wbuf()})
			return err
		}},
		{"SweepImports", observe.OpImportsSweep, func(ctx context.Context, p *Pulse) error { _, err := p.SweepImports(ctx); return err }},
		{"Drop", observe.OpDrop, func(ctx context.Context, p *Pulse) error { return p.Drop(ctx, "no-such-handle") }},
		{"Sample", observe.OpSample, func(ctx context.Context, p *Pulse) error { _, err := p.Sample(ctx, obsCohort, 1); return err }},
		{"SampleWithRequest", observe.OpSample, func(ctx context.Context, p *Pulse) error {
			_, err := p.SampleWithRequest(ctx, &SampleRequest{Cohort: &types.Cohort{Filename: obsCohort}, N: 1})
			return err
		}},
		{"FilterToFile", observe.OpFilterToFile, func(ctx context.Context, p *Pulse) error {
			_, err := p.FilterToFile(ctx, obsCohort, "f1.pulse", "id > 1")
			return err
		}},
		{"FilterToFileBySetAndExpr", observe.OpFilterToFile, func(ctx context.Context, p *Pulse) error {
			_, err := p.FilterToFileBySetAndExpr(ctx, obsCohort, "f2.pulse", "", nil, "id > 1")
			return err
		}},
		{"FilterToFileWithRequest", observe.OpFilterToFile, func(ctx context.Context, p *Pulse) error {
			_, err := p.FilterToFileWithRequest(ctx, &FilterToFileRequest{SourcePath: obsCohort, Expression: "id > 1", OutputDir: "ftf"})
			return err
		}},
		{"Synth", observe.OpSynth, func(ctx context.Context, p *Pulse) error {
			_, err := p.Synth(ctx, &SynthSpec{}, "synth.pulse", SynthOptions{})
			return err
		}},
		{"SynthStream", observe.OpSynthStream, func(ctx context.Context, p *Pulse) error {
			sr, err := p.SynthStream(ctx, &SynthSpec{}, SynthOptions{})
			drainStream(sr)
			return err
		}},
		{"Profile", observe.OpProfile, func(ctx context.Context, p *Pulse) error {
			_, err := p.Profile(ctx, obsCohort, ProfileOptions{})
			return err
		}},
		{"Dedup", observe.OpDedup, func(ctx context.Context, p *Pulse) error {
			_, err := p.Dedup(ctx, obsCohort, DedupOptions{Out: "dedup.pulse"})
			return err
		}},
		{"WidenSetField", observe.OpWiden, func(ctx context.Context, p *Pulse) error {
			_, err := p.WidenSetField(ctx, obsCohort, "region", "set_u16")
			return err
		}},
		{"BuildIndex", observe.OpIndexBuild, func(ctx context.Context, p *Pulse) error {
			_, err := p.BuildIndex(ctx, obsCohort, []string{"id"})
			return err
		}},
		{"VerifyIndex", observe.OpIndexVerify, func(ctx context.Context, p *Pulse) error {
			_, err := p.VerifyIndex(ctx, obsCohort, []string{"id"})
			return err
		}},
		{"ListIndexes", observe.OpIndexList, func(ctx context.Context, p *Pulse) error { _, err := p.ListIndexes(ctx, obsCohort); return err }},
		{"DropIndex", observe.OpIndexDrop, func(ctx context.Context, p *Pulse) error { return p.DropIndex(ctx, obsCohort, []string{"id"}) }},
		{"CreateShardArchive", observe.OpShardCreate, func(ctx context.Context, p *Pulse) error {
			_, err := p.CreateShardArchive(ctx, "arch.pulse", []string{obsCohort})
			return err
		}},
		{"AddShard", observe.OpShardAdd, func(ctx context.Context, p *Pulse) error {
			_, err := p.AddShard(ctx, "arch.pulse", obsCohort)
			return err
		}},
		{"RemoveShard", observe.OpShardRemove, func(ctx context.Context, p *Pulse) error { return p.RemoveShard(ctx, "arch.pulse", "nope.pulse") }},
		{"ListShards", observe.OpShardList, func(ctx context.Context, p *Pulse) error { _, err := p.ListShards(ctx, "arch.pulse"); return err }},
		{"ExtractShard", observe.OpShardExtract, func(ctx context.Context, p *Pulse) error {
			rc, err := p.ExtractShard(ctx, "arch.pulse", "nope.pulse")
			if rc != nil {
				_ = rc.Close()
			}
			return err
		}},
		{"CompactShardArchive", observe.OpShardCompact, func(ctx context.Context, p *Pulse) error { return p.CompactShardArchive(ctx, "arch.pulse") }},
		{"VerifyShardArchive", observe.OpShardVerify, func(ctx context.Context, p *Pulse) error {
			_, err := p.VerifyShardArchive(ctx, "arch.pulse")
			return err
		}},
		{"RenderTemplate", observe.OpTemplateRender, func(ctx context.Context, p *Pulse) error { _, err := p.RenderTemplate("none", nil); return err }},
		{"RenderTemplateRequest", observe.OpTemplateRender, func(ctx context.Context, p *Pulse) error {
			_, err := p.RenderTemplateRequest("none", nil)
			return err
		}},
		{"ReloadTemplates", observe.OpTemplateReload, func(ctx context.Context, p *Pulse) error { return p.ReloadTemplates() }},
	}
}

// uninstrumentedMethods are the exported *Pulse methods that are NOT
// operations. The first block is the interview's excluded in-memory
// getter list (plus the other pure lookups beside it); the second block
// touches the filesystem but has no OperationKind yet — see the U20
// follow-ups. A new exported method must land in obsCalls or here, so a
// method can never silently skip instrumentation.
var uninstrumentedMethods = map[string]bool{
	"Ontology": true, "Skills": true, "SkillsForIntent": true, "Skill": true, "ListTemplates": true, "GetTemplate": true,
	"Limits": true, "LabelTables": true, "RangeTables": true, "ErrorLookup": true,
	"ErrorsByDomain": true, "ErrorsSearch": true, "ExamplesSearch": true, "ExamplesSearchWith": true, "ExampleGet": true,
	"FeatureProfile": true, "FeatureSetDigest": true, "Fs": true, "ResolveLabel": true,

	"CohortArtifacts": true, "NewCohortBuilder": true, "Imports": true, "ResolveImport": true,
	"ResolveCanonicalSchema": true, "ApplySeriesOverlays": true, "InvalidatedSidecars": true,
	"Watch": true, "WatchWithOptions": true, "WatchDir": true, "WatchDirWithOptions": true,
	"ExportReference": true,

	// Recommend is guidance, not an operation: unbound renders in-memory
	// guidance; bound reads a cohort's header, schema and sidecar and
	// runs its draft predicts in-process, firing no predict operation.
	// The OperationKind list stays as the U20 interview locked it.
	"Recommend": true,
	// Explain is guidance too: it describes a request from in-memory
	// guidance and, over a cohort, in-process predicts that fire no
	// predict operation.
	"Explain": true,
}

// TestObservedMethodsCoverSurface guards against a missed method: every
// exported *Pulse method is either instrumented (obsCalls) or listed as
// uninstrumented, and the instrumented set reaches every OperationKind.
func TestObservedMethodsCoverSurface(t *testing.T) {
	instrumented := map[string]bool{}
	kinds := map[observe.OperationKind]bool{}
	for _, c := range obsCalls() {
		if instrumented[c.method] {
			t.Errorf("method %s listed twice in obsCalls", c.method)
		}
		instrumented[c.method] = true
		kinds[c.kind] = true
	}
	pt := reflect.TypeOf(&Pulse{})
	var unlisted []string
	for i := 0; i < pt.NumMethod(); i++ {
		name := pt.Method(i).Name
		if instrumented[name] && uninstrumentedMethods[name] {
			t.Errorf("method %s is both instrumented and listed uninstrumented", name)
		}
		if !instrumented[name] && !uninstrumentedMethods[name] {
			unlisted = append(unlisted, name)
		}
	}
	sort.Strings(unlisted)
	if len(unlisted) > 0 {
		t.Errorf("exported *Pulse methods neither instrumented nor excluded: %v — route them through p.observe and add them to obsCalls", unlisted)
	}
	for _, k := range observe.AllOperationKinds() {
		if !kinds[k] {
			t.Errorf("OperationKind %q has no instrumented method in obsCalls", k)
		}
	}
}

// TestObserveFiresStartEndOncePerOperation calls every instrumented
// method and asserts exactly one top-level start and one top-level end,
// with the method's kind, and that the ctx returned by OnOperationStart
// is the ctx both the end hook and the operation's work saw. (Child
// operations — Compose slots, chain stages — are covered by
// TestObserveChildOperations.)
func TestObserveFiresStartEndOncePerOperation(t *testing.T) {
	rec := &hookRecorder{}
	p, _ := obsFixture(t, Options{Hooks: rec.hooks()})
	for _, c := range obsCalls() {
		t.Run(c.method, func(t *testing.T) {
			rec.take()
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("%s panicked: %v", c.method, r)
					}
				}()
				_ = c.call(context.Background(), p)
			}()
			evs := topEvents(rec.take())
			if len(evs) != 2 || !evs[0].start || evs[1].start {
				t.Fatalf("%s: want exactly [start, end], got %d events %+v", c.method, len(evs), evs)
			}
			start, end := evs[0], evs[1]
			if start.info.Kind != c.kind || end.info.Kind != c.kind {
				t.Errorf("%s: kind start=%q end=%q, want %q", c.method, start.info.Kind, end.info.Kind, c.kind)
			}
			if start.info != end.info {
				t.Errorf("%s: info changed between start and end: %+v vs %+v", c.method, start.info, end.info)
			}
			if start.info.ID == 0 || start.info.Scope != observe.ScopeTop {
				t.Errorf("%s: info ID/scope = %d/%q", c.method, start.info.ID, start.info.Scope)
			}
			if start.marker != nil {
				t.Errorf("%s: start saw a marker before setting one: %v", c.method, start.marker)
			}
			if end.marker != start.info.ID {
				t.Errorf("%s: end hook ctx marker = %v, want the start hook's %d", c.method, end.marker, start.info.ID)
			}
			if end.res.Code == "" {
				t.Errorf("%s: empty result code", c.method)
			}
		})
	}
}

// TestObserveWorkUsesStartHookContext: the operation's work runs under
// the ctx OnOperationStart returned.
func TestObserveWorkUsesStartHookContext(t *testing.T) {
	// The start hook hands back an already-cancelled ctx: if the work
	// runs under it, the operation fails with the cancellation; if it
	// ran under the caller's ctx it would succeed.
	hooks := &observe.Hooks{
		OnOperationStart: func(ctx context.Context, _ observe.OperationInfo) context.Context {
			c, cancel := context.WithCancel(ctx)
			cancel()
			return c
		},
	}
	p, _ := obsFixture(t, Options{Hooks: hooks})
	if _, err := p.Process(context.Background(), obsRequest()); !stderrors.Is(err, context.Canceled) {
		t.Fatalf("Process under a start-hook-cancelled ctx: err = %v, want context.Canceled", err)
	}
	if _, err := p.InspectBytes(context.Background(), nil, nil); !stderrors.Is(err, context.Canceled) {
		t.Fatalf("InspectBytes under a start-hook-cancelled ctx: err = %v, want context.Canceled", err)
	}

	plain, _ := obsFixture(t, Options{})
	if _, err := plain.Process(context.Background(), obsRequest()); err != nil {
		t.Fatalf("control Process failed: %v", err)
	}
}

// TestObserveEndResultCode checks duration and code for success, a
// coded error and an uncoded error.
func TestObserveEndResultCode(t *testing.T) {
	rec := &hookRecorder{}
	p, _ := obsFixture(t, Options{Hooks: rec.hooks()})
	ctx := context.Background()

	cases := []struct {
		name string
		call func() error
		code string
	}{
		{"success", func() error { _, err := p.Process(ctx, obsRequest()); return err }, observe.CodeOK},
		{"coded", func() error {
			_, err := p.WidenSetField(ctx, obsCohort, "region", "not_a_type")
			return err
		}, string(errors.ENCODING_TYPE_MISMATCH)},
		{"uncoded", func() error { _, err := p.FacetSchema(ctx, nil); return err }, observe.CodeUncoded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec.take()
			err := tc.call()
			if (tc.code == observe.CodeOK) != (err == nil) {
				t.Fatalf("unexpected err %v", err)
			}
			evs := rec.take()
			if len(evs) != 2 {
				t.Fatalf("want 2 events, got %d", len(evs))
			}
			res := evs[1].res
			if res.Duration <= 0 {
				t.Errorf("duration = %v, want > 0", res.Duration)
			}
			if res.Code != tc.code {
				t.Errorf("code = %q, want %q", res.Code, tc.code)
			}
		})
	}

	// A coded error found through a wrap chain still reports its code.
	wrapped := stderrors.Join(stderrors.New("ctx"), errors.NewCodedError(errors.SERVICE_RESOURCE, "x"))
	if got := operationCode(wrapped); got != string(errors.SERVICE_RESOURCE) {
		t.Errorf("operationCode(wrapped) = %q", got)
	}
}

// TestObserveInfoCarriesCohortAndHash checks the lazily-built info.
func TestObserveInfoCarriesCohortAndHash(t *testing.T) {
	rec := &hookRecorder{}
	p, _ := obsFixture(t, Options{Hooks: rec.hooks()})
	req := obsRequest()
	want := req.Hash()
	if _, err := p.Process(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	info := rec.take()[0].info
	if info.Cohort != obsCohort || info.RequestHash != want {
		t.Errorf("info cohort/hash = %q/%q, want %q/%q", info.Cohort, info.RequestHash, obsCohort, want)
	}
	if _, err := p.CountRecords(context.Background(), obsCohort); err != nil {
		t.Fatal(err)
	}
	info = rec.take()[0].info
	if info.Cohort != obsCohort || info.RequestHash != "" {
		t.Errorf("count_records info cohort/hash = %q/%q", info.Cohort, info.RequestHash)
	}
}

type countingHasher struct{ calls int }

func (c *countingHasher) Hash() string { c.calls++; return "h" }

// TestObserveOffSkipsHashAndHooks: with no Logger, Hooks or Metrics the
// helper runs the work only — the request hash is never computed and
// the observed path is not entered.
func TestObserveOffSkipsHashAndHooks(t *testing.T) {
	p, _ := obsFixture(t, Options{})
	h := &countingHasher{}
	ran := false
	_ = p.observe(context.Background(), opSpec{kind: observe.OpProcess, req: h}, func(context.Context) error {
		ran = true
		return nil
	})
	if !ran || h.calls != 0 || p.opSeq.Load() != 0 {
		t.Fatalf("off path: ran=%v hash calls=%d opSeq=%d, want true/0/0", ran, h.calls, p.opSeq.Load())
	}

	// On: the hash is computed once.
	on, _ := obsFixture(t, Options{Hooks: &observe.Hooks{}})
	_ = on.observe(context.Background(), opSpec{kind: observe.OpProcess, req: h}, func(context.Context) error { return nil })
	if h.calls != 1 || on.opSeq.Load() != 1 {
		t.Fatalf("on path: hash calls=%d opSeq=%d, want 1/1", h.calls, on.opSeq.Load())
	}
}
