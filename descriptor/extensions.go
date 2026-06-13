package descriptor

import "sort"

// ExtensionsManifest is the manifest-side projection of embedder-
// registered operators and expression-side state. Built-in operators
// continue to live in Manifest.Components; this block lists everything
// the host process adds on top, so a reviewer can see at a glance
// which subset is Pulse-shipped vs registered by the embedder.
//
// All slices are sorted by Name for deterministic output. An empty
// Extensions block emits as nested empty slices (not null) for
// JSON-stability across releases.
type ExtensionsManifest struct {
	Aggregators        []OperatorMeta     `json:"aggregators"`
	Attributes         []OperatorMeta     `json:"attributes"`
	Filterers          []OperatorMeta     `json:"filterers"`
	Groupers           []OperatorMeta     `json:"groupers"`
	Windows            []OperatorMeta     `json:"windows"`
	Features           []OperatorMeta     `json:"features"`
	Tests              []OperatorMeta     `json:"tests"`
	SynthDistributions []OperatorMeta     `json:"synth_distributions"`
	ExprFunctions      []ExprFunctionMeta `json:"expr_functions"`
	LookupTables       []LookupTableMeta  `json:"lookup_tables"`
	LabelTables        []LabelTableMeta   `json:"label_tables"`
}

// OperatorMeta is the manifest projection for an embedder-registered
// operator. The shape mirrors descriptor.Operator but adds Namespace
// (parsed from the registered name) and Mode (attribute-only) so
// reviewers can group operators by source without re-parsing.
type OperatorMeta struct {
	Name        string              `json:"name"`
	Namespace   string              `json:"namespace"`
	Description string              `json:"description,omitempty"`
	Streamable  bool                `json:"streamable"`
	Accepts     []string            `json:"accepts,omitempty"`
	Emits       string              `json:"emits,omitempty"`
	Mode        string              `json:"mode,omitempty"`
	Tier        string              `json:"tier,omitempty"`
	Params      []OperatorParamMeta `json:"params,omitempty"`
}

// OperatorParamMeta is the manifest-friendly mirror of pulse.ParamMeta.
type OperatorParamMeta struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	JSONType    string `json:"json_type"`
	Required    bool   `json:"required,omitempty"`
	Default     any    `json:"default,omitempty"`
}

// ExprFunctionMeta is the manifest projection of an embedder-
// registered expression function.
type ExprFunctionMeta struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Signature   string `json:"signature,omitempty"`
	Pure        bool   `json:"pure,omitempty"`
}

// LookupTableMeta is the manifest projection of a registered lookup
// table. HasRowsData distinguishes the static Rows-backed table from
// the function-driven Lookup table without exposing the embedder's
// data.
type LookupTableMeta struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	HasRowsData bool   `json:"has_rows_data"`
}

// LabelTableMeta is the manifest projection of a registered string-
// valued label table. HasRowsData distinguishes the static Rows-backed
// table from the function-driven Lookup table without exposing the
// embedder's data.
type LabelTableMeta struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	HasRowsData bool   `json:"has_rows_data"`
}

// ExtensionsSnapshot is the immutable read-only view that the service
// hands to descriptor.BuildManifestWithExtensions and predict. It
// carries everything the manifest + predict surfaces need without
// importing the live processing registry — keeping
// TestPredictNoExecutionImports satisfied.
type ExtensionsSnapshot struct {
	Aggregators        []OperatorMeta
	Attributes         []OperatorMeta
	Filterers          []OperatorMeta
	Groupers           []OperatorMeta
	Windows            []OperatorMeta
	Features           []OperatorMeta
	Tests              []OperatorMeta
	SynthDistributions []OperatorMeta
	ExprFunctions      []ExprFunctionMeta
	LookupTables       []LookupTableMeta
	LabelTables        []LabelTableMeta

	// OverlayKinds is the forward-compat reservation slot for
	// embedder-registered overlay catalog entries. The runtime
	// pulse.Options.Extensions.OverlayKinds registration surface
	// (referenced in types/overlay.go) is not yet implemented; this
	// snapshot slot exists so the MCP per-facade schema binders
	// (internal/mcp/schema_bind.go) can merge custom kind names
	// into the per-facade overlay_kind enum the moment the
	// registration surface lands — no churn on the binder when the
	// runtime catches up. Today the slot is populated by tests
	// only; production code paths leave it nil and the per-facade
	// enums fall back to the built-in catalog drawn from
	// types.AllOverlayKinds() via descriptor.OverlayCapabilities().
	// Each entry carries Name (SCREAMING_SNAKE OVERLAY_* constant)
	// alongside the standard OperatorMeta knobs so the per-facade
	// classifier can route an extension kind onto the right tool's
	// enum once the registration surface declares which facade(s)
	// the custom kind targets. v1 lacks the per-kind facade tag —
	// the binder treats every snapshot entry as Request-facade
	// only (the lowest-risk fallback) until the registration
	// surface lands the tag.
	OverlayKinds []OperatorMeta
}

// emptyExtensionsManifest returns a fully-populated ExtensionsManifest
// with empty slices. Used when an ExtensionsSnapshot is nil so the
// JSON shape stays stable.
func emptyExtensionsManifest() ExtensionsManifest {
	return ExtensionsManifest{
		Aggregators:        []OperatorMeta{},
		Attributes:         []OperatorMeta{},
		Filterers:          []OperatorMeta{},
		Groupers:           []OperatorMeta{},
		Windows:            []OperatorMeta{},
		Features:           []OperatorMeta{},
		Tests:              []OperatorMeta{},
		SynthDistributions: []OperatorMeta{},
		ExprFunctions:      []ExprFunctionMeta{},
		LookupTables:       []LookupTableMeta{},
		LabelTables:        []LabelTableMeta{},
	}
}

// extensionsManifestFromSnapshot copies the snapshot's slices into a
// manifest block, sorting each by Name for determinism. Nil snapshot
// yields the empty manifest (stable JSON shape).
func extensionsManifestFromSnapshot(snap *ExtensionsSnapshot) ExtensionsManifest {
	if snap == nil {
		return emptyExtensionsManifest()
	}
	out := ExtensionsManifest{
		Aggregators:        sortOperatorMeta(snap.Aggregators),
		Attributes:         sortOperatorMeta(snap.Attributes),
		Filterers:          sortOperatorMeta(snap.Filterers),
		Groupers:           sortOperatorMeta(snap.Groupers),
		Windows:            sortOperatorMeta(snap.Windows),
		Features:           sortOperatorMeta(snap.Features),
		Tests:              sortOperatorMeta(snap.Tests),
		SynthDistributions: sortOperatorMeta(snap.SynthDistributions),
		ExprFunctions:      sortExprFunctionMeta(snap.ExprFunctions),
		LookupTables:       sortLookupTableMeta(snap.LookupTables),
		LabelTables:        sortLabelTableMeta(snap.LabelTables),
	}
	if out.Aggregators == nil {
		out.Aggregators = []OperatorMeta{}
	}
	if out.Attributes == nil {
		out.Attributes = []OperatorMeta{}
	}
	if out.Filterers == nil {
		out.Filterers = []OperatorMeta{}
	}
	if out.Groupers == nil {
		out.Groupers = []OperatorMeta{}
	}
	if out.Windows == nil {
		out.Windows = []OperatorMeta{}
	}
	if out.Features == nil {
		out.Features = []OperatorMeta{}
	}
	if out.Tests == nil {
		out.Tests = []OperatorMeta{}
	}
	if out.SynthDistributions == nil {
		out.SynthDistributions = []OperatorMeta{}
	}
	if out.ExprFunctions == nil {
		out.ExprFunctions = []ExprFunctionMeta{}
	}
	if out.LookupTables == nil {
		out.LookupTables = []LookupTableMeta{}
	}
	if out.LabelTables == nil {
		out.LabelTables = []LabelTableMeta{}
	}
	return out
}

func sortOperatorMeta(in []OperatorMeta) []OperatorMeta {
	if len(in) == 0 {
		return nil
	}
	out := make([]OperatorMeta, len(in))
	copy(out, in)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortExprFunctionMeta(in []ExprFunctionMeta) []ExprFunctionMeta {
	if len(in) == 0 {
		return nil
	}
	out := make([]ExprFunctionMeta, len(in))
	copy(out, in)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortLookupTableMeta(in []LookupTableMeta) []LookupTableMeta {
	if len(in) == 0 {
		return nil
	}
	out := make([]LookupTableMeta, len(in))
	copy(out, in)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortLabelTableMeta(in []LabelTableMeta) []LabelTableMeta {
	if len(in) == 0 {
		return nil
	}
	out := make([]LabelTableMeta, len(in))
	copy(out, in)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// extensionsFromOpts returns the snapshot reachable from PredictOptions.
// Nil-safe so every isExtension* helper can take a nil opts argument.
func extensionsFromOpts(opts *PredictOptions) *ExtensionsSnapshot {
	if opts == nil {
		return nil
	}
	return opts.Extensions
}

// snapshotHasName reports whether `name` appears in metas. Used by the
// per-category isExtension* helpers below.
func snapshotHasName(metas []OperatorMeta, name string) bool {
	for _, m := range metas {
		if m.Name == name {
			return true
		}
	}
	return false
}
