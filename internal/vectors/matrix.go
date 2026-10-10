package vectors

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Matrix is one Request.Matrices spec after resolution: its effective
// name, its operator, its members (the vector it names, or its inline
// fields resolved by the vector rules) and its decoded parameters.
type Matrix struct {
	// Index is the spec's position in Request.Matrices.
	Index int
	// Name is the result name (MatrixSpec.EffectiveName).
	Name string
	// Type is the operator.
	Type types.MatrixType
	// Members are the axis members, with their labels and coercion.
	Members Resolved
	// ExplicitLabels reports whether the members carry caller-supplied
	// display labels (otherwise Members.Labels are the member names and
	// the result omits them).
	ExplicitLabels bool
	// Encoding is the effective Values layout.
	Encoding types.MatrixEncoding
	// Streamable and Mergeable are the spec-level answers
	// (types.MatrixSpec.Streamable / Mergeable — the type folded with
	// its params), copied at resolution so the engine, predict and the
	// routing gates read one rule. A non-streamable matrix is BUFFERED:
	// its slot keeps the admitted rows for a finalizer that needs them
	// all (a rank method).
	Streamable bool
	Mergeable  bool
	// DDOF is MAT_COVARIANCE's delta degrees of freedom (default 1).
	DDOF int
	// Pairwise reports params.missing "pairwise": each pair is computed
	// over the rows where both members are present. False is the
	// default "listwise": a row with any member null is dropped.
	Pairwise bool
	// MaxDropShare is params.max_drop_share (listwise only; no
	// default): when the share of filter-passing rows listwise drops
	// exceeds it, the result carries PULSE_MATRIX_LISTWISE_HEAVY_DROP.
	// Nil when unset.
	MaxDropShare *float64
	// Method is MAT_CORRELATION's params.method — CorrelationPearson
	// (the default), CorrelationSpearman or CorrelationKendall; "" on
	// every other type. A rank method is buffered and not mergeable
	// (Streamable / Mergeable false).
	Method string
	// TopPairs is MAT_CORRELATION's params.summary.top_pairs: the
	// number of strongest off-diagonal pairs the result lists under
	// vectors.top_pairs. 0 (the default) emits no summary.
	TopPairs int
}

// Missing-data modes (params.missing).
const (
	MissingListwise = "listwise"
	MissingPairwise = "pairwise"
)

// MissingModes returns the params.missing values, default first.
func MissingModes() []string { return []string{MissingListwise, MissingPairwise} }

// Correlation methods (MAT_CORRELATION params.method).
const (
	CorrelationPearson  = "pearson"
	CorrelationSpearman = "spearman"
	CorrelationKendall  = "kendall"
)

// CorrelationMethods returns the params.method values, default first.
func CorrelationMethods() []string {
	return []string{CorrelationPearson, CorrelationSpearman, CorrelationKendall}
}

// IsRankMethod reports whether method is a rank correlation (spearman
// or kendall): buffered, not mergeable, frequency weights only.
func IsRankMethod(method string) bool {
	return method == CorrelationSpearman || method == CorrelationKendall
}

// SpecMethod is spec's correlation method as the resolver reads it —
// params.method on MAT_CORRELATION (CorrelationPearson when absent),
// "" on every other type. Lenient: a params object that does not
// decode yields the default (the resolver refuses it elsewhere).
func SpecMethod(spec types.MatrixSpec) string {
	if spec.Type != types.MAT_CORRELATION {
		return ""
	}
	var p struct {
		Method *string `json:"method"`
	}
	if len(spec.Params) == 0 || json.Unmarshal(spec.Params, &p) != nil || p.Method == nil {
		return CorrelationPearson
	}
	return *p.Method
}

// missingParams are the missing-data knobs every matrix operator takes.
type missingParams struct {
	Missing      *string  `json:"missing"`
	MaxDropShare *float64 `json:"max_drop_share"`
}

// covarianceParams is MAT_COVARIANCE's params object. Summary is
// decoded only to refuse it with a pointed message: top_pairs ranks
// correlations, which a covariance (in the members' own units) is not.
type covarianceParams struct {
	DDOF    *int            `json:"ddof"`
	Summary json.RawMessage `json:"summary"`
	missingParams
}

// correlationParams is MAT_CORRELATION's params object: the method,
// the missing-data knobs and the summary block, so any other key is
// refused (strict decode).
type correlationParams struct {
	Method  *string        `json:"method"`
	Summary *summaryParams `json:"summary"`
	missingParams
}

// summaryParams is MAT_CORRELATION's params.summary block.
type summaryParams struct {
	// TopPairs is k: list the k strongest off-diagonal pairs by |r|.
	TopPairs *int `json:"top_pairs"`
}

// ResolveMatrices resolves req.Matrices against schema: first
// req.Vectors (Resolve — its refusal wins), then every matrix spec in
// request order. known reports whether a matrix type is offered (an
// instance-hidden type is unknown); nil admits exactly
// types.AllMatrixTypes(). The first refusal is returned:
//
//   - an unknown type, a spec setting both or neither of `vector` and
//     `fields`, an unknown `encoding`, a result name used twice, or a
//     malformed / out-of-range `params` — SERVICE_VALIDATION naming
//     `matrix` (the slot path) and `reason`;
//   - a `vector` no Request.Vectors entry defines — PULSE_VECTOR_UNKNOWN
//     (`vector`, `slot`, `defined`);
//   - inline `fields` — the vector rules (ResolveSpec at
//     "matrices[i].fields").
//
// The result is index-aligned with req.Matrices. Like Resolve it reads
// only the schema.
func ResolveMatrices(req *types.Request, schema *encoding.Schema, known func(types.MatrixType) bool) ([]Matrix, *errors.CodedError) {
	if req == nil {
		return nil, nil
	}
	resolved, verr := Resolve(req.Vectors, schema)
	if verr != nil {
		return nil, verr
	}
	if len(req.Matrices) == 0 {
		return nil, nil
	}
	if known == nil {
		known = builtinMatrixType
	}
	out := make([]Matrix, 0, len(req.Matrices))
	names := make(map[string]int, len(req.Matrices))
	for i := range req.Matrices {
		spec := req.Matrices[i]
		at := matrixPath(i)
		if !known(spec.Type) {
			return nil, matrixInvalid(at, "unknown_type",
				at+" references unknown matrix type: "+strconv.Quote(string(spec.Type)),
				map[string]any{"type": string(spec.Type)})
		}
		switch {
		case spec.Vector != "" && len(spec.Fields) > 0:
			return nil, matrixInvalid(at, "vector_and_fields",
				at+" sets both vector and fields; set exactly one", nil)
		case spec.Vector == "" && len(spec.Fields) == 0:
			return nil, matrixInvalid(at, "no_vector_or_fields",
				at+" sets neither vector nor fields; set exactly one", nil)
		}
		enc := spec.EffectiveEncoding()
		if !knownEncoding(enc) {
			return nil, matrixInvalid(at, "unknown_encoding",
				at+" has unknown encoding "+strconv.Quote(string(spec.Encoding)),
				map[string]any{"value": string(spec.Encoding), "valid": encodingNames()})
		}
		name := spec.EffectiveName()
		if first, dup := names[name]; dup {
			return nil, matrixInvalid(at, "duplicate_name",
				"matrix result name "+strconv.Quote(name)+" is used twice (matrices["+strconv.Itoa(first)+"] and "+at+")",
				map[string]any{"name": name, "indices": []int{first, i}})
		}
		names[name] = i

		m := Matrix{Index: i, Name: name, Type: spec.Type, Encoding: enc,
			Streamable: spec.Streamable(), Mergeable: spec.Mergeable()}
		if err := decodeMatrixParams(at, spec, &m); err != nil {
			return nil, err
		}

		if spec.Vector != "" {
			r, ok := Find(resolved, spec.Vector)
			if !ok {
				defined := make([]string, 0, len(resolved))
				for _, v := range resolved {
					defined = append(defined, v.Name)
				}
				return nil, errors.NewCodedErrorWithDetails(errors.PULSE_VECTOR_UNKNOWN,
					at+" names vector "+strconv.Quote(spec.Vector)+", which no vectors entry defines",
					map[string]any{"vector": spec.Vector, "slot": at + ".vector", "defined": defined})
			}
			m.Members = r
			m.ExplicitLabels = len(req.Vectors[indexOfVector(req.Vectors, spec.Vector)].Labels) > 0
		} else {
			r, err := ResolveSpec(at+".fields", types.VectorSpec{Name: name, Fields: spec.Fields}, schema)
			if err != nil {
				return nil, err
			}
			m.Members = r
		}
		out = append(out, m)
	}
	return out, nil
}

// decodeMatrixParams decodes the operator's params onto m. Unknown keys
// are refused, so a misspelt knob never silently takes its default.
func decodeMatrixParams(at string, spec types.MatrixSpec, m *Matrix) *errors.CodedError {
	raw := bytes.TrimSpace(spec.Params)
	empty := len(raw) == 0 || bytes.Equal(raw, []byte("null"))
	switch spec.Type {
	case types.MAT_COVARIANCE:
		m.DDOF = 1
	case types.MAT_CORRELATION:
		m.Method = CorrelationPearson
	}
	if empty {
		return nil
	}
	var miss missingParams
	switch spec.Type {
	case types.MAT_COVARIANCE:
		var p covarianceParams
		if err := decodeStrict(raw, &p); err != nil {
			return matrixInvalid(at, "bad_params", at+" params do not decode: "+err.Error(), nil)
		}
		if p.DDOF != nil {
			if *p.DDOF != 0 && *p.DDOF != 1 {
				return matrixInvalid(at, "bad_params", at+" params.ddof must be 0 or 1",
					map[string]any{"param": "ddof", "value": *p.DDOF})
			}
			m.DDOF = *p.DDOF
		}
		if s := bytes.TrimSpace(p.Summary); len(s) > 0 && !bytes.Equal(s, []byte("null")) {
			return matrixInvalid(at, "bad_params", at+" params.summary (top_pairs) applies to MAT_CORRELATION only; a covariance is in its members' own units, so pairs do not rank",
				map[string]any{"param": "summary", "type": string(types.MAT_COVARIANCE), "valid_types": []string{string(types.MAT_CORRELATION)}})
		}
		miss = p.missingParams
	case types.MAT_CORRELATION:
		var p correlationParams
		if err := decodeStrict(raw, &p); err != nil {
			return matrixInvalid(at, "bad_params", at+" params do not decode: "+err.Error(), nil)
		}
		if p.Method != nil {
			switch *p.Method {
			case CorrelationPearson, CorrelationSpearman, CorrelationKendall:
				m.Method = *p.Method
			default:
				return matrixInvalid(at, "bad_params", at+" params.method must be \"pearson\", \"spearman\" or \"kendall\"",
					map[string]any{"param": "method", "value": *p.Method, "valid": CorrelationMethods()})
			}
		}
		if p.Summary != nil {
			k := p.Summary.TopPairs
			if k == nil {
				return matrixInvalid(at, "bad_params", at+" params.summary must set top_pairs (a positive integer)",
					map[string]any{"param": "summary.top_pairs"})
			}
			if *k < 1 {
				return matrixInvalid(at, "bad_params", at+" params.summary.top_pairs must be a positive integer",
					map[string]any{"param": "summary.top_pairs", "value": *k})
			}
			m.TopPairs = *k
		}
		miss = p.missingParams
	}
	return decodeMissing(at, miss, m)
}

// decodeMissing applies params.missing / params.max_drop_share: missing
// is "listwise" (default) or "pairwise"; max_drop_share is a share in
// [0, 1] and applies to listwise only (pairwise drops no whole row).
func decodeMissing(at string, p missingParams, m *Matrix) *errors.CodedError {
	if p.Missing != nil {
		switch *p.Missing {
		case MissingListwise:
		case MissingPairwise:
			m.Pairwise = true
		default:
			return matrixInvalid(at, "bad_params", at+" params.missing must be \"listwise\" or \"pairwise\"",
				map[string]any{"param": "missing", "value": *p.Missing, "valid": MissingModes()})
		}
	}
	if p.MaxDropShare != nil {
		s := *p.MaxDropShare
		if !(s >= 0 && s <= 1) {
			return matrixInvalid(at, "bad_params", at+" params.max_drop_share must be a share in [0, 1]",
				map[string]any{"param": "max_drop_share", "value": s})
		}
		if m.Pairwise {
			return matrixInvalid(at, "bad_params", at+" params.max_drop_share applies to missing \"listwise\" only; pairwise drops no whole row",
				map[string]any{"param": "max_drop_share", "missing": MissingPairwise})
		}
		v := s
		m.MaxDropShare = &v
	}
	return nil
}

// decodeStrict decodes raw into v, refusing unknown keys.
func decodeStrict(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// MatrixMembers returns the union of every member req's matrix specs
// read — a vector spec's vector members and an inline spec's fields —
// and whether every one resolved (projection decodes wide otherwise).
func MatrixMembers(req *types.Request, schema *encoding.Schema) (members []string, ok bool) {
	if req == nil || len(req.Matrices) == 0 {
		return nil, true
	}
	ok = true
	for i, spec := range req.Matrices {
		if spec.Vector != "" || len(spec.Fields) == 0 {
			continue // vector members ride Members(req.Vectors)
		}
		r, err := ResolveSpec(matrixPath(i)+".fields", types.VectorSpec{Name: spec.EffectiveName(), Fields: spec.Fields}, schema)
		if err != nil {
			ok = false
			continue
		}
		members = append(members, r.Members...)
	}
	return members, ok
}

func builtinMatrixType(t types.MatrixType) bool {
	for _, k := range types.AllMatrixTypes() {
		if k == t {
			return true
		}
	}
	return false
}

func knownEncoding(e types.MatrixEncoding) bool {
	for _, k := range types.AllMatrixEncodings() {
		if k == e {
			return true
		}
	}
	return false
}

func encodingNames() []string {
	all := types.AllMatrixEncodings()
	out := make([]string, len(all))
	for i, e := range all {
		out[i] = string(e)
	}
	return out
}

func indexOfVector(specs []types.VectorSpec, name string) int {
	for i, s := range specs {
		if s.Name == name {
			return i
		}
	}
	return -1
}

func matrixPath(i int) string { return "matrices[" + strconv.Itoa(i) + "]" }

func matrixInvalid(at, reason, msg string, extra map[string]any) *errors.CodedError {
	details := map[string]any{"matrix": at, "reason": reason}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION, msg, details)
}
