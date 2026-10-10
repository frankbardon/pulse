package vectors

import (
	"bytes"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"

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
	// Controls is MAT_PARTIAL_CORRELATION's params.control named list,
	// in the caller's order: nil is "all" (each pair controls for every
	// other member). A control may be a member (it leaves the output
	// axis) or any other numeric field (it joins the fold: Extra).
	Controls []string
	// Extra are the control fields outside the members, in params
	// order: the slot folds them after the members (Columns), so they
	// join the co-moment and the missing-data mode.
	Extra []string
	// Output are the output axis positions in Columns — the members
	// that are not controls, in axis order. Nil means every member
	// (Columns is then exactly the members).
	Output []int
	// Repair is a decomposition operator's params.repair: "" (refuse a
	// non-PSD input with PULSE_MATRIX_NOT_PSD) or RepairNearest.
	Repair string
	// Reverse are MAT_RELIABILITY's reverse-keyed items as member
	// positions, ascending (params.reverse, de-duplicated by name): the
	// slot folds x' = ScaleMin + ScaleMax − x for each before the
	// co-moment. Nil when nothing is reversed.
	Reverse []int
	// HasScale reports a declared battery-wide params.scale_min /
	// scale_max (both or neither; required with a reverse list): every
	// present member value must lie in [ScaleMin, ScaleMax], or the run
	// is refused with PROCESSING_CONFIG — never clamped, never inferred
	// from the data.
	HasScale           bool
	ScaleMin, ScaleMax float64
	// reverseNames carries params.reverse from decode to
	// resolveReliability (members resolve in between).
	reverseNames []string
	// On is MAT_PCA's params.on: PCAOnCorrelation (the default) or
	// PCAOnCovariance; "" on every other type.
	On string
	// Components is MAT_PCA's retention rule (params.components).
	Components PCAComponents
	// Center is MAT_COLLINEARITY's params.center: false (the default)
	// runs Belsley's diagnostics on the scaled, UNCENTERED predictors
	// with an intercept column; true on the scaled, centered ones (the
	// predictors' correlation, no intercept).
	Center bool
}

// MAT_PCA params.on values.
const (
	PCAOnCorrelation = "correlation"
	PCAOnCovariance  = "covariance"
)

// PCAOnValues returns the params.on values, default first.
func PCAOnValues() []string { return []string{PCAOnCorrelation, PCAOnCovariance} }

// MAT_PCA retention rules (PCAComponents.Rule).
const (
	// PCAComponentsFixed keeps exactly K components (an integer
	// params.components, 1 ≤ K ≤ p).
	PCAComponentsFixed = "fixed"
	// PCAComponentsKaiser keeps the components whose eigenvalue
	// exceeds 1 (params.components "kaiser"; the default on a
	// correlation, refused on a covariance, whose eigenvalues are in
	// the members' units).
	PCAComponentsKaiser = "kaiser"
	// PCAComponentsVariance keeps the fewest leading components whose
	// cumulative explained-variance share reaches Share
	// (params.components {"variance": share}, 0 < share ≤ 1).
	PCAComponentsVariance = "variance"
)

// PCAComponents is MAT_PCA's resolved params.components.
type PCAComponents struct {
	Rule  string
	K     int
	Share float64
}

// RepairNearest is params.repair "nearest": a non-PSD input matrix is
// replaced by its nearest correlation matrix (Higham 2002, alternating
// projections with Dykstra's correction), scaled back to the input's
// diagonal, with a PULSE_MATRIX_NOT_PSD warning carrying the Frobenius
// adjustment.
const RepairNearest = "nearest"

// ControlAll is MAT_PARTIAL_CORRELATION's default params.control.
const ControlAll = "all"

// Columns returns the fields the slot folds, in fold order: the
// members, then any control outside them (Extra). Every co-moment
// index (N, PairN, the warnings' member names) is a Columns index.
func (m Matrix) Columns() []string {
	if len(m.Extra) == 0 {
		return m.Members.Members
	}
	out := make([]string, 0, len(m.Members.Members)+len(m.Extra))
	out = append(out, m.Members.Members...)
	return append(out, m.Extra...)
}

// OutputIndices returns the output axis as Columns positions: Output,
// or every member when Output is nil.
func (m Matrix) OutputIndices() []int {
	if m.Output != nil {
		return m.Output
	}
	out := make([]int, len(m.Members.Members))
	for i := range out {
		out[i] = i
	}
	return out
}

// OutputMembers returns the result's axis keys (OutputIndices'
// fields) and their labels.
func (m Matrix) OutputMembers() (members, labels []string) {
	idx := m.OutputIndices()
	members = make([]string, len(idx))
	labels = make([]string, len(idx))
	for k, i := range idx {
		members[k] = m.Members.Members[i]
		if i < len(m.Members.Labels) {
			labels[k] = m.Members.Labels[i]
		} else {
			labels[k] = members[k]
		}
	}
	return members, labels
}

// Decomposition reports whether the matrix's operator decomposes (or
// inverts) its input matrix, so a non-PSD input is a FATAL
// PULSE_MATRIX_NOT_PSD unless params.repair is "nearest" — the shared
// guard (processing.guardPSD). Every other operator keeps the
// detection-only warning.
func (m Matrix) Decomposition() bool { return IsDecomposition(m.Type) }

// IsDecomposition reports whether t is a decomposition operator (see
// Matrix.Decomposition): MAT_PARTIAL_CORRELATION, MAT_PCA (it
// eigen-decomposes its correlation or covariance), MAT_COLLINEARITY (it
// inverts and eigen-decomposes the predictors' correlation), and
// MAT_RELIABILITY,
// whose McDonald's omega factors the inter-item correlation (there the
// guard's refusal nulls omega with a warning instead of failing the
// matrix: alpha needs no PSD input).
func IsDecomposition(t types.MatrixType) bool {
	switch t {
	case types.MAT_PARTIAL_CORRELATION, types.MAT_RELIABILITY, types.MAT_PCA, types.MAT_COLLINEARITY:
		return true
	}
	return false
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

// partialParams is MAT_PARTIAL_CORRELATION's params object: the
// control set ("all" or a field list, decoded by decodeControl), the
// repair choice and the missing-data knobs; any other key is refused.
type partialParams struct {
	Control json.RawMessage `json:"control"`
	Repair  *string         `json:"repair"`
	missingParams
}

// reliabilityParams is MAT_RELIABILITY's params object: the
// reverse-keyed items, the battery-wide scale range, the repair choice
// and the missing-data knobs; any other key is refused.
type reliabilityParams struct {
	Reverse  []string `json:"reverse"`
	ScaleMin *float64 `json:"scale_min"`
	ScaleMax *float64 `json:"scale_max"`
	Repair   *string  `json:"repair"`
	missingParams
}

// pcaParams is MAT_PCA's params object: the input matrix, the
// retention rule (an integer, "kaiser" or {"variance": share}, decoded
// by decodeComponents), the repair choice and the missing-data knobs;
// any other key is refused.
type pcaParams struct {
	On         *string         `json:"on"`
	Components json.RawMessage `json:"components"`
	Repair     *string         `json:"repair"`
	missingParams
}

// collinearityParams is MAT_COLLINEARITY's params object: the Belsley
// variant (center), the repair choice and the missing-data knobs; any
// other key is refused. The members are the predictors only — there is
// no response key.
type collinearityParams struct {
	Center *bool   `json:"center"`
	Repair *string `json:"repair"`
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
		// Controls resolve after the members (below): a control is a
		// member or another numeric field.

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
		if err := resolveControls(at, schema, &m); err != nil {
			return nil, err
		}
		if err := resolveReliability(at, spec, &m); err != nil {
			return nil, err
		}
		if err := resolvePCA(at, &m); err != nil {
			return nil, err
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
	case types.MAT_PCA:
		m.On = PCAOnCorrelation
		m.Components = PCAComponents{Rule: PCAComponentsKaiser}
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
	case types.MAT_PARTIAL_CORRELATION:
		var p partialParams
		if err := decodeStrict(raw, &p); err != nil {
			return matrixInvalid(at, "bad_params", at+" params do not decode: "+err.Error(), nil)
		}
		controls, err := decodeControl(at, p.Control)
		if err != nil {
			return err
		}
		m.Controls = controls
		if err := decodeRepair(at, p.Repair, m); err != nil {
			return err
		}
		miss = p.missingParams
	case types.MAT_RELIABILITY:
		var p reliabilityParams
		if err := decodeStrict(raw, &p); err != nil {
			return matrixInvalid(at, "bad_params", at+" params do not decode: "+err.Error(), nil)
		}
		if err := decodeScale(at, p, m); err != nil {
			return err
		}
		if err := decodeRepair(at, p.Repair, m); err != nil {
			return err
		}
		miss = p.missingParams
	case types.MAT_PCA:
		var p pcaParams
		if err := decodeStrict(raw, &p); err != nil {
			return matrixInvalid(at, "bad_params", at+" params do not decode: "+err.Error(), nil)
		}
		if err := decodePCA(at, p, m); err != nil {
			return err
		}
		if err := decodeRepair(at, p.Repair, m); err != nil {
			return err
		}
		miss = p.missingParams
	case types.MAT_COLLINEARITY:
		var p collinearityParams
		if err := decodeStrict(raw, &p); err != nil {
			return matrixInvalid(at, "bad_params", at+" params do not decode: "+err.Error(), nil)
		}
		if p.Center != nil {
			m.Center = *p.Center
		}
		if err := decodeRepair(at, p.Repair, m); err != nil {
			return err
		}
		miss = p.missingParams
	}
	return decodeMissing(at, miss, m)
}

// decodePCA applies MAT_PCA's params.on and params.components: on
// "correlation" (default) or "covariance"; components an integer k ≥ 1
// (checked against p in resolvePCA), "kaiser" (the correlation
// default) or {"variance": share} with 0 < share ≤ 1. On a covariance
// components is required and "kaiser" is refused — λ > 1 has no
// meaning in the members' units. Every refusal is bad_params.
func decodePCA(at string, p pcaParams, m *Matrix) *errors.CodedError {
	m.On = PCAOnCorrelation
	if p.On != nil {
		switch *p.On {
		case PCAOnCorrelation, PCAOnCovariance:
			m.On = *p.On
		default:
			return matrixInvalid(at, "bad_params", at+" params.on must be \"correlation\" or \"covariance\"",
				map[string]any{"param": "on", "value": *p.On, "valid": PCAOnValues()})
		}
	}
	raw := bytes.TrimSpace(p.Components)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		if m.On == PCAOnCovariance {
			return matrixInvalid(at, "bad_params", at+" params.components is required on a covariance (an integer k or {\"variance\": share}); \"kaiser\" applies to a correlation only",
				map[string]any{"param": "components", "on": m.On})
		}
		m.Components = PCAComponents{Rule: PCAComponentsKaiser}
		return nil
	}
	bad := func(msg string, value any) *errors.CodedError {
		return matrixInvalid(at, "bad_params", at+" params.components "+msg,
			map[string]any{"param": "components", "value": value, "valid": []string{"a positive integer", PCAComponentsKaiser, "{\"variance\": share}"}})
	}
	switch raw[0] {
	case '"':
		var str string
		if err := json.Unmarshal(raw, &str); err != nil || str != PCAComponentsKaiser {
			return bad("must be a positive integer, \"kaiser\" or {\"variance\": share}", string(raw))
		}
		if m.On == PCAOnCovariance {
			return matrixInvalid(at, "bad_params", at+" params.components \"kaiser\" (eigenvalue > 1) applies to a correlation only: a covariance's eigenvalues are in the members' units; set an integer k or {\"variance\": share}",
				map[string]any{"param": "components", "value": PCAComponentsKaiser, "on": m.On})
		}
		m.Components = PCAComponents{Rule: PCAComponentsKaiser}
	case '{':
		var v struct {
			Variance *float64 `json:"variance"`
		}
		if err := decodeStrict(raw, &v); err != nil || v.Variance == nil {
			return bad("object must be {\"variance\": share}", string(raw))
		}
		if s := *v.Variance; !(s > 0 && s <= 1) {
			return bad("variance share must be in (0, 1]", s)
		}
		m.Components = PCAComponents{Rule: PCAComponentsVariance, Share: *v.Variance}
	default:
		var f float64
		if err := json.Unmarshal(raw, &f); err != nil || f != math.Trunc(f) || f < 1 || f > math.MaxInt32 {
			return bad("must be a positive integer, \"kaiser\" or {\"variance\": share}", string(raw))
		}
		m.Components = PCAComponents{Rule: PCAComponentsFixed, K: int(f)}
	}
	return nil
}

// resolvePCA refuses a MAT_PCA integer params.components above the
// member count (bad_params): a p-member battery has p components.
func resolvePCA(at string, m *Matrix) *errors.CodedError {
	if m.Type != types.MAT_PCA || m.Components.Rule != PCAComponentsFixed {
		return nil
	}
	if p := len(m.Members.Members); m.Components.K > p {
		return matrixInvalid(at, "bad_params", at+" params.components "+strconv.Itoa(m.Components.K)+" exceeds the "+strconv.Itoa(p)+" members (a battery has as many components as members)",
			map[string]any{"param": "components", "value": m.Components.K, "members": p})
	}
	return nil
}

// decodeRepair applies a decomposition operator's params.repair: absent
// or "nearest".
func decodeRepair(at string, repair *string, m *Matrix) *errors.CodedError {
	if repair == nil {
		return nil
	}
	if *repair != RepairNearest {
		return matrixInvalid(at, "bad_params", at+" params.repair must be \"nearest\"",
			map[string]any{"param": "repair", "value": *repair, "valid": []string{RepairNearest}})
	}
	m.Repair = RepairNearest
	return nil
}

// decodeScale applies MAT_RELIABILITY's params.scale_min / scale_max
// (both or neither, finite, min < max) and keeps the reverse names for
// resolveReliability. A reverse list without a range, half a range, or
// a range that is not a finite min < max is PROCESSING_CONFIG: the
// reversal x' = min + max − x needs the declared range, which is never
// inferred from the data.
func decodeScale(at string, p reliabilityParams, m *Matrix) *errors.CodedError {
	switch {
	case p.ScaleMin == nil && p.ScaleMax == nil:
		if len(p.Reverse) > 0 {
			return scaleConfig(at, at+" params.reverse needs the battery's scale range: set params.scale_min and params.scale_max (the range is never inferred from the data)",
				map[string]any{"param": "scale_min", "reverse": append([]string(nil), p.Reverse...)})
		}
		return nil
	case p.ScaleMin == nil || p.ScaleMax == nil:
		return scaleConfig(at, at+" params.scale_min and params.scale_max are set together", map[string]any{"param": "scale_min"})
	}
	lo, hi := *p.ScaleMin, *p.ScaleMax
	if math.IsNaN(lo) || math.IsInf(lo, 0) || math.IsNaN(hi) || math.IsInf(hi, 0) || !(lo < hi) {
		return scaleConfig(at, at+" params.scale_min must be below params.scale_max",
			map[string]any{"param": "scale_min", "scale_min": lo, "scale_max": hi})
	}
	m.HasScale, m.ScaleMin, m.ScaleMax = true, lo, hi
	m.reverseNames = p.Reverse
	return nil
}

// scaleConfig is a MAT_RELIABILITY scale-range refusal: PROCESSING_CONFIG
// naming the matrix slot.
func scaleConfig(at, msg string, extra map[string]any) *errors.CodedError {
	details := map[string]any{"matrix": at}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG, msg, details)
}

// ReliabilityMinItems is the fewest members MAT_RELIABILITY takes:
// alpha compares at least two items.
const ReliabilityMinItems = 2

// resolveReliability places MAT_RELIABILITY's reverse names on the
// resolved members (each must be a member, listed once) and refuses a
// battery of fewer than ReliabilityMinItems members — both
// SERVICE_VALIDATION bad_params.
func resolveReliability(at string, spec types.MatrixSpec, m *Matrix) *errors.CodedError {
	if spec.Type != types.MAT_RELIABILITY {
		return nil
	}
	members := m.Members.Members
	if len(members) < ReliabilityMinItems {
		return matrixInvalid(at, "bad_params", at+" MAT_RELIABILITY needs at least 2 items; alpha compares items with one another",
			map[string]any{"members": append([]string(nil), members...), "min_items": ReliabilityMinItems})
	}
	names := m.reverseNames
	m.reverseNames = nil
	if len(names) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(names))
	for _, r := range names {
		if seen[r] {
			return matrixInvalid(at, "bad_params", at+" params.reverse names "+strconv.Quote(r)+" twice",
				map[string]any{"param": "reverse", "value": r})
		}
		seen[r] = true
		pos := -1
		for i, f := range members {
			if f == r {
				pos = i
				break
			}
		}
		if pos < 0 {
			return matrixInvalid(at, "bad_params", at+" params.reverse names "+strconv.Quote(r)+", which is not an item of the matrix",
				map[string]any{"param": "reverse", "value": r, "members": append([]string(nil), members...)})
		}
		m.Reverse = append(m.Reverse, pos)
	}
	slices.Sort(m.Reverse)
	return nil
}

// decodeControl decodes params.control: absent, null or "all" is nil
// (control for every other member); otherwise a non-empty list of
// distinct, non-empty field names (no glob: a control names one field).
func decodeControl(at string, raw json.RawMessage) ([]string, *errors.CodedError) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var all string
	if json.Unmarshal(raw, &all) == nil {
		if all == ControlAll {
			return nil, nil
		}
		return nil, matrixInvalid(at, "bad_params", at+" params.control must be \"all\" or a list of field names",
			map[string]any{"param": "control", "value": all})
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, matrixInvalid(at, "bad_params", at+" params.control must be \"all\" or a list of field names",
			map[string]any{"param": "control"})
	}
	if len(list) == 0 {
		return nil, matrixInvalid(at, "bad_params", at+" params.control lists no field; omit it (or use \"all\") to control for every other member",
			map[string]any{"param": "control"})
	}
	seen := make(map[string]bool, len(list))
	for _, c := range list {
		switch {
		case c == "" || isGlob(c):
			return nil, matrixInvalid(at, "bad_params", at+" params.control entries are field names (no glob)",
				map[string]any{"param": "control", "value": c})
		case seen[c]:
			return nil, matrixInvalid(at, "bad_params", at+" params.control names "+strconv.Quote(c)+" twice",
				map[string]any{"param": "control", "value": c})
		}
		seen[c] = true
	}
	return list, nil
}

// controlSlot rewrites a ResolveSpec refusal's "slot" detail
// ("…params.control.fields[k]", k indexing the outside controls) to the
// caller's own path, "…params.control[j]" with j the entry's position
// in params.control. The error is copied, never mutated.
func controlSlot(err *errors.CodedError, at string, controls, outside []string) *errors.CodedError {
	prefix := at + ".params.control.fields["
	slot, ok := err.Details["slot"].(string)
	if !ok || !strings.HasPrefix(slot, prefix) {
		return err
	}
	k, convErr := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(slot, prefix), "]"))
	if convErr != nil || k < 0 || k >= len(outside) {
		return err
	}
	j := 0
	for i, c := range controls {
		if c == outside[k] {
			j = i
			break
		}
	}
	details := make(map[string]any, len(err.Details))
	for key, v := range err.Details {
		details[key] = v
	}
	details["slot"] = at + ".params.control[" + strconv.Itoa(j) + "]"
	return errors.NewCodedErrorWithDetails(err.Code, err.Message, details)
}

// resolveControls places m.Controls against the resolved members: a
// control that is a member leaves the output axis; any other must be a
// numeric field the vector rules admit (ResolveSpec at
// "matrices[i].params.control": the field-reference and member-type
// refusals) and joins the fold as Extra. At least one member must stay
// on the output axis.
func resolveControls(at string, schema *encoding.Schema, m *Matrix) *errors.CodedError {
	if m.Controls == nil {
		return nil
	}
	members := m.Members.Members
	isControl := make(map[string]bool, len(m.Controls))
	var outside []string
	for _, c := range m.Controls {
		isControl[c] = true
		inside := false
		for _, f := range members {
			if f == c {
				inside = true
				break
			}
		}
		if !inside {
			outside = append(outside, c)
		}
	}
	if len(outside) > 0 {
		r, err := ResolveSpec(at+".params.control", types.VectorSpec{Name: m.Name, Fields: outside}, schema)
		if err != nil {
			return controlSlot(err, at, m.Controls, outside)
		}
		m.Extra = r.Members
	}
	m.Output = []int{}
	for i, f := range members {
		if !isControl[f] {
			m.Output = append(m.Output, i)
		}
	}
	if len(m.Output) == 0 {
		return matrixInvalid(at, "bad_params", at+" params.control names every member; at least one member must stay uncontrolled",
			map[string]any{"param": "control", "members": append([]string(nil), members...)})
	}
	return nil
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

// MatrixMembers returns the union of every field req's matrix specs
// read beyond Request.Vectors — an inline spec's fields and a
// MAT_PARTIAL_CORRELATION params.control entry (a control outside the
// members joins the fold) — and whether every one resolved (projection
// decodes wide otherwise).
func MatrixMembers(req *types.Request, schema *encoding.Schema) (members []string, ok bool) {
	if req == nil || len(req.Matrices) == 0 {
		return nil, true
	}
	ok = true
	for i, spec := range req.Matrices {
		if spec.Type == types.MAT_PARTIAL_CORRELATION {
			members, ok = appendControlFields(members, ok, matrixPath(i), spec, schema)
		}
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

// appendControlFields appends spec's params.control fields that exist
// in the schema (a member repeated here is harmless: projection
// de-duplicates); a malformed control, or one the schema lacks, clears
// ok.
func appendControlFields(members []string, ok bool, at string, spec types.MatrixSpec, schema *encoding.Schema) ([]string, bool) {
	var p struct {
		Control json.RawMessage `json:"control"`
	}
	if len(spec.Params) == 0 || json.Unmarshal(spec.Params, &p) != nil {
		return members, ok
	}
	controls, err := decodeControl(at, p.Control)
	if err != nil {
		return members, false
	}
	for _, c := range controls {
		if schema == nil || schema.Field(c) == nil {
			ok = false
			continue
		}
		members = append(members, c)
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
