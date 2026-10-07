package pulse

import (
	stderrors "errors"
	"reflect"
	"testing"
	"time"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fs"
)

func newLimitsInstance(t *testing.T, l Limits) (*Pulse, error) {
	t.Helper()
	return New(Options{FS: fs.NewMemMap().Fs(), Limits: l})
}

// TestLimits_DefaultsResolve: a zero Options resolves every built-in
// default, and the service and the instance snapshot carry the same
// effective values the accessor reports.
func TestLimits_DefaultsResolve(t *testing.T) {
	p, err := newLimitsInstance(t, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	want := Limits{
		RequestTimeout:     Unlimited,
		MaxGroups:          10_000_000,
		MaxCrosstabCells:   10_000_000,
		MaxEstimatedMemory: Unlimited,
		MaxMatrixDim:       2_048,
		MaxComposeSlots:    1_000,
		MaxChainStages:     1_000,
		MaxJoinBuildRows:   100_000_000,
	}
	if got := p.Limits(); got != want {
		t.Fatalf("Limits() = %+v, want %+v", got, want)
	}
	exported := Limits{
		RequestTimeout:     DefaultRequestTimeout,
		MaxGroups:          DefaultMaxGroups,
		MaxCrosstabCells:   DefaultMaxCrosstabCells,
		MaxEstimatedMemory: DefaultMaxEstimatedMemory,
		MaxMatrixDim:       DefaultMaxMatrixDim,
		MaxComposeSlots:    DefaultMaxComposeSlots,
		MaxChainStages:     DefaultMaxChainStages,
		MaxJoinBuildRows:   DefaultMaxJoinBuildRows,
	}
	if exported != want {
		t.Fatalf("exported default constants = %+v, want %+v", exported, want)
	}
	if got := p.svc.InstanceSnapshot().Limits(); got != want {
		t.Fatalf("snapshot Limits() = %+v, want %+v", got, want)
	}
}

// TestLimits_OptionsInstalled: non-zero Options values (incl. Unlimited)
// reach the accessor, the service and the snapshot.
func TestLimits_OptionsInstalled(t *testing.T) {
	in := Limits{
		RequestTimeout:     5 * time.Second,
		MaxGroups:          Unlimited,
		MaxCrosstabCells:   Unlimited,
		MaxEstimatedMemory: 1 << 30,
		MaxMatrixDim:       Unlimited,
		MaxComposeSlots:    Unlimited,
		MaxChainStages:     3,
		MaxJoinBuildRows:   Unlimited,
	}
	p, err := newLimitsInstance(t, in)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Limits(); got != in {
		t.Fatalf("Limits() = %+v, want %+v", got, in)
	}
	if got := p.svc.Limits(); got != in {
		t.Fatalf("service Limits() = %+v, want %+v", got, in)
	}
	if got := p.svc.InstanceSnapshot().Limits(); got != in {
		t.Fatalf("snapshot Limits() = %+v, want %+v", got, in)
	}
}

// TestLimits_AccessorIsCopy: mutating the returned value never changes
// the instance.
func TestLimits_AccessorIsCopy(t *testing.T) {
	p, err := newLimitsInstance(t, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	l := p.Limits()
	l.MaxGroups = 1
	if p.Limits().MaxGroups != DefaultMaxGroups {
		t.Fatal("mutating the returned Limits changed the instance")
	}
}

// TestLimits_InvalidRefused: -2 on every field fails New with
// PULSE_LIMIT_INVALID naming the field.
func TestLimits_InvalidRefused(t *testing.T) {
	v := reflect.ValueOf(Limits{})
	snake := map[string]string{
		"RequestTimeout":     "request_timeout",
		"MaxGroups":          "max_groups",
		"MaxCrosstabCells":   "max_crosstab_cells",
		"MaxEstimatedMemory": "max_estimated_memory",
		"MaxMatrixDim":       "max_matrix_dim",
		"MaxComposeSlots":    "max_compose_slots",
		"MaxChainStages":     "max_chain_stages",
		"MaxJoinBuildRows":   "max_join_build_rows",
	}
	if v.NumField() != len(snake) {
		t.Fatalf("Limits has %d fields, test covers %d", v.NumField(), len(snake))
	}
	for i := 0; i < v.NumField(); i++ {
		name := v.Type().Field(i).Name
		t.Run(name, func(t *testing.T) {
			var l Limits
			reflect.ValueOf(&l).Elem().Field(i).SetInt(-2)
			_, err := newLimitsInstance(t, l)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_LIMIT_INVALID {
				t.Fatalf("err = %v, want PULSE_LIMIT_INVALID", err)
			}
			if ce.Details["limit"] != snake[name] {
				t.Errorf("details.limit = %v, want %s", ce.Details["limit"], snake[name])
			}
			if ce.Details["value"] != int64(-2) {
				t.Errorf("details.value = %v, want -2", ce.Details["value"])
			}
		})
	}
}
