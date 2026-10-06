package pulse_test

import (
	"bytes"
	"context"
	stderrors "errors"
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// newReturnInstance builds a second instance over the acceptance
// cohort's filesystem with the given options.
func newReturnInstance(t *testing.T, fs afero.Fs, opts pulse.Options) *pulse.Pulse {
	t.Helper()
	opts.FS = fs
	p, err := pulse.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

// minimalProfileWith is the published `minimal` example profile (it
// hides capability:matrices and every overlay host) carrying ret.
func minimalProfileWith(t *testing.T, ret *types.Return) *pulse.FeatureProfile {
	t.Helper()
	fp, err := pulse.ExampleFeatureProfile("minimal")
	if err != nil {
		t.Fatal(err)
	}
	fp.Return = ret
	return fp
}

// requirePredictDigest asserts predict reports the same resolved plan
// the runtime stamped (nil marker ⇔ identity / no plan).
func requirePredictDigest(t *testing.T, p *pulse.Pulse, req *types.Request, resp *types.Response) {
	t.Helper()
	pr, err := p.Predict(context.Background(), req)
	if err != nil {
		t.Fatalf("predict: %v", err)
	}
	if !pr.Valid {
		t.Fatalf("predict invalid: %+v", pr)
	}
	switch {
	case resp.Returned == nil:
		if pr.Return != nil && !pr.Return.Identity {
			t.Errorf("runtime unshaped but predict plan %+v is not identity", pr.Return)
		}
	case pr.Return == nil:
		t.Errorf("runtime marker %+v but predict reports no plan", resp.Returned)
	case pr.Return.Digest != resp.Returned.Digest || pr.Return.Preset != resp.Returned.Preset:
		t.Errorf("predict plan %s/%s != runtime marker %s/%s",
			pr.Return.Preset, pr.Return.Digest, resp.Returned.Preset, resp.Returned.Digest)
	}
}

// TestReturnDefaults_Precedence walks the four layers — request block >
// Options.DefaultReturn > FeatureProfile.Return > full — at runtime
// (Process) and in predict (same plan, same digest).
func TestReturnDefaults_Precedence(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	mk := returnCorpus(cohort)["grouped"]
	plain := newReturnInstance(t, fs, pulse.Options{})
	_, baseline := processJSON(t, plain, mk())

	standard := &types.Return{Preset: types.ReturnPresetStandard}
	minimal := &types.Return{Preset: types.ReturnPresetMinimal}

	cases := []struct {
		name       string
		opts       pulse.Options
		reqReturn  *types.Return
		wantPreset string   // "" ⇒ unshaped (byte-identical to baseline)
		wantKeys   []string // top-level keys when shaped
	}{
		{name: "no layer is full", opts: pulse.Options{}},
		{name: "profile default", opts: pulse.Options{FeatureProfile: minimalProfileWith(t, minimal)},
			wantPreset: "minimal", wantKeys: []string{"data", "returned"}},
		{name: "options default", opts: pulse.Options{DefaultReturn: standard},
			wantPreset: "standard", wantKeys: []string{"data", "metadata", "returned"}},
		{name: "options beats profile", opts: pulse.Options{DefaultReturn: standard, FeatureProfile: minimalProfileWith(t, minimal)},
			wantPreset: "standard", wantKeys: []string{"data", "metadata", "returned"}},
		{name: "request full replaces the default", opts: pulse.Options{DefaultReturn: standard, FeatureProfile: minimalProfileWith(t, minimal)},
			reqReturn: &types.Return{Preset: types.ReturnPresetFull}},
		{name: "request empty block replaces the default", opts: pulse.Options{DefaultReturn: minimal},
			reqReturn: &types.Return{}},
		{name: "include-only request over standard keeps an empty base", opts: pulse.Options{DefaultReturn: standard},
			reqReturn: &types.Return{Include: []string{"components"}}, wantPreset: "custom", wantKeys: []string{"components", "returned"}},
		{name: "precision-only request replaces the default with full", opts: pulse.Options{DefaultReturn: minimal},
			reqReturn: &types.Return{Precision: 6}, wantPreset: "full", wantKeys: []string{"components", "data", "metadata", "returned"}},
		{name: "precision-only default still marks", opts: pulse.Options{DefaultReturn: &types.Return{Precision: 6}},
			wantPreset: "full", wantKeys: []string{"components", "data", "metadata", "returned"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newReturnInstance(t, fs, tc.opts)
			req := mk()
			req.Return = tc.reqReturn
			resp, b := processJSON(t, p, req)
			if tc.wantPreset == "" {
				if !bytes.Equal(b, baseline) {
					t.Errorf("want byte-identical to the no-return baseline:\n got %s\nwant %s", b, baseline)
				}
			} else {
				if resp.Returned == nil || resp.Returned.Preset != tc.wantPreset {
					t.Fatalf("returned = %+v; want preset %s (%s)", resp.Returned, tc.wantPreset, b)
				}
				if got := topKeys(t, b); !slices.Equal(got, tc.wantKeys) {
					t.Errorf("top-level keys = %v; want %v (%s)", got, tc.wantKeys, b)
				}
			}
			req2 := mk()
			req2.Return = tc.reqReturn
			requirePredictDigest(t, p, req2, resp)
		})
	}
}

// TestReturnDefaults_DisableComponentsShorthand crosses the engine
// switch × request disable_components (nil / true / false) × the
// `return` layers, asserting the documented rule: components reach the
// wire iff the compute gate is open (request flag, else closed only by
// the engine switch — a request `return` block never re-opens it) AND
// the effective selection keeps them; the marker appears iff some
// layer supplies a block (DisableComponents alone never marks).
func TestReturnDefaults_DisableComponentsShorthand(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	mk := returnCorpus(cohort)["grouped"]
	tru, fal := true, false

	type layer struct {
		name       string
		defaultRet *types.Return
		reqRet     *types.Return
	}
	layers := []layer{
		{name: "no return anywhere"},
		{name: "request excludes components", reqRet: &types.Return{Exclude: []string{"components"}}},
		{name: "request includes components", reqRet: &types.Return{Include: []string{"components", "data"}}},
		{name: "default keeps components", defaultRet: &types.Return{Exclude: []string{"metadata"}}},
		{name: "default full", defaultRet: &types.Return{Preset: types.ReturnPresetFull}},
		{name: "default standard omits components", defaultRet: &types.Return{Preset: types.ReturnPresetStandard}},
	}
	for _, engine := range []bool{false, true} {
		for _, dc := range []*bool{nil, &tru, &fal} {
			for _, l := range layers {
				dcName := "nil"
				if dc != nil {
					dcName = map[bool]string{true: "true", false: "false"}[*dc]
				}
				name := l.name + "/engine=" + map[bool]string{true: "on", false: "off"}[engine] + "/request=" + dcName
				t.Run(name, func(t *testing.T) {
					p := newReturnInstance(t, fs, pulse.Options{DisableComponents: engine, DefaultReturn: l.defaultRet})
					req := mk()
					req.DisableComponents = dc
					req.Return = l.reqRet

					// The documented rule.
					// An engine DisableComponents sticks: only an
					// explicit request flag overrides it — a request
					// `return` block never re-opens the gate.
					gate := engine
					if dc != nil {
						gate = *dc
					}
					selected := true
					switch {
					case l.reqRet != nil && len(l.reqRet.Exclude) > 0:
						selected = false
					case l.reqRet == nil && l.defaultRet != nil && l.defaultRet.Preset == types.ReturnPresetStandard:
						selected = false
					}
					wantComponents := !gate && selected
					hasLayer := l.reqRet != nil || l.defaultRet != nil
					identityLayer := l.reqRet == nil && l.defaultRet != nil && l.defaultRet.Preset == types.ReturnPresetFull && !gate
					wantMarker := hasLayer && !identityLayer

					resp, b := processJSON(t, p, req)
					if got := bytes.Contains(b, []byte(`"components"`)); got != wantComponents {
						t.Errorf("components on wire = %v; want %v (%s)", got, wantComponents, b)
					}
					if got := resp.Components != nil; got != wantComponents {
						t.Errorf("components in Go = %v; want %v", got, wantComponents)
					}
					if got := resp.Returned != nil; got != wantMarker {
						t.Errorf("returned marker = %+v; want present=%v", resp.Returned, wantMarker)
					}
					req2 := mk()
					req2.DisableComponents = dc
					req2.Return = l.reqRet
					requirePredictDigest(t, p, req2, resp)
				})
			}
		}
	}
}

// TestReturnDefaults_DisableComponentsAloneByteIdentical: the engine
// switch and the request flag without any `return` layer keep today's
// wire form — no marker, components nil — byte for byte against an
// instance that never heard of `return`.
func TestReturnDefaults_DisableComponentsAloneByteIdentical(t *testing.T) {
	_, fs, cohort := acceptanceCohort(t)
	mk := returnCorpus(cohort)["grouped"]
	tru := true
	engineOff := newReturnInstance(t, fs, pulse.Options{DisableComponents: true})
	reqOff := mk()
	reqOff.DisableComponents = &tru
	_, viaRequest := processJSON(t, newReturnInstance(t, fs, pulse.Options{}), reqOff)
	_, viaEngine := processJSON(t, engineOff, mk())
	if !bytes.Equal(viaRequest, viaEngine) {
		t.Errorf("request vs engine DisableComponents differ:\n%s\n%s", viaRequest, viaEngine)
	}
	if bytes.Contains(viaEngine, []byte(`"returned"`)) || bytes.Contains(viaEngine, []byte(`"components"`)) {
		t.Errorf("DisableComponents alone marked or emitted components: %s", viaEngine)
	}
}

// TestReturnDefaults_OptionsRefusal: a bad Options.DefaultReturn fails
// New with the resolver's own code; a path the profile hides is
// PULSE_RETURN_PATH_UNKNOWN exactly like a nonexistent one.
func TestReturnDefaults_OptionsRefusal(t *testing.T) {
	cases := []struct {
		name string
		opts pulse.Options
		want perr.Code
	}{
		{"bad preset", pulse.Options{DefaultReturn: &types.Return{Preset: "tiny"}}, perr.PULSE_RETURN_INVALID},
		{"bad precision", pulse.Options{DefaultReturn: &types.Return{Precision: 18}}, perr.PULSE_RETURN_INVALID},
		{"unknown path", pulse.Options{DefaultReturn: &types.Return{Exclude: []string{"nope"}}}, perr.PULSE_RETURN_PATH_UNKNOWN},
		{"hidden path", pulse.Options{DefaultReturn: &types.Return{Include: []string{"matrices"}},
			FeatureProfile: minimalProfileWith(t, nil)}, perr.PULSE_RETURN_PATH_UNKNOWN},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.FS = afero.NewMemMapFs()
			_, err := pulse.New(tc.opts)
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != tc.want {
				t.Fatalf("New err = %v; want %s", err, tc.want)
			}
		})
	}
	// The same path on an unprofiled instance is accepted (the hidden
	// case is not vacuous).
	if _, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), DefaultReturn: &types.Return{Include: []string{"matrices"}}}); err != nil {
		t.Fatalf("unprofiled New: %v", err)
	}
}
