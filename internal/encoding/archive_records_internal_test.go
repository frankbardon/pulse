package encoding

import (
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// TestWithEntry: a coded refusal gains details.entry naming the shard
// (details created when absent) unless it already names one; an uncoded
// error passes through untouched.
func TestWithEntry(t *testing.T) {
	t.Parallel()

	bare := errors.NewCodedError(errors.PULSE_SHARD_DICT_DIVERGENCE, "diverges")
	if got := withEntry(bare, "part-00003.pulse").(*errors.CodedError); got.Details["entry"] != "part-00003.pulse" {
		t.Errorf("bare refusal details %v, want entry part-00003.pulse", got.Details)
	}
	named := errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_SCHEMA_MISMATCH, "mismatch", map[string]any{"entry": "inner", "field": "x"})
	if got := withEntry(named, "outer").(*errors.CodedError); got.Details["entry"] != "inner" || got.Details["field"] != "x" {
		t.Errorf("named refusal details %v, want the inner entry kept", got.Details)
	}
	plain := stderrors.New("plain")
	if got := withEntry(plain, "x"); got != plain {
		t.Errorf("uncoded error became %v", got)
	}
}
