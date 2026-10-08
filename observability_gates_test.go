package pulse

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// The U20 non-negotiable observability gates (PRD FR-30):
//
//   - TestObservabilityDefaultsSilent — default Options write nothing and
//     never enter the observed path.
//   - TestObservabilityNoRowData — with everything on at Debug, no record
//     value, dictionary entry, filter literal, expression or error message
//     ever reaches any output.
//   - TestHookPanicRecovered — a panic in any hook leaves every operation's
//     outcome unchanged and is reported by hook + op only.
//   - TestObservabilityOffNoAllocs — the off path allocates exactly what it
//     did before U20.
//
// All four iterate obsCalls(), which TestObservedMethodsCoverSurface keeps
// complete, so an operation added later is covered without editing them.

// processOutput captures everything a process-wide sink could receive
// while fn runs: os.Stdout, os.Stderr, the default slog logger and the
// standard log package. Do not call t.Log / t.Run inside fn — the testing
// framework's own output would be captured.
type processOutput struct {
	stdout, stderr, stdlog string
	slogRecords            int
}

func captureProcessOutput(t *testing.T, fn func()) processOutput {
	t.Helper()
	drain := func(r *os.File, dst *string, wg *sync.WaitGroup) {
		defer wg.Done()
		b, _ := io.ReadAll(r)
		*dst = string(b)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var got processOutput
	var wg sync.WaitGroup
	wg.Add(2)
	go drain(outR, &got.stdout, &wg)
	go drain(errR, &got.stderr, &wg)

	slogSink := &captureHandler{}
	var logBuf bytes.Buffer
	prevOut, prevErr := os.Stdout, os.Stderr
	prevSlog := slog.Default()
	prevLogOut, prevLogFlags := log.Writer(), log.Flags()
	os.Stdout, os.Stderr = outW, errW
	slog.SetDefault(slog.New(slogSink))
	log.SetOutput(&logBuf)
	func() {
		defer func() {
			os.Stdout, os.Stderr = prevOut, prevErr
			slog.SetDefault(prevSlog)
			log.SetOutput(prevLogOut)
			log.SetFlags(prevLogFlags)
		}()
		fn()
	}()
	_ = outW.Close()
	_ = errW.Close()
	wg.Wait()
	_ = outR.Close()
	_ = errR.Close()
	got.stdlog = logBuf.String()
	got.slogRecords = len(slogSink.take())
	got.stdlog += slogSink.output()
	return got
}

// TestObservabilityDefaultsSilent: an instance built from default Options
// (no Logger, Hooks or Metrics) runs every operation kind writing nothing
// to stdout, stderr, the default slog logger or the log package, and never
// enters the observed path (no operation ID is ever issued, so no hook or
// instrument could have been reached).
//
// Falsified by making observing() return true: the observed path issues
// operation IDs.
func TestObservabilityDefaultsSilent(t *testing.T) {
	var (
		p       *Pulse
		newErr  error
		panics  []string
		calls   = obsCalls()
		fsys    = afero.NewMemMapFs()
		rowData = [][]string{{"1", "north", "10.5"}, {"2", "south", "20.25"}, {"3", "north", "7"}}
	)
	createTestPulseFile(t, fsys, obsCohort, []string{"id", "region", "amount"}, rowData)

	out := captureProcessOutput(t, func() {
		p, newErr = New(Options{FS: fsys})
		if newErr != nil {
			return
		}
		for _, c := range calls {
			func() {
				defer func() {
					if r := recover(); r != nil {
						panics = append(panics, fmt.Sprintf("%s: %v", c.method, r))
					}
				}()
				_ = c.call(context.Background(), p)
			}()
		}
	})
	if newErr != nil {
		t.Fatal(newErr)
	}
	if len(panics) > 0 {
		t.Fatalf("operations panicked: %v", panics)
	}
	if out.stdout != "" {
		t.Errorf("default Options wrote to stdout:\n%s", out.stdout)
	}
	if out.stderr != "" {
		t.Errorf("default Options wrote to stderr:\n%s", out.stderr)
	}
	if out.slogRecords != 0 || out.stdlog != "" {
		t.Errorf("default Options reached the default slog / log sink (%d slog records):\n%s", out.slogRecords, out.stdlog)
	}
	if n := p.opSeq.Load(); n != 0 {
		t.Errorf("default Options entered the observed path %d times across %d operations — the off path must be a single branch to the work", n, len(calls))
	}
}

// Sentinels for TestObservabilityNoRowData. Each is a value no
// identifier, count, timing, enum or code could ever spell, planted in
// one place row data or user input can live. None appears in a field
// name or cohort path (those are identifiers and are logged by design).
const (
	sentinelRecordStr    = "zqxRecordSentinel"   // a record value (also a dictionary entry)
	sentinelDictEntry    = "zqxDictSentinel"     // a dictionary-only entry
	sentinelRecordNum    = "8675309.4417"        // a numeric record value
	sentinelRecordID     = "77553311"            // an integer record value
	sentinelFilterLit    = "zqxFilterSentinel"   // a filter literal
	sentinelExpr         = "zqxExprSentinel"     // inside an expression
	sentinelErrEcho      = "zqxErrEchoSentinel"  // input whose error message echoes it
	sentinelLookupKey    = "zqxLookupSentinel"   // a lookup key value
	sentinelPanicPayload = "zqxPanicSentinel"    // a hook panic value
	sentinelTemplateVar  = "zqxTemplateSentinel" // a template variable value
)

func allSentinels() []string {
	return []string{
		sentinelRecordStr, sentinelDictEntry, sentinelRecordNum, sentinelRecordID,
		sentinelFilterLit, sentinelExpr, sentinelErrEcho, sentinelLookupKey,
		sentinelPanicPayload, sentinelTemplateVar,
	}
}

// sentinelCalls are operations that carry a sentinel in their INPUT —
// filter literals, expressions, lookup keys — including inputs that fail
// with an error whose message echoes the sentinel. echoes marks the ones
// whose error must echo it, so the test proves the leak is reachable.
func sentinelCalls() []struct {
	name   string
	echoes bool
	call   func(ctx context.Context, p *Pulse) error
} {
	withFilter := func(f *types.Filterer) *Request {
		r := obsRequest()
		r.Filterers = []*types.Filterer{f}
		return r
	}
	type sc = struct {
		name   string
		echoes bool
		call   func(ctx context.Context, p *Pulse) error
	}
	return []sc{
		{"process include literal", false, func(ctx context.Context, p *Pulse) error {
			_, err := p.Process(ctx, withFilter(&types.Filterer{Type: types.FILTER_INCLUDE, Field: "region", Values: []string{sentinelFilterLit, sentinelRecordStr}}))
			return err
		}},
		{"process expression literal", false, func(ctx context.Context, p *Pulse) error {
			_, err := p.Process(ctx, withFilter(&types.Filterer{Type: types.FILTER_EXPRESSION, Expression: `region == "` + sentinelExpr + `"`}))
			return err
		}},
		{"process broken expression", true, func(ctx context.Context, p *Pulse) error {
			_, err := p.Process(ctx, withFilter(&types.Filterer{Type: types.FILTER_EXPRESSION, Expression: sentinelErrEcho + ` ===( "`}))
			return err
		}},
		{"process grouped categories", false, func(ctx context.Context, p *Pulse) error {
			r := obsRequest()
			r.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
			_, err := p.Process(ctx, r)
			return err
		}},
		{"facet dictionary", false, func(ctx context.Context, p *Pulse) error { _, err := p.Facet(ctx, obsCohort, "region"); return err }},
		{"lookup key", false, func(ctx context.Context, p *Pulse) error {
			_, err := p.Lookup(ctx, &LookupRequest{Cohort: &types.Cohort{Filename: obsCohort}, Field: "region", Value: sentinelLookupKey})
			return err
		}},
		{"filter to file expression", false, func(ctx context.Context, p *Pulse) error {
			_, err := p.FilterToFile(ctx, obsCohort, "sent1.pulse", `region != "`+sentinelExpr+`"`)
			return err
		}},
		{"filter to file broken expression", true, func(ctx context.Context, p *Pulse) error {
			_, err := p.FilterToFile(ctx, obsCohort, "sent2.pulse", sentinelErrEcho+` ===( "`)
			return err
		}},
		{"widen to a bad type", true, func(ctx context.Context, p *Pulse) error {
			_, err := p.WidenSetField(ctx, obsCohort, "region", sentinelErrEcho)
			return err
		}},
		{"template variable", false, func(ctx context.Context, p *Pulse) error {
			_, err := p.RenderTemplate("none", map[string]any{"v": sentinelTemplateVar})
			return err
		}},
	}
}

// TestObservabilityNoRowData is the privacy gate (FR-16): with a Logger
// at Debug, Hooks and tight Limits all on, every operation kind plus a
// set of sentinel-carrying inputs runs over a cohort whose records and
// dictionary are sentinels. No sentinel may appear in ANY output — the
// Logger's rendered text and every attribute value, stdout, stderr, the
// default slog / log sinks. A hook that panics with a sentinel payload
// is in the mix, so the panic report is scanned too.
//
// Falsified by logging err.Error() on the failed-operation record.
func TestObservabilityNoRowData(t *testing.T) {
	variants := []struct {
		name string
		opts func(h *captureHandler) Options
	}{
		{"logger", func(h *captureHandler) Options { return Options{Logger: h.logger()} }},
		{"logger+hooks+limits", func(h *captureHandler) Options {
			return Options{
				Logger: h.logger(),
				Limits: Limits{MaxGroups: 1},
				Hooks: &observe.Hooks{
					OnOperationStart: func(ctx context.Context, _ observe.OperationInfo) context.Context { return ctx },
					OnOperationEnd: func(context.Context, observe.OperationInfo, observe.OperationResult) {
						panic(sentinelPanicPayload)
					},
					OnPhase: func(context.Context, observe.OperationInfo, observe.PhaseTiming) {
						panic(sentinelPanicPayload)
					},
				},
			}
		}},
	}
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			h := &captureHandler{}
			fsys := afero.NewMemMapFs()
			createTestPulseFile(t, fsys, obsCohort, []string{"id", "region", "amount"}, [][]string{
				{sentinelRecordID, sentinelRecordStr, sentinelRecordNum},
				{"2", sentinelDictEntry, "20.25"},
				{"3", sentinelRecordStr, "7"},
			})
			opts := v.opts(h)
			opts.FS = fsys

			var (
				panics   []string
				noEcho   []string
				ran      int
				sentCall = sentinelCalls()
			)
			out := captureProcessOutput(t, func() {
				p, err := New(opts)
				if err != nil {
					panics = append(panics, "New: "+err.Error())
					return
				}
				guard := func(name string, fn func() error) {
					defer func() {
						if r := recover(); r != nil {
							panics = append(panics, fmt.Sprintf("%s: %v", name, r))
						}
					}()
					ran++
					_ = fn()
				}
				ctx := context.Background()
				for _, c := range obsCalls() {
					guard(c.method, func() error { return c.call(ctx, p) })
				}
				for _, c := range sentCall {
					guard(c.name, func() error {
						err := c.call(ctx, p)
						if c.echoes && (err == nil || !strings.Contains(err.Error(), sentinelErrEcho)) {
							noEcho = append(noEcho, fmt.Sprintf("%s: %v", c.name, err))
						}
						return err
					})
				}
			})
			if len(panics) > 0 {
				t.Fatalf("operations panicked or New failed: %v", panics)
			}
			if len(noEcho) > 0 {
				t.Errorf("precondition: these inputs must fail with an error echoing the sentinel, or the gate proves nothing: %v", noEcho)
			}
			recs := h.take()
			// Today the logger writes failures, warnings and lifecycle
			// records; E2-S1's Debug plan records join the same corpus
			// with no edit here. Insist the failure path was exercised,
			// or the error-echo sentinels prove nothing.
			if failed := len(only(recs, logMsgFailed)); failed < len(sentCall)/2 {
				t.Fatalf("captured only %d failure records over %d operations — the logger is not wired, so the scan proves nothing", failed, ran)
			}

			var corpus strings.Builder
			corpus.WriteString(h.output())
			for _, r := range recs {
				corpus.WriteString(r.msg)
				for k, a := range r.attrs {
					fmt.Fprintf(&corpus, " %s=%v", k, a)
				}
				corpus.WriteByte('\n')
			}
			sinks := map[string]string{
				"logger":   corpus.String(),
				"stdout":   out.stdout,
				"stderr":   out.stderr,
				"std sink": out.stdlog,
			}
			names := make([]string, 0, len(sinks))
			for n := range sinks {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, sink := range names {
				text := sinks[sink]
				for _, s := range allSentinels() {
					if i := strings.Index(text, s); i >= 0 {
						lo, hi := max(0, i-120), min(len(text), i+len(s)+40)
						t.Errorf("%s leaked sentinel %q: …%s…", sink, s, text[lo:hi])
					}
				}
			}
		})
	}
}

// panicHooks builds a Hooks whose named field panics with payload and
// records that it fired. Every func field of observe.Hooks must have a
// builder here — TestHookPanicRecovered checks it by reflection, so a
// hook added later cannot dodge the gate.
var panicHooks = map[string]struct {
	field string
	build func(payload string, fired *bool) *observe.Hooks
}{
	hookStart: {"OnOperationStart", func(payload string, fired *bool) *observe.Hooks {
		return &observe.Hooks{OnOperationStart: func(context.Context, observe.OperationInfo) context.Context {
			*fired = true
			panic(payload)
		}}
	}},
	hookEnd: {"OnOperationEnd", func(payload string, fired *bool) *observe.Hooks {
		return &observe.Hooks{OnOperationEnd: func(context.Context, observe.OperationInfo, observe.OperationResult) {
			*fired = true
			panic(payload)
		}}
	}},
	"phase": {"OnPhase", func(payload string, fired *bool) *observe.Hooks {
		return &observe.Hooks{OnPhase: func(context.Context, observe.OperationInfo, observe.PhaseTiming) {
			*fired = true
			panic(payload)
		}}
	}},
}

const logMsgHookPanic = "pulse: observability hook panicked"

// TestHookPanicRecovered (FR-18): for each hook, a hook that panics on
// every call leaves every operation kind's outcome (its result code)
// identical to an unhooked twin run over the same fixture; whenever the
// hook fired, exactly one Warn names the hook and the op — never the
// panic value. With no Logger the panic is still contained.
//
// Falsified by removing the defer p.recoverHook in hookEnd.
//
// OnPhase is wired in E2-S2; until then it never fires, so its "logged"
// assertion engages automatically once it does.
func TestHookPanicRecovered(t *testing.T) {
	ht := reflect.TypeOf(observe.Hooks{})
	covered := map[string]bool{}
	for _, b := range panicHooks {
		covered[b.field] = true
	}
	for i := 0; i < ht.NumField(); i++ {
		if f := ht.Field(i); f.Type.Kind() == reflect.Func && !covered[f.Name] {
			t.Errorf("observe.Hooks.%s has no panicHooks row — add one so its panic recovery is gated", f.Name)
		}
	}

	// Baseline: the code each call returns with no hooks, on a fresh
	// fixture, in the same order.
	base, _ := obsFixture(t, Options{})
	var wantCodes []string
	for _, c := range obsCalls() {
		wantCodes = append(wantCodes, operationCode(c.call(context.Background(), base)))
	}

	hookNames := make([]string, 0, len(panicHooks))
	for n := range panicHooks {
		hookNames = append(hookNames, n)
	}
	sort.Strings(hookNames)
	for _, hook := range hookNames {
		for _, withLogger := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/logger=%v", hook, withLogger), func(t *testing.T) {
				fired := false
				h := &captureHandler{}
				opts := Options{Hooks: panicHooks[hook].build(sentinelPanicPayload, &fired)}
				if withLogger {
					opts.Logger = h.logger()
				}
				p, _ := obsFixture(t, opts)
				h.take()
				firedAny := false
				for i, c := range obsCalls() {
					fired = false
					var err error
					var panicked any
					func() {
						defer func() { panicked = recover() }()
						err = c.call(context.Background(), p)
					}()
					if panicked != nil {
						t.Errorf("%s: %s hook panic escaped the operation: %v", c.method, hook, panicked)
						continue
					}
					if got := operationCode(err); got != wantCodes[i] {
						t.Errorf("%s: code %q under a panicking %s hook, want %q (unhooked)", c.method, got, hook, wantCodes[i])
					}
					recs := only(h.take(), logMsgHookPanic)
					if !withLogger || !fired {
						continue
					}
					firedAny = true
					if len(recs) == 0 {
						t.Errorf("%s: %s hook panicked but nothing was logged", c.method, hook)
					}
					for _, r := range recs {
						if r.level != slog.LevelWarn || r.attrs["hook"] != hook || r.attrs[logKeyOp] != string(c.kind) {
							t.Errorf("%s: panic record = %+v, want Warn hook=%s op=%s", c.method, r, hook, c.kind)
						}
					}
				}
				if withLogger && strings.Contains(h.output(), sentinelPanicPayload) {
					t.Errorf("panic value leaked into the log")
				}
				if hook != "phase" && !firedAny && withLogger {
					t.Errorf("%s hook never fired — the gate is vacuous", hook)
				}
			})
		}
	}

	// The data path is untouched, not just the code.
	plain, _ := obsFixture(t, Options{})
	want, err := plain.Process(context.Background(), obsRequest())
	if err != nil {
		t.Fatal(err)
	}
	var fired bool
	p, _ := obsFixture(t, Options{Hooks: panicHooks[hookEnd].build("x", &fired)})
	got, err := p.Process(context.Background(), obsRequest())
	if err != nil || !reflect.DeepEqual(got.Data, want.Data) {
		t.Errorf("Process under a panicking end hook: err=%v, data changed=%v", err, err == nil && !reflect.DeepEqual(got.Data, want.Data))
	}
}

// obsOffAllocBaseline pins testing.AllocsPerRun for small operations on
// an instance with default Options (nil Logger, Hooks, Metrics),
// MEASURED ON PRE-U20 main (commit 406c6b7c, go1.26.1 and go1.27.1,
// arm64 and amd64 — identical). U20 promised the off path is
// allocation-identical; this pin is the proof.
var obsOffAllocBaseline = map[string]float64{
	"process":       164,
	"count_records": 45,
	"predict":       236,
	"inspect":       66,
}

// TestObservabilityOffNoAllocs (FR-30) guards the observability OFF
// path: the observe helper itself allocates nothing, and small
// operations allocate exactly the pre-U20 baseline.
//
// Falsified by building the OperationInfo (or computing the request
// hash) before the observing() branch in observe/observed.
func TestObservabilityOffNoAllocs(t *testing.T) {
	t.Run("helper", func(t *testing.T) {
		p, _ := obsFixture(t, Options{})
		req := obsRequest()
		ctx := context.Background()
		allocs := testing.AllocsPerRun(100, func() {
			_, _ = observed(p, ctx, requestOp(observe.OpProcess, req), func(context.Context) (int, error) { return 1, nil })
			_ = p.observe(ctx, requestOp(observe.OpProcess, req), func(context.Context) error { return nil })
		})
		if allocs != 0 {
			t.Fatalf("off-path observe/observed allocated %v per run, want 0 — keep every observability cost behind p.observing()", allocs)
		}
	})

	p, _ := obsFixture(t, Options{})
	ctx := context.Background()
	req := obsRequest()
	ops := map[string]func() error{
		"process":       func() error { _, err := p.Process(ctx, req); return err },
		"count_records": func() error { _, err := p.CountRecords(ctx, obsCohort); return err },
		"predict":       func() error { _, err := p.Predict(ctx, req); return err },
		"inspect":       func() error { _, err := p.Inspect(ctx, obsCohort); return err },
	}
	for name, want := range obsOffAllocBaseline {
		t.Run(name, func(t *testing.T) {
			op := ops[name]
			if op == nil {
				t.Fatalf("no operation for baseline %q", name)
			}
			if err := op(); err != nil {
				t.Fatal(err)
			}
			got := testing.AllocsPerRun(200, func() { _ = op() })
			if raceEnabled {
				// The race runtime adds its own allocations; the pin is a
				// plain-build figure (CI's make test). The helper subtest
				// above still holds under -race.
				t.Logf("-race build: %s allocated %v (pin %v applies to non-race builds only)", name, got, want)
				return
			}
			if got != want {
				t.Fatalf(`%s with default Options allocated %v per run; pinned pre-U20 baseline is %v.

This gate guards the observability OFF path: with no Logger, Hooks or
Metrics, an operation must allocate exactly what it did before U20.

To tell which kind of change moved it, run on your merge-base (without
your change):
    go test -count=1 -run 'TestObservabilityOffNoAllocs' -v .
  - Same new count there: an unrelated change moved the baseline. Re-pin
    obsOffAllocBaseline[%q] = %v in observability_gates_test.go, and
    record the commit you measured on in its comment.
  - Only your change moves it: your change put work on the off path.
    Move it behind p.observing() (see observability.go) instead of
    re-pinning.`, name, got, want, name, got)
			}
		})
	}
	if len(ops) != len(obsOffAllocBaseline) {
		t.Errorf("ops (%d) and obsOffAllocBaseline (%d) disagree", len(ops), len(obsOffAllocBaseline))
	}
}
