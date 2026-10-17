package fieldaccept

import (
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestSlots are the column-naming slots of one test spec a required-slot
// rule reads.
type TestSlots struct {
	Field, Field2, SplitBy, Subject string
}

// TestSlotsOf reads t's column-naming slots.
func TestSlotsOf(t *types.Test) TestSlots {
	return TestSlots{Field: t.Field, Field2: t.Field2, SplitBy: t.SplitBy, Subject: t.SubjectField}
}

func (s TestSlots) value(slot Slot) string {
	switch slot {
	case SlotField:
		return s.Field
	case SlotField2:
		return s.Field2
	case SlotSplitBy:
		return s.SplitBy
	case SlotSubject:
		return s.Subject
	}
	return ""
}

// slotRule refuses its message when any of its slots is empty.
type slotRule struct {
	slots []Slot
	msg   string
}

func one(slot Slot, msg string) slotRule { return slotRule{slots: []Slot{slot}, msg: msg} }

func any2(a, b Slot, msg string) slotRule { return slotRule{slots: []Slot{a, b}, msg: msg} }

// fieldThenSplit is the common tier-1 pair: field, then split_by.
func fieldThenSplit(op string) []slotRule {
	return []slotRule{one(SlotField, op+" requires field"), one(SlotSplitBy, op+" requires split_by")}
}

// rowTestRequired / postTestRequired are each built-in test
// constructor's required-slot refusals, in its check order — the
// tier-1 row tests and the tier-2 post-tests. TEST_PROP_Z (split_by
// optional under params.p0), the contingency tests (rows / cols) and
// the vector tests judge their own slots.
var rowTestRequired = map[types.TestType][]slotRule{
	types.TEST_T:              {one(SlotField, "TEST_T requires field")},
	types.TEST_WELCH:          fieldThenSplit("TEST_WELCH"),
	types.TEST_ANOVA_F:        fieldThenSplit("TEST_ANOVA_F"),
	types.TEST_ANOVA_WELCH:    fieldThenSplit("TEST_ANOVA_WELCH"),
	types.TEST_BROWN_FORSYTHE: fieldThenSplit("TEST_BROWN_FORSYTHE"),
	types.TEST_KRUSKAL_WALLIS: fieldThenSplit("TEST_KRUSKAL_WALLIS"),
	types.TEST_KS:             fieldThenSplit("TEST_KS"),
	types.TEST_MANN_WHITNEY_U: fieldThenSplit("TEST_MANN_WHITNEY_U"),
	types.TEST_Z_TWO_SAMPLE: {
		one(SlotField, "TEST_Z_TWO_SAMPLE requires field (numeric measurement column)"),
		one(SlotSplitBy, "TEST_Z_TWO_SAMPLE requires split_by (categorical two-group column)"),
	},
	types.TEST_ANOVA_RM: {{slots: []Slot{SlotField, SlotSplitBy, SlotSubject},
		msg: "TEST_ANOVA_RM requires field (value), split_by (condition), and subject_field"}},
	types.TEST_PAIRED_T: {
		one(SlotField, "TEST_PAIRED_T requires field"),
		one(SlotField2, "TEST_PAIRED_T requires field2 (paired column)"),
	},
	types.TEST_PEARSON_R: {
		one(SlotField, "TEST_PEARSON_R requires field"),
		one(SlotField2, "TEST_PEARSON_R requires field2 (second numeric column)"),
	},
	types.TEST_KENDALL_TAU:  {any2(SlotField, SlotField2, "TEST_KENDALL_TAU requires field and field2")},
	types.TEST_SPEARMAN_R:   {any2(SlotField, SlotField2, "TEST_SPEARMAN_R requires field and field2")},
	types.TEST_WILCOXON_SR:  {any2(SlotField, SlotField2, "TEST_WILCOXON_SR requires field and field2 (paired columns)")},
	types.TEST_SHAPIRO_WILK: {one(SlotField, "TEST_SHAPIRO_WILK requires field")},
}

var postTestRequired = map[types.TestType][]slotRule{
	types.TEST_ANOVA_F:        {any2(SlotField, SlotSplitBy, "TEST_ANOVA_F (post): requires field (per-group mean column) and split_by (group label column)")},
	types.TEST_TREND:          {one(SlotField, "TEST_TREND requires field")},
	types.TEST_TUKEY_HSD:      {any2(SlotField, SlotSplitBy, "TEST_TUKEY_HSD requires field (per-group mean column) and split_by (group label column)")},
	types.TEST_PEARSON_R:      {one(SlotField, "TEST_PEARSON_R (post): requires field"), one(SlotField2, "TEST_PEARSON_R (post): requires field2 (second numeric column)")},
	types.TEST_PAIRED_T:       {any2(SlotField, SlotField2, "TEST_PAIRED_T (post): requires field and field2 (paired columns)")},
	types.TEST_SPEARMAN_R:     {any2(SlotField, SlotField2, "TEST_SPEARMAN_R (post): requires field and field2")},
	types.TEST_KENDALL_TAU:    {any2(SlotField, SlotField2, "TEST_KENDALL_TAU (post): requires field and field2")},
	types.TEST_WILCOXON_SR:    {any2(SlotField, SlotField2, "TEST_WILCOXON_SR (post): requires field and field2 (paired columns)")},
	types.TEST_ANOVA_WELCH:    {any2(SlotField, SlotSplitBy, "TEST_ANOVA_WELCH (post): requires field (per-group mean column) and split_by (group label column)")},
	types.TEST_BROWN_FORSYTHE: {any2(SlotField, SlotSplitBy, "TEST_BROWN_FORSYTHE (post): requires field and split_by")},
	types.TEST_SHAPIRO_WILK:   {one(SlotField, "TEST_SHAPIRO_WILK (post): requires field")},
	types.TEST_KS:             {any2(SlotField, SlotSplitBy, "TEST_KS (post): requires field and split_by")},
}

func requiredRules(op types.TestType, post bool) ([]slotRule, bool) {
	if post {
		r, ok := postTestRequired[op]
		return r, ok
	}
	r, ok := rowTestRequired[op]
	return r, ok
}

// HasRequiredSlots reports whether built-in test op (a post-test when
// post) has a tabled required-slot rule.
func HasRequiredSlots(op types.TestType, post bool) bool {
	_, ok := requiredRules(op, post)
	return ok
}

// RequiredSlotsRefusal is a built-in test constructor's missing-slot
// refusal — PROCESSING_CONFIG with the constructor's message, at its
// first rule with an empty slot — or nil when every required slot is
// named (or op has no tabled rule). The constructors raise it and
// predict reports the same coded error, so a missing field / field2 /
// split_by / subject_field reads alike on both sides.
func RequiredSlotsRefusal(op types.TestType, post bool, s TestSlots) *errors.CodedError {
	rules, _ := requiredRules(op, post)
	for _, r := range rules {
		for _, slot := range r.slots {
			if s.value(slot) == "" {
				return errors.NewCodedError(errors.PROCESSING_CONFIG, r.msg)
			}
		}
	}
	return nil
}
