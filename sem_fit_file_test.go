package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// sem_fit_file_test.go pins the saved SEM fit file (U59 E3-S3) at the
// facade: a SEM fit's `save` writes a .pulse-sem.json (atomically, the
// overwrite warned, a write fault PULSE_MODEL_WRITE_FAILED) holding the
// fit at full precision; a later request's `models` entry loads it by
// its suffix and ATTR_SEM_SCORE scores from it bit-equal to the
// in-compose bound fit — a plain Process, a Compose slot and a chain
// stage alike, predict ≡ runtime on every load refusal.

const semFitPath = "fits/brand" + model.SEMSuffix

// semSaveCompose fits brand under save and scores slot from it.
func semSaveCompose(save string, slot *types.Request) *pulse.ComposedRequest {
	return &pulse.ComposedRequest{
		Fits:     []types.FitSpec{{Name: "brand", Request: semFitRequest(semFitKnobs()), Save: save}},
		Requests: []*types.Request{slot},
	}
}

// loaded is r loading the saved fit as brand.
func loaded(r *types.Request, path string) *types.Request {
	r.Models = []types.ModelRef{{Name: "brand", Path: path}}
	return r
}

// TestSEMFit_SaveThenLoadScoresBitEqual: the saved file holds the
// fit's every figure (its Result equals the compose `fits` entry), and
// scoring from it — Process and a Compose slot, ungrouped and by wave —
// gives the in-compose bound slot's figures and warnings exactly
// (serial: the sum included); predict streams the loaded slot. A second
// save overwrites with PULSE_MODEL_OVERWRITTEN (details.fit).
func TestSEMFit_SaveThenLoadScoresBitEqual(t *testing.T) {
	t.Parallel()

	p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	ctx := context.Background()
	for _, grouped := range []bool{false, true} {
		shape := func(r *types.Request) *types.Request {
			if grouped {
				return byWave(r)
			}
			return r
		}
		out, err := p.Compose(ctx, semSaveCompose(semFitPath, shape(semScoreSlotReq(boundParams(), true))))
		if err != nil {
			t.Fatalf("grouped=%v: %v", grouped, err)
		}
		overwrote := false
		for _, w := range out.Warnings {
			if w.Code == string(errors.PULSE_MODEL_OVERWRITTEN) && w.Details["fit"] == 0 && w.Details["path"] == semFitPath {
				overwrote = true
			}
		}
		if overwrote != grouped { // the second pass replaces the first's file
			t.Fatalf("grouped=%v: overwrite warning %v in %+v", grouped, overwrote, out.Warnings)
		}
		data, err := afero.ReadFile(fsys, semFitPath)
		if err != nil {
			t.Fatalf("no SEM fit file written: %v", err)
		}
		file, err := model.DecodeSEMFile(semFitPath, data)
		if err != nil {
			t.Fatal(err)
		}
		if g, w := dataJSON(t, file.SEMFit("brand").Result()), dataJSON(t, out.Fits["brand"].SEM); g != w {
			t.Fatalf("the file's fit differs from the compose entry\n got %s\nwant %s", g, w)
		}
		want := dataJSON(t, out.Responses[0].Data) + dataJSON(t, out.Responses[0].Warnings)
		if !strings.Contains(want, `"sum":`) {
			t.Fatal("vacuous: no figures")
		}

		resp, err := p.Process(ctx, loaded(shape(semScoreSlotReq(boundParams(), true)), semFitPath))
		if err != nil {
			t.Fatal(err)
		}
		if got := dataJSON(t, resp.Data) + dataJSON(t, resp.Warnings); got != want {
			t.Fatalf("grouped=%v: loaded scores differ from the bound fit's\n got %s\nwant %s", grouped, got, want)
		}
		slot, err := p.Compose(ctx, &pulse.ComposedRequest{Requests: []*types.Request{loaded(shape(semScoreSlotReq(boundParams(), true)), semFitPath)}})
		if err != nil {
			t.Fatal(err)
		}
		if got := dataJSON(t, slot.Responses[0].Data) + dataJSON(t, slot.Responses[0].Warnings); got != want {
			t.Fatalf("grouped=%v: loaded compose slot differs from the bound fit's", grouped)
		}
		env, err := p.PredictEnvelope(ctx, loaded(shape(semScoreSlotReq(boundParams(), false)), semFitPath))
		if err != nil || len(env.Errors) != 0 {
			t.Fatalf("predict: %v %+v", err, env)
		}
		if !env.Data.(*descriptor.PredictResult).Streamable {
			t.Fatalf("grouped=%v: predict calls the loaded slot buffered", grouped)
		}
	}
}

// TestSEMFit_LoadedChainStage: a chain stage loading the saved fit
// scores row-local, so the chain runs (predict and runtime agree), and
// its stage figures are the plain request's.
func TestSEMFit_LoadedChainStage(t *testing.T) {
	t.Parallel()

	p, _ := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	ctx := context.Background()
	if _, err := p.Compose(ctx, semSaveCompose(semFitPath, semScoreSlotReq(boundParams(), false))); err != nil {
		t.Fatal(err)
	}
	s0 := loaded(byWave(semScoreSlotReq(boundParams(), false)), semFitPath)
	s0.Cohort = nil
	s1 := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "n", Label: "rows"}}}
	chain := &pulse.ChainRequest{Cohort: &types.Cohort{Filename: "brand.pulse"},
		Stages: []*types.ChainStage{{Name: "s0", Request: s0}, {Name: "s1", Request: s1}}}
	env, err := p.PredictChain(ctx, chain)
	if err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict chain: %v %+v", err, env.Errors)
	}
	res, err := p.ProcessChain(ctx, chain)
	if err != nil {
		t.Fatalf("a chain stage scoring a loaded SEM fit: %v", err)
	}
	want, err := p.Process(ctx, loaded(byWave(semScoreSlotReq(boundParams(), false)), semFitPath))
	if err != nil {
		t.Fatal(err)
	}
	if g, w := dataJSON(t, res.Stages[0].Data), dataJSON(t, want.Data); g != w || !strings.Contains(w, `"n":`) {
		t.Fatalf("chain stage 0 differs from the plain request\n got %s\nwant %s", g, w)
	}
	if !strings.Contains(dataJSON(t, res.Final.Data), `"rows":1200`) {
		t.Fatalf("final %s", dataJSON(t, res.Final.Data))
	}
}

// TestSEMFit_SaveAndLoadRefusals: the save path rule (a SEM fit saves
// only .pulse-sem.json), a write fault, a failed fit writing nothing,
// and every load fault — a version, a decode, a model that does not
// rebuild its stored orders, a kind mismatch on a loaded fit — refused
// with predict's exact error.
func TestSEMFit_SaveAndLoadRefusals(t *testing.T) {
	t.Parallel()

	p, fsys := brandGroupCohort{csv: "brand", wave: encoding.FieldTypeCategoricalU8}.build(t, pulse.Options{})
	ctx := context.Background()

	bad := semSaveCompose("fits/brand"+model.FileSuffix, semScoreSlotReq(boundParams(), false))
	_, rerr := p.Compose(ctx, bad)
	var ce *errors.CodedError
	if !stderrors.As(rerr, &ce) || ce.Code != errors.PULSE_MODEL_SAVE_INVALID || ce.Details["reason"] != model.ReasonSaveSuffix || ce.Details["fit"] != 0 {
		t.Fatalf("a SEM fit saving a model file: %v", rerr)
	}
	env, err := p.PredictCompose(ctx, bad)
	if err != nil {
		t.Fatal(err)
	}
	semSameRefusal(t, "save suffix", rerr, semEnvError(t, env, errors.PULSE_MODEL_SAVE_INVALID))

	if err := fsys.MkdirAll("fits/dir"+model.SEMSuffix, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = p.Compose(ctx, semSaveCompose("fits/dir"+model.SEMSuffix, semScoreSlotReq(boundParams(), false)))
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_MODEL_WRITE_FAILED || ce.Details["stage"] != "stat" || !strings.Contains(ce.Message, "SEM fit") {
		t.Fatalf("a save onto a directory: %v", err)
	}

	oneWave := semFitRequest(semFitKnobs())
	oneWave.Filterers = []*types.Filterer{{Type: types.FILTER_INCLUDE, Field: "wave", Values: []string{"1"}}}
	if _, err := p.Compose(ctx, &pulse.ComposedRequest{Fits: []types.FitSpec{{Name: "brand", Request: oneWave, Save: "fits/failed" + model.SEMSuffix}},
		Requests: []*types.Request{semScoreSlotReq(boundParams(), false)}}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := afero.Exists(fsys, "fits/failed"+model.SEMSuffix); ok {
		t.Fatal("a failed SEM fit wrote a file")
	}

	if _, err := p.Compose(ctx, semSaveCompose(semFitPath, semScoreSlotReq(boundParams(), false))); err != nil {
		t.Fatal(err)
	}
	good, err := afero.ReadFile(fsys, semFitPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(good, &doc); err != nil {
		t.Fatal(err)
	}
	write := func(name string, mutate func(map[string]any)) string {
		m := map[string]any{}
		for k, v := range doc {
			m[k] = v
		}
		mutate(m)
		b, _ := json.Marshal(m)
		path := "fits/" + name + model.SEMSuffix
		if err := afero.WriteFile(fsys, path, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	// The model re-specifies different variables than the stored ones.
	renamed := write("renamed", func(m map[string]any) { m["model"] = strings.ReplaceAll(m["model"].(string), "service", "care") })
	v9 := write("v9", func(m map[string]any) { m["sem_format_version"] = 9 })
	short := write("short", func(m map[string]any) { m["labels"] = []any{"q1"} })
	if renamed == semFitPath || !strings.Contains(doc["model"].(string), "service") {
		t.Fatal("vacuous: the model names no service latent")
	}
	mahal := &types.Request{Cohort: &types.Cohort{Filename: "brand.pulse"},
		Attributes:   []*types.Attribute{{Type: types.ATTR_MAHALANOBIS, Label: "x", Fields: brandMembers, Params: json.RawMessage(`{"fit":"brand"}`)}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}}}
	for _, c := range []struct {
		name string
		req  *types.Request
		code errors.Code
	}{
		{"model does not rebuild", loaded(semScoreSlotReq(boundParams(), false), renamed), errors.PULSE_MODEL_FILE_INVALID},
		{"version", loaded(semScoreSlotReq(boundParams(), false), v9), errors.PULSE_MODEL_VERSION_UNSUPPORTED},
		{"members unlike labels", loaded(semScoreSlotReq(boundParams(), false), short), errors.PULSE_MODEL_FILE_INVALID},
		{"missing", loaded(semScoreSlotReq(boundParams(), false), "fits/none"+model.SEMSuffix), errors.PULSE_MODEL_FILE_UNREADABLE},
		{"population suffix", loaded(semScoreSlotReq(boundParams(), false), "fits/brand"+model.PopulationSuffix), errors.PULSE_MODEL_REF_INVALID},
		{"vector consumer of a loaded sem fit", loaded(mahal, semFitPath), errors.PULSE_MODEL_KIND_MISMATCH},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, rerr := p.Process(ctx, c.req)
			var ce *errors.CodedError
			if !stderrors.As(rerr, &ce) || ce.Code != c.code {
				t.Fatalf("runtime %v, want %s", rerr, c.code)
			}
			env, err := p.PredictEnvelope(ctx, c.req)
			if err != nil {
				t.Fatal(err)
			}
			semSameRefusal(t, c.name, rerr, semEnvError(t, env, c.code))
		})
	}
}
