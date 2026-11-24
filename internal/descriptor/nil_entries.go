package descriptor

import (
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// nil_entries.go is the null-entry rule for a request root's operator
// lists: `groups: [null]` decodes to a nil *types.Group, and every
// arm — predict's streamability gate, the runtime's canStream, the
// buffered partitioner — would dereference it. SlotRefusal runs it
// first, so every service funnel and every predict root refuse the
// same entry with the same code, message and details before anything
// else reads the lists (TestNilEntryRefusedOnEveryArm). The crosstab
// axis twin is crosstabaxis.NilGrouperRefusals; a chain stage's lists
// stay with mergegate (PULSE_CHAIN_NOT_MERGEABLE).

// nilEntryNoun names one list element in the refusal message.
var nilEntryNoun = map[string]string{
	"filterers":    "filterer",
	"aggregations": "aggregation",
	"attributes":   "attribute",
	"groups":       "grouper",
	"windows":      "window",
	"features":     "feature",
	"tests":        "test",
	"post_tests":   "test",
	"regressions":  "regression",
	"joins":        "join",
	"requests":     "request",
}

// nilEntryRefusal is PROCESSING_CONFIG
// "<slot> contains a null <noun> at index <i>", details {slot, index}.
func nilEntryRefusal(slot string, i int) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
		slot+" contains a null "+nilEntryNoun[slot]+" at index "+strconv.Itoa(i),
		map[string]any{"slot": slot, "index": i})
}

// firstNil returns the index of the first nil element, or -1.
func firstNil[T any](list []*T) int {
	for i, e := range list {
		if e == nil {
			return i
		}
	}
	return -1
}

// requestNilEntryRefusal walks one Request's operator lists in JSON
// order and refuses the first null entry. Nil for a nil request.
func requestNilEntryRefusal(req *types.Request) *errors.CodedError {
	if req == nil {
		return nil
	}
	lists := []struct {
		slot string
		at   int
	}{
		{"filterers", firstNil(req.Filterers)},
		{"aggregations", firstNil(req.Aggregations)},
		{"attributes", firstNil(req.Attributes)},
		{"groups", firstNil(req.Groups)},
		{"windows", firstNil(req.Windows)},
		{"features", firstNil(req.Features)},
		{"tests", firstNil(req.Tests)},
		{"post_tests", firstNil(req.PostTests)},
		{"regressions", firstNil(req.Regressions)},
		{"joins", firstNil(req.Joins)},
	}
	for _, l := range lists {
		if l.at >= 0 {
			return nilEntryRefusal(l.slot, l.at)
		}
	}
	return nil
}

// nilEntryGate is the null-entry gate SlotRefusal runs first. v is a
// *types.Request (its operator lists), a *types.FacetRequest (its
// filterers) or a *types.ComposedRequest (a null Requests entry, then
// each slot's lists with details.request = i, then each fit request's
// with details.fit = i — the base, banner and sweep slots are judged
// once expanded into Requests); anything else passes.
func nilEntryGate(v any) error {
	switch r := v.(type) {
	case *types.Request:
		if ce := requestNilEntryRefusal(r); ce != nil {
			return ce
		}
	case *types.FacetRequest:
		if r == nil {
			return nil
		}
		if i := firstNil(r.Filterers); i >= 0 {
			return nilEntryRefusal("filterers", i)
		}
	case *types.ComposedRequest:
		if r == nil {
			return nil
		}
		if i := firstNil(r.Requests); i >= 0 {
			return nilEntryRefusal("requests", i)
		}
		for i, req := range r.Requests {
			if ce := requestNilEntryRefusal(req); ce != nil {
				return RefusalAt(ce, "request", i)
			}
		}
		for i := range r.Fits {
			if ce := requestNilEntryRefusal(r.Fits[i].Request); ce != nil {
				return RefusalAt(ce, "fit", i)
			}
		}
	}
	return nil
}
