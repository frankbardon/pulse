package processing

import (
	"fmt"
	"math"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg/metric"
)

// Set-similarity built-ins: jaccard, dice, hamming, overlap. Each takes
// two label sets and compares them BY LABEL, so two set fields with
// different dictionaries compare correctly. The counting rules live in
// the linalg/metric registry (consumer "expr": SetCounts + EvalSets);
// labelSetCounts counts label lists, and these closures adapt
// arguments.
//
// An argument is one of:
//
//   - a dictionary-bearing set field — a sorted []string of labels at
//     every width, set_u128 / set_u256 included;
//   - a literal string list, ["Acme", "Zed"];
//   - a dictionary-less set field — its raw bitmask (a uint64 for
//     set_u8..set_u64, an encoding.SetMask for the wide rungs). It has no
//     labels, so it pairs only with another mask, compared bit by bit;
//     a mask beside a label set is refused (PROCESSING_RUNTIME).
//
// A nil argument (a null set) gives NaN for all four — hamming too, so a
// missing answer never reads as "identical". An empty set is not null:
// it is a set with no members (two empties: jaccard / dice / overlap
// NaN, hamming 0).

// setSimArg is one coerced set-similarity argument.
type setSimArg struct {
	labels []string
	mask   encoding.SetMask
	isMask bool
	null   bool
}

func setSimArgOf(fn string, i int, a any) (setSimArg, error) {
	switch v := a.(type) {
	case nil:
		return setSimArg{null: true}, nil
	case []string:
		return setSimArg{labels: v}, nil
	case []any:
		out := make([]string, len(v))
		for j, e := range v {
			s, ok := e.(string)
			if !ok {
				return setSimArg{}, errors.NewCodedErrorWithDetails(errors.PROCESSING_RUNTIME,
					fmt.Sprintf("%s(): set argument %d element %d is %T, not a label string", fn, i+1, j, e),
					map[string]any{"function": fn, "argument": i + 1, "element": j})
			}
			out[j] = s
		}
		return setSimArg{labels: out}, nil
	case uint64:
		return setSimArg{mask: encoding.SetMaskFromUint64(v), isMask: true}, nil
	case encoding.SetMask:
		return setSimArg{mask: v, isMask: true}, nil
	}
	return setSimArg{}, errors.NewCodedErrorWithDetails(errors.PROCESSING_RUNTIME,
		fmt.Sprintf("%s(): argument %d is %T, not a label set (a set field or a literal list such as [\"a\", \"b\"])", fn, i+1, a),
		map[string]any{"function": fn, "argument": i + 1})
}

// setSimOverlap coerces the two arguments and counts them. null is true
// when either side is a null set.
func setSimOverlap(fn string, args []any) (o metric.SetCounts, null bool, err error) {
	if len(args) != 2 {
		return o, false, builtinArityError(fn, 2, len(args))
	}
	a, err := setSimArgOf(fn, 0, args[0])
	if err != nil {
		return o, false, err
	}
	b, err := setSimArgOf(fn, 1, args[1])
	if err != nil {
		return o, false, err
	}
	if a.null || b.null {
		return o, true, nil
	}
	switch {
	case a.isMask && b.isMask:
		return metric.SetCounts{
			A:     a.mask.PopCount(),
			B:     b.mask.PopCount(),
			Inter: a.mask.Intersect(b.mask).PopCount(),
		}, false, nil
	case a.isMask || b.isMask:
		return o, false, errors.NewCodedErrorWithDetails(errors.PROCESSING_RUNTIME,
			fmt.Sprintf("%s(): cannot compare a dictionary-less set (a raw bitmask, no labels) with a label set; give both arguments labels or both dictionary-less", fn),
			map[string]any{"function": fn})
	}
	return labelSetCounts(a.labels, b.labels), false, nil
}

// labelSetCounts counts the distinct members of a and b and of their
// intersection. Duplicates within one list count once; order is
// irrelevant. Neither input is mutated.
func labelSetCounts(a, b []string) metric.SetCounts {
	inA := make(map[string]struct{}, len(a))
	for _, s := range a {
		inA[s] = struct{}{}
	}
	inB := make(map[string]struct{}, len(b))
	inter := 0
	for _, s := range b {
		if _, dup := inB[s]; dup {
			continue
		}
		inB[s] = struct{}{}
		if _, ok := inA[s]; ok {
			inter++
		}
	}
	return metric.SetCounts{A: len(inA), B: len(inB), Inter: inter}
}

// setMetric evaluates the registry set metric r over counts that
// setSimOverlap built from two real sets; a refusal (impossible counts)
// is a PROCESSING_INTERNAL fault.
func setMetric(fn string, r exprMetricRef, o metric.SetCounts) (float64, error) {
	v, err := r.m.EvalSets(r.form, o)
	if err != nil {
		return 0, errors.WrapCodedError(err, errors.PROCESSING_INTERNAL, fn+"(): "+err.Error())
	}
	return v, nil
}

func setSimilarity(fn string, r exprMetricRef) exprBuiltinFunc {
	return func(args ...any) (any, error) {
		o, null, err := setSimOverlap(fn, args)
		if err != nil {
			return nil, err
		}
		if null {
			return math.NaN(), nil
		}
		return setMetric(fn, r, o)
	}
}

// exprHamming is the registry's hamming, resolved once.
var exprHamming = exprMetric(builtinMetric("hamming"))

// setHammingBuiltin returns an int count, NaN when either set is null.
func setHammingBuiltin(args ...any) (any, error) {
	o, null, err := setSimOverlap("hamming", args)
	if err != nil {
		return nil, err
	}
	if null {
		return math.NaN(), nil
	}
	v, err := setMetric("hamming", exprHamming, o)
	if err != nil {
		return nil, err
	}
	return int(v), nil
}
