package vectors

import (
	"encoding/json"
	stderrors "errors"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/sem"
	"github.com/frankbardon/pulse/types"
)

// TestResolveMatrices_SEM pins MAT_SEM's resolution: the model's
// observed variables are matched to the members by label (semopy's
// variable order mapped onto member positions), the default model is
// one factor over every member, the solver options decode, and every
// refusal names its param.
func TestResolveMatrices_SEM(t *testing.T) {
	resolve := func(spec types.MatrixSpec, vecs ...types.VectorSpec) (Matrix, *errors.CodedError) {
		t.Helper()
		got, err := ResolveMatrices(&types.Request{Vectors: vecs, Matrices: []types.MatrixSpec{spec}}, testSchema(), nil)
		if err != nil {
			return Matrix{}, err
		}
		return got[0], nil
	}
	sem3 := func(params string) types.MatrixSpec {
		s := types.MatrixSpec{Type: types.MAT_SEM, Fields: []string{"q_1", "q_2", "q_3"}}
		if params != "" {
			s.Params = json.RawMessage(params)
		}
		return s
	}

	t.Run("default model", func(t *testing.T) {
		m, err := resolve(sem3(""))
		if err != nil {
			t.Fatal(err)
		}
		if m.SEM == nil || m.SEM.Desc != "F1 =~ q_1 + q_2 + q_3" {
			t.Fatalf("plan = %+v", m.SEM)
		}
		if m.SEM.Options != (sem.Options{}) || !m.Streamable || !m.Mergeable {
			t.Errorf("default options / tier = %+v / %v %v", m.SEM.Options, m.Streamable, m.Mergeable)
		}
		if keys, _ := m.AuxiliaryKeys(); !slices.Equal(keys, []string{"estimates", "residual_covariance"}) {
			t.Errorf("auxiliary keys = %v", keys)
		}
	})

	t.Run("order maps semopy's variables to members", func(t *testing.T) {
		// semopy orders observed variables: sorted outputs (q_1, q_2,
		// q_3 here) — member positions 1, 2, 0 in a q_3-first battery.
		spec := types.MatrixSpec{Type: types.MAT_SEM, Fields: []string{"q_3", "q_1", "q_2"},
			Params: json.RawMessage(`{"model": "f =~ q_2 + q_1 + q_3"}`)}
		m, err := resolve(spec)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(m.SEM.Model.Observed, []string{"q_1", "q_2", "q_3"}) || !slices.Equal(m.SEM.Order, []int{1, 2, 0}) {
			t.Errorf("observed %v order %v", m.SEM.Model.Observed, m.SEM.Order)
		}
	})

	t.Run("labels name the variables", func(t *testing.T) {
		vec := types.VectorSpec{Name: "b", Fields: []string{"q_1", "q_2", "q_3"}, Labels: []string{"one", "two", "three"}}
		m, err := resolve(types.MatrixSpec{Type: types.MAT_SEM, Vector: "b", Params: json.RawMessage(`{"model": "f =~ one + two + three"}`)}, vec)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(m.SEM.Order, []int{0, 2, 1}) { // observed sorted: one, three, two
			t.Errorf("order %v", m.SEM.Order)
		}
		if _, err := resolve(types.MatrixSpec{Type: types.MAT_SEM, Vector: "b", Params: json.RawMessage(`{"model": "f =~ q_1 + q_2 + q_3"}`)}, vec); err == nil {
			t.Error("member names accepted where the vector carries labels")
		}
	})

	t.Run("options", func(t *testing.T) {
		m, err := resolve(sem3(`{"solver": "fisher", "information": "rank2", "grad_tol": 1e-9, "max_iter": 50}`))
		if err != nil {
			t.Fatal(err)
		}
		want := sem.Options{Solver: sem.SolverFisher, Information: sem.InformationRank2, GradTol: 1e-9, MaxIter: 50}
		if m.SEM.Options != want {
			t.Errorf("options = %+v, want %+v", m.SEM.Options, want)
		}
		if m, err = resolve(sem3(`{"ftol": 1e-12}`)); err != nil || m.SEM.Options.Ftol != 1e-12 {
			t.Errorf("ftol: %v %+v", err, m.SEM)
		}
	})

	refusals := []struct {
		name, params, reason, param string
	}{
		{"bad syntax", `{"model": "f => q_1"}`, "bad_params", "model"},
		{"empty model", `{"model": "  "}`, "bad_params", "model"},
		{"non-member variable", `{"model": "f =~ q_1 + q_2 + score"}`, "bad_params", "model"},
		{"unused member", `{"model": "f =~ q_1 + q_2"}`, "bad_params", "model"},
		{"unknown solver", `{"solver": "newton"}`, "bad_params", "solver"},
		{"unknown information", `{"information": "sparse"}`, "bad_params", "information"},
		{"ftol beside fisher", `{"solver": "fisher", "ftol": 1e-8}`, "bad_params", "ftol"},
		{"grad_tol beside slsqp", `{"grad_tol": 1e-8}`, "bad_params", "grad_tol"},
		{"zero ftol", `{"ftol": 0}`, "bad_params", "ftol"},
		{"zero max_iter", `{"max_iter": 0}`, "bad_params", "max_iter"},
		{"pairwise", `{"missing": "pairwise"}`, "pairwise_unsupported", "missing"},
		{"unknown key", `{"modle": "f =~ q_1 + q_2 + q_3"}`, "bad_params", ""},
	}
	for _, c := range refusals {
		t.Run(c.name, func(t *testing.T) {
			_, err := resolve(sem3(c.params))
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.SERVICE_VALIDATION {
				t.Fatalf("err = %v, want SERVICE_VALIDATION", err)
			}
			if ce.Details["reason"] != c.reason {
				t.Errorf("reason = %v, want %s", ce.Details["reason"], c.reason)
			}
			if c.param != "" && ce.Details["param"] != c.param {
				t.Errorf("param = %v, want %s", ce.Details["param"], c.param)
			}
		})
	}
	t.Run("refusal details name the variables", func(t *testing.T) {
		_, err := resolve(sem3(`{"model": "f =~ q_1 + q_2 + score + mid"}`))
		if got := err.Details["not_members"]; !slices.Equal(got.([]string), []string{"mid", "score"}) {
			t.Errorf("not_members = %v", got)
		}
		_, err = resolve(sem3(`{"model": "f =~ q_1 + q_2"}`))
		if got := err.Details["unused_members"]; !slices.Equal(got.([]string), []string{"q_3"}) {
			t.Errorf("unused_members = %v", got)
		}
	})
}
