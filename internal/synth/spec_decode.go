package synth

import (
	stderrors "errors"
	"reflect"
	"slices"
	"strconv"
	"sync/atomic"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/strictjson"
)

// Strict document decode (U44 #302). Every synth input document — a
// spec (ParseSpec), a standalone rules file (ParseRules) and a profile
// (ParseProfile) — decodes through internal/strictjson, so an unknown
// key at any depth is refused PULSE_REQUEST_UNKNOWN_FIELD with its path
// and a suggestion rather than silently dropped. A distribution's
// `params` object is a free map on the wire, so its keys are checked
// separately against distributionParamKeys in validateSpec, which every
// generation path (ParseSpec, Synth, SynthBytes, AugmentFromProfile)
// runs.

// correlationAliases is the one misnomer a correlation pair carries:
// the spec key was `correlation` until the rename to `rho` (the
// profile's spelling), and no edit distance reaches it.
var correlationAliases = map[string]string{"correlation": "rho"}

var (
	correlationSpecType       = reflect.TypeFor[CorrelationSpec]()
	vectorCorrelationSpecType = reflect.TypeFor[VectorCorrelationSpec]()
)

// documentOptions are the strict-decode options every synth document
// shares.
func documentOptions() strictjson.Options {
	return strictjson.Options{Aliases: func(t reflect.Type) map[string]string {
		if t == correlationSpecType || t == vectorCorrelationSpecType {
			return correlationAliases
		}
		return nil
	}}
}

// decodeDocument strictly decodes raw into out. An unknown key is the
// coded PULSE_REQUEST_UNKNOWN_FIELD refusal, returned as is; any other
// decode fault is the encoding/json error, for the caller to wrap.
func decodeDocument(raw []byte, out any) (coded *errors.CodedError, err error) {
	err = strictjson.DecodeInto(raw, out, documentOptions())
	if err == nil {
		return nil, nil
	}
	var ce *errors.CodedError
	if stderrors.As(err, &ce) && ce.Code == errors.PULSE_REQUEST_UNKNOWN_FIELD {
		return ce, err
	}
	return nil, err
}

// ParseProfile strictly decodes a profile document (`profile create`
// output, the `synth from-profile --profile` input). An unknown key is
// refused PULSE_REQUEST_UNKNOWN_FIELD; malformed JSON is
// SERVICE_VALIDATION.
func ParseProfile(raw []byte) (*Profile, error) {
	var p Profile
	if ce, err := decodeDocument(raw, &p); ce != nil {
		return nil, ce
	} else if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_VALIDATION, "parsing profile")
	}
	return &p, nil
}

// distributionParamKeys is the params allowlist per distribution: the
// exact key set its sampler (and the copula's quantile builder) reads.
// The manifest's synth_distributions Param lists declare the same sets
// (TestDistributionParamsMatchManifest, internal/descriptor) and the
// keys actually read are held to it (TestDistributionParamsDeclaredMatchRead).
var distributionParamKeys = map[string][]string{
	DistUniform:             {"min", "max"},
	DistNormal:              {"mean", "std", "min", "max"},
	DistLogNormal:           {"mu", "sigma"},
	DistExponential:         {"lambda"},
	DistPoisson:             {"lambda"},
	DistPareto:              {"xm", "alpha"},
	DistBernoulli:           {"p"},
	DistMonotonicFrom:       {"start", "step"},
	DistWeightedCategorical: {"values", "weights"},
	DistDiscrete:            {"values", "weights"},
	DistMixture:             {"means", "stds", "weights"},
	DistUniformDate:         {"start", "end"},
	DistRegex:               {"pattern", "max_repeat"},
	DistConstant:            {"value", "options"}, // options: set_* fields only (setOnlyParam)
	DistSetBernoulli:        {"options", "frequencies"},
}

// setOnlyParam is the params key a set_* field reads whatever its
// distribution — `options`, the dictionary the writer pre-registers in
// bit order (writer.go) and a rule's selection is checked against
// (rules_coerce.go). set_bernoulli declares it as its own; on a
// `constant` it is accepted only when the field is a set_*.
const setOnlyParam = "options"

// distributionParamAliases are params misnomers no edit distance
// reaches: the rate / moment spellings of a neighbouring
// parameterisation.
var distributionParamAliases = map[string]map[string]string{
	DistExponential: {"rate": "lambda"},
	DistPoisson:     {"rate": "lambda", "mean": "lambda"},
	DistNormal:      {"mu": "mean", "sigma": "std", "sd": "std"},
	DistLogNormal:   {"mean": "mu", "std": "sigma", "sd": "sigma"},
	DistPareto:      {"scale": "xm", "shape": "alpha"},
}

// DistributionParamKeys returns the params keys distribution name
// accepts, and whether name is a registered distribution.
func DistributionParamKeys(name string) ([]string, bool) {
	keys, ok := distributionParamKeys[name]
	return slices.Clone(keys), ok
}

// checkDistributionParams refuses a params key distribution dist does
// not read, as PULSE_REQUEST_UNKNOWN_FIELD located at objectPath
// (`fields[2].params`). An unregistered distribution is left to
// buildSampler's PULSE_SYNTH_DISTRIBUTION_UNKNOWN.
func checkDistributionParams(objectPath, dist, fieldType string, params map[string]any) error {
	valid, ok := distributionParamKeys[dist]
	if !ok {
		return nil
	}
	if dist == DistConstant {
		if ft, ok := fieldTypeFromName(fieldType); !ok || !ft.IsSet() {
			valid = slices.DeleteFunc(slices.Clone(valid), func(k string) bool { return k == setOnlyParam })
		}
	}
	var unknown []string
	for k := range params {
		if !slices.Contains(valid, k) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	return strictjson.UnknownFieldErrorAliased(objectPath, unknown, valid, distributionParamAliases[dist])
}

// checkFieldParams checks field i's params (and, on a vector, every
// element override's) against the allowlist.
func checkFieldParams(i int, f FieldSpec) error {
	base := "fields[" + strconv.Itoa(i) + "]"
	if err := checkDistributionParams(base+".params", f.Distribution, f.Type, f.Params); err != nil {
		return err
	}
	if f.Vector == nil {
		return nil
	}
	for k, e := range f.Vector.Elements {
		dist := f.Distribution
		if e.Distribution != "" {
			dist = e.Distribution
		}
		if err := checkDistributionParams(base+".vector.elements["+strconv.Itoa(k)+"].params", dist, "f64", e.Params); err != nil {
			return err
		}
	}
	return nil
}

// paramReads is the test-only recording hook behind the declared-vs-read
// gate: while set, every params lookup reports (field, key). Production
// never sets it, so a lookup costs one atomic load.
var paramReads atomic.Pointer[func(field, key string)]

// paramLookup is THE params read: every param* helper and direct value
// lookup goes through it (TestDistributionParamsReadThroughLookup).
func paramLookup(name string, params map[string]any, key string) (any, bool) {
	if rec := paramReads.Load(); rec != nil {
		(*rec)(name, key)
	}
	v, ok := params[key]
	return v, ok
}
