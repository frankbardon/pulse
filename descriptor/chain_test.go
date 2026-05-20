package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/types"
)

func TestValidateChain_Valid(t *testing.T) {
	data := buildSimplePulseBytes(t)
	env := ValidateChainFromBytes(data, &types.ChainRequest{
		Cohort: &types.Cohort{Filename: "x.pulse"},
		Stages: []*types.ChainStage{
			{
				Name: "sum_x",
				Request: &types.Request{
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_SUM, Field: "x", Label: "sx"},
					},
				},
			},
			{
				Name: "count_sx",
				Request: &types.Request{
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_COUNT, Field: "sx", Label: "n"},
					},
				},
			},
		},
	})
	if len(env.Errors) > 0 {
		t.Fatalf("expected no errors; got %+v", env.Errors)
	}
	res, ok := env.Data.(*ChainValidationResult)
	if !ok || !res.Valid {
		t.Fatalf("expected Valid result; got %+v", env.Data)
	}
	if len(res.StageSchemas) != 2 {
		t.Fatalf("expected 2 stage schemas, got %d", len(res.StageSchemas))
	}
	if res.StageSchemas[0][0] != "sx" {
		t.Errorf("stage0 first output = %q, want sx", res.StageSchemas[0][0])
	}
}

func TestValidateChain_RejectsWindowStage(t *testing.T) {
	data := buildSimplePulseBytes(t)
	env := ValidateChainFromBytes(data, &types.ChainRequest{
		Cohort: &types.Cohort{Filename: "x.pulse"},
		Stages: []*types.ChainStage{
			{
				Name: "window_lag",
				Request: &types.Request{
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_SUM, Field: "x"},
					},
					Windows: []*types.Window{
						{Type: types.WIN_LAG, Field: "x"},
					},
				},
			},
		},
	})
	if len(env.Errors) == 0 {
		t.Fatal("expected PULSE_CHAIN_NOT_MERGEABLE for windowed stage")
	}
	if env.Errors[0].Code != "PULSE_CHAIN_NOT_MERGEABLE" {
		t.Errorf("expected PULSE_CHAIN_NOT_MERGEABLE, got %q", env.Errors[0].Code)
	}
}

func TestValidateChain_UnknownFieldInStage2(t *testing.T) {
	data := buildSimplePulseBytes(t)
	env := ValidateChainFromBytes(data, &types.ChainRequest{
		Cohort: &types.Cohort{Filename: "x.pulse"},
		Stages: []*types.ChainStage{
			{
				Request: &types.Request{
					Aggregations: []*types.Aggregation{
						{Type: types.AGG_SUM, Field: "x", Label: "sx"},
					},
				},
			},
			{
				Request: &types.Request{
					Aggregations: []*types.Aggregation{
						// 'does_not_exist' is not in stage 1's output
						{Type: types.AGG_SUM, Field: "does_not_exist"},
					},
				},
			},
		},
	})
	if len(env.Errors) == 0 {
		t.Fatal("expected SERVICE_VALIDATION for unknown field in stage 2")
	}
	if env.Errors[0].Code != "SERVICE_VALIDATION" {
		t.Errorf("expected SERVICE_VALIDATION, got %q", env.Errors[0].Code)
	}
}

func TestValidateChain_Empty(t *testing.T) {
	data := buildSimplePulseBytes(t)
	env := ValidateChainFromBytes(data, &types.ChainRequest{
		Cohort: &types.Cohort{Filename: "x.pulse"},
		Stages: nil,
	})
	if len(env.Errors) == 0 || env.Errors[0].Code != "PULSE_CHAIN_EMPTY" {
		t.Fatalf("expected PULSE_CHAIN_EMPTY, got %+v", env.Errors)
	}
}
