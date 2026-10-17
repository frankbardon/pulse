package processing

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fieldaccept"
	"github.com/frankbardon/pulse/types"
)

// TestRequiredSlots_ConstructorsRaiseTheTable: for every built-in row
// test and post-test with a tabled required-slot rule, over every
// combination of named / empty field, field2, split_by and
// subject_field, the constructor's missing-slot refusal is exactly
// fieldaccept.RequiredSlotsRefusal — the coded error predict reports —
// and a combination the table accepts draws no missing-slot refusal.
func TestRequiredSlots_ConstructorsRaiseTheTable(t *testing.T) {
	type factory func(*types.Test) error
	var cases []struct {
		op   types.TestType
		post bool
		make factory
	}
	for op, f := range rowTestRegistry {
		if !fieldaccept.HasRequiredSlots(op, false) {
			continue
		}
		cases = append(cases, struct {
			op   types.TestType
			post bool
			make factory
		}{op, false, func(s *types.Test) error { _, err := f(s, nil); return err }})
	}
	for op, f := range postTestRegistry {
		if !fieldaccept.HasRequiredSlots(op, true) {
			t.Errorf("post-test %s has no required-slot rule", op)
			continue
		}
		cases = append(cases, struct {
			op   types.TestType
			post bool
			make factory
		}{op, true, func(s *types.Test) error { _, err := f(s, nil); return err }})
	}
	if len(cases) < 25 {
		t.Fatalf("only %d tabled constructors", len(cases))
	}
	for _, c := range cases {
		for mask := range 16 {
			name := func(bit int) string {
				if mask&(1<<bit) != 0 {
					return "col"
				}
				return ""
			}
			spec := &types.Test{Type: c.op, Field: name(0), Field2: name(1), SplitBy: name(2), SubjectField: name(3),
				Params: []byte(`{"ms_within":1,"df_within":1,"n_col":"n","variance_col":"v"}`)}
			want := fieldaccept.RequiredSlotsRefusal(c.op, c.post, fieldaccept.TestSlotsOf(spec))
			err := c.make(spec)
			var ce *errors.CodedError
			coded := stderrors.As(err, &ce)
			if want != nil {
				if !coded || ce.Code != want.Code || ce.Message != want.Message {
					t.Errorf("%s post=%v mask %04b: constructor %v, want %s %q", c.op, c.post, mask, err, want.Code, want.Message)
				}
				continue
			}
			if coded && ce.Code == errors.PROCESSING_CONFIG && strings.Contains(ce.Message, " requires ") {
				t.Errorf("%s post=%v mask %04b: constructor refuses %q the table accepts", c.op, c.post, mask, ce.Message)
			}
		}
	}
}
