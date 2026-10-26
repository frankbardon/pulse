package vectors

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// Vector-input groupers — the types.Group Vector / Fields slots,
// resolved by the one leaf predict (the field-reference walk), the
// runtime and projection all call (the Test / Attribute Vector / Fields
// mirror).
//
// Rules (the contract), per grouper slot ("groups[i]",
// "crosstab.rows[i]", "crosstab.columns[i]"):
//
//   - A built-in vector-input type (VectorGroupTypes) reads exactly one
//     of vector and fields; field is refused (it reads no scalar
//     column), and so are both or neither of vector and fields.
//   - A vector no Request.Vectors entry defines is PULSE_VECTOR_UNKNOWN
//     (`vector`, `slot`, `defined`); inline fields resolve by the vector
//     rules (ResolveSpec at "<slot>.fields"). Members are source columns
//     only, never an attribute's label.
//   - Params are strict (unknown keys refused): GROUP_KMEANS reads
//     KMeansParams (k, seed, max_iter, n_init, standardize, missing —
//     "listwise" only).
//   - Any OTHER built-in grouper refuses a vector input.
//   - A registered extension grouper (ext) that sets a vector input has
//     its member set judged by the same member rules: vector XOR fields
//     (vector_and_fields), the named vector (PULSE_VECTOR_UNKNOWN) and
//     the inline fields (ResolveSpec: existence, numeric type,
//     duplicates). field and params are its own business. Its factory,
//     like a built-in's, receives the resolved literal members
//     (GroupInput.Extension; its field is kept as written).
//   - The bucket key column of a built-in vector-input grouper is
//     GroupKeyField: "<TYPE>_<vector>", or "<TYPE>_<first fields entry
//     as written>".
//
// Shape refusals are SERVICE_VALIDATION naming `group` (the slot path)
// and `reason`.

// VectorGroupTypes returns the built-in group types that read their
// columns from Vector / Fields, in AllGroupTypes order.
func VectorGroupTypes() []types.GroupType {
	return []types.GroupType{types.GROUP_KMEANS}
}

// IsVectorGroup reports whether t is a built-in vector-input group type.
func IsVectorGroup(t types.GroupType) bool {
	return slices.Contains(VectorGroupTypes(), t)
}

// HasGroupVectorInput reports whether g sets any vector-input slot.
func HasGroupVectorInput(g *types.Group) bool {
	return g != nil && (g.Vector != "" || len(g.Fields) > 0)
}

// GroupKeyField is the column a grouper slot whose route is typ writes
// its bucket key under (the Data row key, the crosstab axis field): the
// slot's Field for every grouper except a vector-input one, whose key
// column is "<TYPE>_<vector>" or "<TYPE>_<first fields entry as
// written>".
func GroupKeyField(typ types.GroupType, g *types.Group) string {
	if g == nil {
		return ""
	}
	if !IsVectorGroup(typ) || g.Field != "" {
		return g.Field
	}
	if g.Vector != "" {
		return string(g.Type) + "_" + g.Vector
	}
	first := ""
	if len(g.Fields) > 0 {
		first = g.Fields[0]
	}
	return string(g.Type) + "_" + first
}

// GroupInput is one grouper slot's vector input after resolution. The
// zero value (Has false) is a grouper that reads no vector.
type GroupInput struct {
	// Slot is the slot path ("groups[0]", "crosstab.rows[1]", ...).
	Slot string
	// Members is the member set (axis order).
	Members Resolved
	// KMeans is GROUP_KMEANS's decoded params (zero on an extension).
	KMeans KMeansConfig
	// Extension reports the slot is a registered extension grouper: its
	// members are resolved, its params and key column its own.
	Extension bool
}

// Has reports whether the slot resolved a vector input.
func (in GroupInput) Has() bool { return len(in.Members.Members) > 0 }

// GroupSlot is one grouper slot of a request with its path.
type GroupSlot struct {
	Path  string
	Group *types.Group
}

// GroupSlots lists req's grouper slots in walk order: groups, then the
// crosstab rows, then the crosstab columns.
func GroupSlots(req *types.Request) []GroupSlot {
	if req == nil {
		return nil
	}
	var out []GroupSlot
	add := func(prefix string, gs []*types.Group) {
		for i, g := range gs {
			if g != nil {
				out = append(out, GroupSlot{Path: prefix + "[" + strconv.Itoa(i) + "]", Group: g})
			}
		}
	}
	add("groups", req.Groups)
	if ct := req.Crosstab; ct != nil {
		add("crosstab.rows", ct.Rows)
		add("crosstab.columns", ct.Columns)
	}
	return out
}

// ResolveGroups resolves every grouper slot's vector input (GroupSlots
// order) against schema (Request.Vectors first, like ResolveTests).
// route maps a group type to the built-in it executes as — the
// instance's view, where a hidden type routes to an unknown name; nil
// is the identity. ext reports whether a routed type that is not a
// built-in is a registered extension grouper (its member set is then
// judged); nil means none is. The result is keyed by the slot's
// *types.Group and holds only the slots that resolved a vector input;
// the first refusal is returned. Like Resolve it reads only the schema.
// cat is the site's bound-model catalog, read by the windows-first
// decision (ResolveRequest; nil none).
func ResolveGroups(req *types.Request, schema *encoding.Schema, cat model.Catalog, route func(types.GroupType) types.GroupType, ext func(string) bool) (map[*types.Group]GroupInput, *errors.CodedError) {
	slots := GroupSlots(req)
	if len(slots) == 0 {
		return nil, nil
	}
	if route == nil {
		route = func(t types.GroupType) types.GroupType { return t }
	}
	resolved, verr := ResolveRequest(req, schema, cat)
	if verr != nil {
		return nil, verr
	}
	var out map[*types.Group]GroupInput
	for _, s := range slots {
		typ := route(s.Group.Type)
		var in GroupInput
		var err *errors.CodedError
		if isExtension(ext, string(typ), slices.Contains(types.AllGroupTypes(), typ)) {
			var m Resolved
			m, err = resolveExtensionMembers(s.Path, s.Group.Vector, s.Group.Fields, req.Vectors, resolved, schema, groupInvalid)
			in = GroupInput{Slot: s.Path, Members: m, Extension: true}
		} else {
			in, err = resolveGroup(s.Path, s.Group, typ, req.Vectors, resolved, schema)
		}
		if err != nil {
			return nil, err
		}
		if in.Has() {
			if out == nil {
				out = map[*types.Group]GroupInput{}
			}
			out[s.Group] = in
		}
	}
	return out, nil
}

// resolveGroup resolves one slot whose built-in route is typ.
func resolveGroup(at string, g *types.Group, typ types.GroupType, specs []types.VectorSpec, resolved []Resolved, schema *encoding.Schema) (GroupInput, *errors.CodedError) {
	name := string(g.Type)
	if !IsVectorGroup(typ) {
		if HasGroupVectorInput(g) && slices.Contains(types.AllGroupTypes(), typ) {
			return GroupInput{}, groupInvalid(at, "vector_not_accepted",
				at+" ("+name+") groups by its field column, not a vector; drop vector / fields",
				map[string]any{"type": name})
		}
		return GroupInput{}, nil
	}
	if g.Field != "" {
		return GroupInput{}, groupInvalid(at, "field_on_vector_group",
			at+" ("+name+") reads its columns from vector or fields, not field",
			map[string]any{"type": name, "field": g.Field})
	}
	switch {
	case g.Vector != "" && len(g.Fields) > 0:
		return GroupInput{}, groupInvalid(at, "vector_and_fields", at+" sets both vector and fields; set exactly one", nil)
	case g.Vector == "" && len(g.Fields) == 0:
		return GroupInput{}, groupInvalid(at, "no_vector_or_fields", at+" ("+name+") sets neither vector nor fields; set exactly one", nil)
	}
	cfg, err := KMeansParams(at, g.Params)
	if err != nil {
		return GroupInput{}, err
	}
	var members Resolved
	if g.Vector == "" {
		members, err = ResolveSpec(at+".fields", types.VectorSpec{Fields: g.Fields}, schema)
	} else {
		members, err = findVector(at, "vector", g.Vector, specs, resolved)
	}
	if err != nil {
		return GroupInput{}, err
	}
	return GroupInput{Slot: at, Members: members, KMeans: cfg}, nil
}

// GROUP_KMEANS params defaults.
const (
	// KMeansDefaultMaxIter is params.max_iter's default: the Lloyd
	// iteration cap per start.
	KMeansDefaultMaxIter = 100
	// KMeansDefaultNInit is params.n_init's default: the number of
	// seeded k-means++ starts; the lowest within-SS wins.
	KMeansDefaultNInit = 10
	// KMeansDefaultSeed is the seed params.seed 0 (or absent) stands
	// for — fixed, never time-derived.
	KMeansDefaultSeed int64 = 20251011
)

// KMeansConfig is GROUP_KMEANS's decoded params.
type KMeansConfig struct {
	// K is the cluster count (≥ 2).
	K int
	// Seed is the PRNG seed; params.seed 0 resolves to KMeansDefaultSeed.
	Seed int64
	// MaxIter is the Lloyd iteration cap per start (≥ 1).
	MaxIter int
	// NInit is the number of k-means++ starts (≥ 1).
	NInit int
	// Standardize z-scores every member over the clustered rows first.
	Standardize bool
}

// KMeansParams decodes GROUP_KMEANS's params: k (required, an integer
// ≥ 2), seed (int64; 0 = the fixed default), max_iter (≥ 1, default
// 100), n_init (≥ 1, default 10), standardize (default true) and
// missing ("listwise" only). fit is accepted and left to
// internal/groupfit. Unknown keys are refused. at is the slot
// path. Predict and the runtime factory both call it.
func KMeansParams(at string, raw json.RawMessage) (KMeansConfig, *errors.CodedError) {
	cfg := KMeansConfig{Seed: KMeansDefaultSeed, MaxIter: KMeansDefaultMaxIter, NInit: KMeansDefaultNInit, Standardize: true}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return KMeansConfig{}, groupInvalid(at, "k_required", at+" GROUP_KMEANS needs params.k, the cluster count (an integer ≥ 2)",
			map[string]any{"param": "k"})
	}
	var p struct {
		K           *json.Number `json:"k"`
		Seed        *json.Number `json:"seed"`
		MaxIter     *json.Number `json:"max_iter"`
		NInit       *json.Number `json:"n_init"`
		Standardize *bool        `json:"standardize"`
		Missing     *string      `json:"missing"`
		// Fit is params.fit, read and refused by internal/groupfit
		// (shared with GROUP_QUANTILE); accepted here so the strict
		// decode does not refuse the key.
		Fit json.RawMessage `json:"fit"`
	}
	if err := paramread.DecodeStrict(paramread.Grouper, string(types.GROUP_KMEANS), raw, &p); err != nil {
		return KMeansConfig{}, groupInvalid(at, "bad_params",
			at+" GROUP_KMEANS params: "+err.Error()+" (keys: k, seed, max_iter, n_init, standardize, missing, fit)", nil)
	}
	intParam := func(name string, n *json.Number, min int) (int, *errors.CodedError) {
		v, err := strconv.ParseInt(n.String(), 10, 32)
		if err != nil || int(v) < min {
			return 0, groupInvalid(at, "bad_params", at+" params."+name+" must be an integer ≥ "+strconv.Itoa(min),
				map[string]any{"param": name, "value": n.String()})
		}
		return int(v), nil
	}
	if p.K == nil {
		return KMeansConfig{}, groupInvalid(at, "k_required", at+" GROUP_KMEANS needs params.k, the cluster count (an integer ≥ 2)",
			map[string]any{"param": "k"})
	}
	var cerr *errors.CodedError
	if cfg.K, cerr = intParam("k", p.K, 2); cerr != nil {
		return KMeansConfig{}, cerr
	}
	if p.Seed != nil {
		v, err := strconv.ParseInt(p.Seed.String(), 10, 64)
		if err != nil {
			return KMeansConfig{}, groupInvalid(at, "bad_params", at+" params.seed must be an int64",
				map[string]any{"param": "seed", "value": p.Seed.String()})
		}
		if v != 0 {
			cfg.Seed = v
		}
	}
	if p.MaxIter != nil {
		if cfg.MaxIter, cerr = intParam("max_iter", p.MaxIter, 1); cerr != nil {
			return KMeansConfig{}, cerr
		}
	}
	if p.NInit != nil {
		if cfg.NInit, cerr = intParam("n_init", p.NInit, 1); cerr != nil {
			return KMeansConfig{}, cerr
		}
	}
	if p.Standardize != nil {
		cfg.Standardize = *p.Standardize
	}
	if p.Missing != nil && *p.Missing != MissingListwise {
		reason, msg := "bad_params", at+" params.missing must be \"listwise\""
		if *p.Missing == MissingPairwise {
			reason = "pairwise_unsupported"
			msg = at + " params.missing \"pairwise\" is not supported on GROUP_KMEANS: a distance needs every member; use \"listwise\""
		}
		return KMeansConfig{}, groupInvalid(at, reason, msg,
			map[string]any{"param": "missing", "value": *p.Missing, "valid": []string{MissingListwise}})
	}
	return cfg, nil
}

// KMeansK returns g's params.k when it decodes (EstimateBuckets'
// bound), false otherwise.
func KMeansK(g *types.Group) (int, bool) {
	cfg, err := KMeansParams("groups[0]", g.Params)
	if err != nil {
		return 0, false
	}
	return cfg.K, true
}

// GroupInputMembers returns the union of every field req's grouper
// slots read through inline fields (a named vector's members ride
// Members(req)), and whether every one resolved — projection decodes
// wide otherwise.
func GroupInputMembers(req *types.Request, schema *encoding.Schema) (members []string, ok bool) {
	ok = true
	for _, s := range GroupSlots(req) {
		if len(s.Group.Fields) == 0 {
			continue
		}
		r, err := ResolveSpec(s.Path+".fields", types.VectorSpec{Fields: s.Group.Fields}, schema)
		if err != nil {
			ok = false
			continue
		}
		members = append(members, r.SourceFields()...)
	}
	return members, ok
}

func groupInvalid(at, reason, msg string, extra map[string]any) *errors.CodedError {
	details := map[string]any{"group": at, "reason": reason}
	for k, v := range extra {
		details[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.SERVICE_VALIDATION, msg, details)
}
