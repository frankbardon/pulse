package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// encoderIndent is the reference the streaming indenter mirrors: what
// writeJSON writes for an already-marshalled body.
func encoderIndent(t *testing.T, src []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(json.RawMessage(src)); err != nil {
		t.Fatalf("reference encode %s: %v", src, err)
	}
	return buf.Bytes()
}

// TestJSONIndenter_MatchesEncoder: the streaming indenter writes every
// input exactly as json.Encoder{SetIndent("", "  ")} does — empty
// containers, escapes, HTML-unsafe and U+2028/U+2029 runes inside
// strings, insignificant whitespace — whether the input arrives whole
// or one byte at a time.
func TestJSONIndenter_MatchesEncoder(t *testing.T) {
	t.Parallel()

	inputs := []string{
		`{}`, `[]`, `{"a":[]}`, `{"a":{},"b":[{}],"c":[[],[1,2]]}`,
		`{"s":"a\"b\\c","n":-1.5e-7,"t":true,"f":false,"z":null}`,
		`{"h":"<b>&amp;</b>","ls":"x` + "\u2028" + `y` + "\u2029" + `z","e":"\u00e2\u0080"}`,
		"{ \"sp\" : [ 1 , 2 ] ,\n\t\"k\":\"v \" }",
		`{"\u00e2":"` + "\xe2\x82\xac\xe2\x80\x93" + `"}`,
		`[{"data":[{"x":1},{"x":"]}["}]}]`,
	}
	for _, in := range inputs {
		want := encoderIndent(t, []byte(in))
		for _, chunked := range []bool{false, true} {
			var buf bytes.Buffer
			ind := newTestIndenter(&buf)
			if chunked {
				for i := range len(in) {
					ind.write([]byte{in[i]})
				}
			} else {
				ind.write([]byte(in))
			}
			ind.emit('\n')
			if err := ind.flush(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(buf.Bytes(), want) {
				t.Errorf("input %q chunked=%v:\n got %q\nwant %q", in, chunked, buf.Bytes(), want)
			}
		}
	}
}

// testIndenter wraps the streaming indenter over w for a test.
type testIndenter struct{ *jsonIndenter }

func newTestIndenter(w *bytes.Buffer) testIndenter {
	return testIndenter{&jsonIndenter{w: bufio.NewWriter(w)}}
}

func (ti testIndenter) flush() error {
	if ti.err != nil {
		return ti.err
	}
	return ti.w.Flush()
}

// streamArm is one envelope the digest test writes both ways.
type streamArm struct {
	name string
	env  *descriptor.Envelope
}

// streamFixture seeds the cohort the digest arms run against.
func streamFixture(t *testing.T) string {
	t.Helper()
	dir := withTempDataDir(t)
	var b strings.Builder
	b.WriteString("cat,num,den,w,tag\n")
	for i := range 24 {
		den := i % 3
		if i%2 == 0 {
			den = 0
		}
		fmt.Fprintf(&b, "%s,%d,%d,%d,%s\n", []string{"a", "b", "c"}[i%3], i, den, 1+i%4, []string{"x<y", "p&q", "plain"}[i%3])
	}
	return seedCohortViaImport(t, dir, "stream.csv", b.String())
}

func decodeInto[T any](t *testing.T, body string) *T {
	t.Helper()
	v := new(T)
	if err := json.Unmarshal([]byte(body), v); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return v
}

// streamArms runs every --json arm the leaves stream and returns each
// envelope exactly as api.go builds it.
func streamArms(t *testing.T, cohort string) []streamArm {
	t.Helper()
	ctx := context.Background()
	c := `"cohort":{"filename":"` + jsonEsc(cohort) + `"}`
	grp := `"groups":[{"type":"GROUP_CATEGORY","field":"cat"}]`
	sum := `"aggregations":[{"type":"AGG_SUM","field":"num","label":"total"},{"type":"AGG_AVERAGE","field":"num","label":"mean"}]`
	ratio := `"aggregations":[{"type":"AGG_RATIO","field":"num","label":"r","params":{"numerator_field":"num","denominator_field":"den"}}]`
	leak := `"features":[{"type":"FEAT_TARGET_ENCODE","field":"cat","params":{"target":"num"}}],"aggregations":[{"type":"AGG_COUNT","field":"num"}]`

	var arms []streamArm
	process := func(name, body string, echo bool) {
		p, err := newPulseOpts(ctx, pulse.Options{EchoRequest: echo})
		if err != nil {
			t.Fatal(err)
		}
		req := decodeInto[types.Request](t, body)
		resp, err := p.Process(ctx, req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		env := withRuntimeWarnings(descriptor.NewEnvelope(resp), resp)
		if echo {
			env.WithRequest(req)
		}
		arms = append(arms, streamArm{name, env})
		// The error envelope a leaf builds around a partial result.
		if name == "process/grouped" {
			err := perrors.NewCodedErrorWithDetails(perrors.PROCESSING_CONFIG, "boom <&>", map[string]any{"at": 1})
			arms = append(arms, streamArm{"process/error", codedErrorEnvelope(resp, "PROCESS_ERROR", err)})
		}
	}
	process("process/plain", `{`+c+`,`+sum+`}`, false)
	process("process/grouped", `{`+c+`,`+grp+`,`+sum+`,"labels":[]}`, false)
	process("process/weighted", `{`+c+`,`+grp+`,`+sum+`,"weight":{"field":"w"}}`, false)
	process("process/return", `{`+c+`,`+grp+`,`+sum+`,"return":{"exclude":["metadata"],"precision":3}}`, false)
	process("process/non-finite", `{`+c+`,`+grp+`,`+ratio+`}`, false)
	process("process/warnings", `{`+c+`,`+leak+`}`, false)
	process("process/echo-request", `{`+c+`,`+grp+`,`+sum+`}`, true)
	process("process/rows-carry-strings", `{`+c+`,"groups":[{"type":"GROUP_CATEGORY","field":"tag"}],`+sum+`}`, false)

	chain := func(name, body string, echo bool) {
		p, err := newPulseOpts(ctx, pulse.Options{EchoRequest: echo})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := p.ProcessChain(ctx, decodeInto[types.ChainRequest](t, body))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var echoed any
		if echo && resp.NormalizedRequest != nil {
			echoed = resp.NormalizedRequest
		}
		arms = append(arms, streamArm{name, withRuntimeWarnings(descriptor.NewEnvelopeWithRequest(resp, echoed), resp)})
	}
	stage := func(field, label string) string {
		return `{"request":{` + grp + `,"aggregations":[{"type":"AGG_SUM","field":"` + field + `","label":"` + label + `"}]}}`
	}
	stages := `"stages":[` + stage("num", "v") + `,` + stage("v", "w") + `,{"request":{` + grp + `,"aggregations":[{"type":"AGG_COUNT","field":"w","label":"c"}],"return":{"precision":2}}}]`
	chain("chain/multi-stage", `{`+c+`,`+stages+`}`, false)
	chain("chain/echo-request", `{`+c+`,`+stages+`}`, true)
	chain("chain/warnings", `{`+c+`,"stages":[{"request":{"vectors":[{"name":"v","fields":["num","den"]}],"aggregations":[{"type":"AGG_COUNT","field":"num"}]}}]}`, false)

	compose := func(name, body string, echo bool, workers int) {
		p, err := newPulseOpts(ctx, pulse.Options{EchoRequest: echo})
		if err != nil {
			t.Fatal(err)
		}
		req := decodeInto[types.ComposedRequest](t, body)
		var resp *types.ComposedResponse
		if workers != 1 {
			resp, err = p.ComposeParallel(ctx, req, pulse.ComposeOptions{MaxWorkers: workers, FailFast: true})
		} else {
			resp, err = p.Compose(ctx, req)
		}
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var echoed any
		if echo && resp.NormalizedRequest != nil {
			echoed = resp.NormalizedRequest
		}
		arms = append(arms, streamArm{name, withRuntimeWarnings(descriptor.NewEnvelopeWithRequest(resp, echoed), resp)})
	}
	slot := func(label, extra string) string {
		return `{"label":"` + label + `",` + c + `,` + grp + `,"aggregations":[{"type":"AGG_AVERAGE","field":"num","label":"m"}]` + extra + `}`
	}
	overlay := `"overlays":[{"name":"o","kind":"OVERLAY_INDEX_VS_REF","scope":"group","reference":"a","targets":["b"]}]`
	compose("compose/overlays", `{"requests":[`+slot("a", "")+`,`+slot("b", `,"weight":{"field":"w"}`)+`],`+overlay+`}`, false, 1)
	compose("compose/return", `{"requests":[`+slot("a", `,"return":{"exclude":["metadata"],"precision":4}`)+`,`+slot("b", "")+`],"return":{"preset":"minimal"}}`, false, 1)
	compose("compose/warnings-parallel", `{"requests":[{`+c+`,`+sum+`},{`+c+`,`+leak+`}]}`, false, 2)
	compose("compose/echo-request", `{"requests":[`+slot("a", "")+`,{`+c+`,`+ratio+`}]}`, true, 1)
	return arms
}

// TestWriteEnvelopeStreamed_ByteIdentical: on every --json arm the
// process / process-chain / compose leaves write, the row-streamed
// envelope is byte for byte (digest-equal) what writeJSON writes from
// the whole envelope — and it really streamed: every row-bearing
// Response was spliced, never the writeJSON fallback.
func TestWriteEnvelopeStreamed_ByteIdentical(t *testing.T) {
	// serial: t.Setenv mutates the process environment

	cohort := streamFixture(t)
	for _, arm := range streamArms(t, cohort) {
		t.Run(arm.name, func(t *testing.T) {
			_, targets := rowSkeleton(arm.env.Data)
			if len(targets) == 0 {
				t.Fatal("arm carries no rows: the splice is not exercised")
			}
			var want, got bytes.Buffer
			wantErr := writeJSON(&want, arm.env)
			gotErr := writeEnvelopeStreamed(&got, arm.env)
			if !stderrors.Is(gotErr, wantErr) && gotErr != wantErr {
				t.Fatalf("error = %v, want %v", gotErr, wantErr)
			}
			if sha256.Sum256(got.Bytes()) != sha256.Sum256(want.Bytes()) {
				t.Fatalf("digest differs at byte %d\n got: %s\nwant: %s", firstDiff(got.Bytes(), want.Bytes()),
					excerpt(got.Bytes(), firstDiff(got.Bytes(), want.Bytes())), excerpt(want.Bytes(), firstDiff(got.Bytes(), want.Bytes())))
			}
			// The arm carries the feature it is named for.
			for _, sub := range armFeatures[arm.name] {
				if !bytes.Contains(want.Bytes(), []byte(sub)) {
					t.Errorf("arm output lacks %q: the arm does not exercise its feature", sub)
				}
			}
			// It spliced: the skeleton the writer used carries the
			// sentinel at every target, so no arm passed via the
			// whole-envelope fallback.
			skelData, _ := rowSkeleton(arm.env.Data)
			skelEnv := *arm.env
			skelEnv.Data = skelData
			skel, err := types.MarshalFinite(&skelEnv)
			if err != nil {
				t.Fatal(err)
			}
			if n, _ := spliceRows(nil, skel, targets); n != len(targets) {
				t.Fatalf("spliced %d of %d row arrays: the arm fell back to writeJSON", n, len(targets))
			}
		})
	}
}

// armFeatures pins, per arm, the bytes proving the arm exercises the
// shape it is named for.
var armFeatures = map[string][]string{
	"process/grouped":            {`"cat": "b"`},
	"process/error":              {`"code": "PROCESSING_CONFIG"`, `boom \u003c\u0026\u003e`},
	"process/weighted":           {`"sum_weights"`},
	"process/return":             {`"returned"`},
	"process/non-finite":         {`"r": null`},
	"process/warnings":           {`"PULSE_FEAT_TARGET_LEAKAGE_RISK"`},
	"process/echo-request":       {`"request": {`},
	"process/rows-carry-strings": {`x\u003cy`, `p\u0026q`},
	"chain/multi-stage":          {`"stages"`, `"final"`, `"w"`, `"c"`},
	"chain/echo-request":         {`"request": {`},
	"chain/warnings":             {`"PULSE_VECTOR_UNREFERENCED"`, `"stage": 0`},
	"compose/overlays":           {`"OVERLAY_INDEX_VS_REF"`},
	"compose/return":             {`"minimal"`},
	"compose/warnings-parallel":  {`"request": 1`},
	"compose/echo-request":       {`"request": {`, `null`},
}

func firstDiff(a, b []byte) int {
	n := min(len(a), len(b))
	for i := range n {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

func excerpt(b []byte, at int) string {
	lo, hi := max(at-120, 0), min(at+120, len(b))
	return string(b[lo:hi])
}

// chunkRecorder records the largest single Write a leaf makes.
type chunkRecorder struct {
	bytes.Buffer
	maxWrite int
}

func (c *chunkRecorder) Write(p []byte) (int, error) {
	c.maxWrite = max(c.maxWrite, len(p))
	return c.Buffer.Write(p)
}

// TestAPI_JSONLeavesStreamRows: `pulse api process`, `process-chain`
// and `compose` with --json write their envelope through the row
// streamer — the output arrives in bounded chunks, never one Write of
// the whole document — and it is still the envelope writeJSON writes.
func TestAPI_JSONLeavesStreamRows(t *testing.T) {
	// serial: t.Setenv mutates the process environment

	dir := withTempDataDir(t)
	var b strings.Builder
	b.WriteString("id,n\n")
	// 500 groups, each id repeated so the import keeps it categorical.
	for i := range 2000 {
		fmt.Fprintf(&b, "id%05d,%d\n", i/4, i)
	}
	cohort := seedCohortViaImport(t, dir, "wide.csv", b.String())
	c := `"cohort":{"filename":"` + jsonEsc(cohort) + `"}`
	body := `"groups":[{"type":"GROUP_CATEGORY","field":"id"}],"aggregations":[{"type":"AGG_SUM","field":"n","label":"s"},{"type":"AGG_AVERAGE","field":"n","label":"m"},{"type":"AGG_MIN","field":"n","label":"lo"},{"type":"AGG_MAX","field":"n","label":"hi"}]`
	files := map[string]string{
		"process":       `{` + c + `,` + body + `}`,
		"process-chain": `{` + c + `,"stages":[{"request":{` + body + `}}]}`,
		"compose":       `{"requests":[{` + c + `,` + body + `}]}`,
	}
	for leaf, req := range files {
		t.Run(leaf, func(t *testing.T) {
			path := writeInclude(t, dir, leaf+".json", req)
			rec := &chunkRecorder{}
			root := APICommand()
			root.Writer = rec
			if err := root.Run(context.Background(), []string{"api", leaf, "--json", "-r", path}); err != nil {
				t.Fatalf("run: %v\n%s", err, rec.String())
			}
			if rec.Len() < 2*streamChunk {
				t.Fatalf("output %d bytes: too small to prove chunking", rec.Len())
			}
			if rec.maxWrite > streamChunk {
				t.Errorf("largest Write %d bytes of %d: the envelope was not streamed", rec.maxWrite, rec.Len())
			}
			var env descriptor.Envelope
			if err := json.Unmarshal(rec.Bytes(), &env); err != nil || len(env.Errors) != 0 {
				t.Fatalf("envelope: %v %v", err, env.Errors)
			}
		})
	}
}
