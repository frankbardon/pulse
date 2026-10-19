package descriptor

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// ZoneLoader resolves a zone name to a *temporal.Zone. A nil loader
// means temporal.LoadZone; long-lived owners pass a temporal.Cache's
// Load so a request does not rebuild a transition table per call.
type ZoneLoader func(name string) (*temporal.Zone, error)

// zoneSlot is one slot that can carry a per-slot `tz`, flattened out of
// a request so the resolution rules are written once over every slot
// family.
type zoneSlot struct {
	slot     string
	operator string
	field    string
	tz       string
	// tzField is the slot's own `tz_field` (a per-row zone field).
	tzField string
	// hidden: the instance hides operator, so it is judged as a
	// never-registered (not zone-capable) name; messages keep naming it.
	hidden bool
}

// ResolveZones is the single zone-resolution pass for a Request. The
// runtime (internal/service, every execution mode) and predict both
// call it, so a request is refused or accepted identically by both.
//
// Precedence per zone-capable slot: slot `tz` → slot `tz_field` →
// req.TimeZoneField → req.TimeZone → defaultZone
// (pulse.Options.DefaultTimeZone) → "UTC". A `tz_field` /
// `time_zone_field` names a categorical field read per row; its rules
// are resolveFieldZone's. Rules, in evaluation order:
//
//   - an unknown req.TimeZone or defaultZone → PULSE_TIMEZONE_UNKNOWN,
//     whether or not a zone-capable slot is present;
//   - an explicit slot `tz` (then `tz_field`) on an operator that is
//     not zone-capable (every extension operator included) →
//     PROCESSING_CONFIG;
//   - `tz` and `tz_field` both set on one slot → PROCESSING_CONFIG
//     {slot, operator, tz, tz_field};
//   - an explicit slot `tz` whose field is in the schema and is not
//     `datetime` (a calendar `date`, or an epoch-day count) →
//     PROCESSING_CONFIG;
//   - an unknown slot `tz` → PULSE_TIMEZONE_UNKNOWN;
//   - an INHERITED zone on a non-`datetime` field is not applied: the
//     slot echoes a null `tz`;
//   - a zone that is not UTC-equivalent (temporal.Zone.IsUTC — "UTC" and
//     the fixed-zero Etc aliases are equivalent) resolving onto a field
//     absent from the schema (a derived or chain-synthesised column) →
//     PROCESSING_CONFIG with details {slot, operator, tz}: the operators
//     read an absent field as epoch days, so the zone would be silently
//     ignored. Onto a `datetime` schema field it is accepted and applied
//     (the runtime writes it onto the executing slot — ZonedRequest).
//
// The slots walked are every filterer, feature, attribute, group,
// crosstab row and column, then each MAT_DISTANCE params.between
// ("matrices[i].params.between"), in that order.
//
// A slot with an empty operator Type is skipped (it is a type error,
// not a zone error, and type validation reports it identically with or
// without a `tz`). A nil schema skips the two field-dependent refusals
// (explicit `tz` on a non-datetime field; a non-UTC zone onto instants):
// only a caller with no cohort schema in reach passes nil, and it must
// never refuse what the schema-holding runtime accepts.
//
// inst is the instance feature set: an operator it hides is not
// zone-capable, exactly as a never-registered name. Nil hides nothing.
//
// It never mutates req. The returned slice is never nil.
func ResolveZones(req *types.Request, schema *encoding.Schema, defaultZone string, load ZoneLoader, inst *InstanceSnapshot) ([]descriptor.ResolvedZone, error) {
	if req == nil {
		return []descriptor.ResolvedZone{}, nil
	}
	return resolveZoneSlots(requestZoneSlots(req, inst), req.TimeZone, req.TimeZoneField, schema, defaultZone, load, inst)
}

// requestZoneSlots flattens every slot of req that can carry a `tz` /
// `tz_field`, in resolution order (see ResolveZones).
func requestZoneSlots(req *types.Request, inst *InstanceSnapshot) []zoneSlot {
	var slots []zoneSlot
	for i, f := range req.Filterers {
		if f != nil {
			slots = append(slots, zoneSlot{slot: fmt.Sprintf("filterers[%d]", i), operator: string(f.Type), field: f.Field, tz: f.TimeZone, tzField: f.TimeZoneField, hidden: inst.Hidden(string(f.Type))})
		}
	}
	for i, f := range req.Features {
		if f != nil {
			slots = append(slots, zoneSlot{slot: fmt.Sprintf("features[%d]", i), operator: string(f.Type), field: f.Field, tz: f.TimeZone, tzField: f.TimeZoneField, hidden: inst.Hidden(string(f.Type))})
		}
	}
	for i, a := range req.Attributes {
		if a != nil {
			slots = append(slots, zoneSlot{slot: fmt.Sprintf("attributes[%d]", i), operator: string(a.Type), field: a.Field, tz: a.TimeZone, tzField: a.TimeZoneField, hidden: inst.Hidden(string(a.Type))})
		}
	}
	for i, g := range req.Groups {
		if g != nil {
			slots = append(slots, zoneSlot{slot: fmt.Sprintf("groups[%d]", i), operator: string(g.Type), field: g.Field, tz: g.TimeZone, tzField: g.TimeZoneField, hidden: inst.Hidden(string(g.Type))})
		}
	}
	if ct := req.Crosstab; ct != nil {
		for i, g := range ct.Rows {
			if g != nil {
				slots = append(slots, zoneSlot{slot: fmt.Sprintf("crosstab.rows[%d]", i), operator: string(g.Type), field: g.Field, tz: g.TimeZone, tzField: g.TimeZoneField, hidden: inst.Hidden(string(g.Type))})
			}
		}
		for i, g := range ct.Columns {
			if g != nil {
				slots = append(slots, zoneSlot{slot: fmt.Sprintf("crosstab.columns[%d]", i), operator: string(g.Type), field: g.Field, tz: g.TimeZone, tzField: g.TimeZoneField, hidden: inst.Hidden(string(g.Type))})
			}
		}
	}
	// A MAT_DISTANCE params.between is a grouper slot too: it resolves,
	// refuses and inherits exactly like a top-level one, at the slot
	// path "matrices[i].params.between" (ZonedRequest writes an
	// inherited zone into it). A between that does not decode is left
	// to the matrix resolver (vectors.ResolveMatrices), as a slot with
	// no Type is left to type validation.
	for i, m := range req.Matrices {
		if opRoute(inst, m.Type) != types.MAT_DISTANCE {
			continue
		}
		if g, ok := vectors.SpecBetween(m); ok {
			slots = append(slots, zoneSlot{slot: fmt.Sprintf("matrices[%d].params.between", i), operator: string(g.Type), field: g.Field, tz: g.TimeZone, tzField: g.TimeZoneField, hidden: inst.Hidden(string(g.Type))})
		}
	}
	return slots
}

// ResolveFacetZones is ResolveZones for a FacetRequest: its filterers
// resolve through slot `tz` → req.TimeZone → defaultZone → "UTC" under
// the same rules.
func ResolveFacetZones(req *types.FacetRequest, schema *encoding.Schema, defaultZone string, load ZoneLoader, inst *InstanceSnapshot) ([]descriptor.ResolvedZone, error) {
	if req == nil {
		return []descriptor.ResolvedZone{}, nil
	}
	var slots []zoneSlot
	for i, f := range req.Filterers {
		if f != nil {
			slots = append(slots, zoneSlot{slot: fmt.Sprintf("filterers[%d]", i), operator: string(f.Type), field: f.Field, tz: f.TimeZone, tzField: f.TimeZoneField, hidden: inst.Hidden(string(f.Type))})
		}
	}
	return resolveZoneSlots(slots, req.TimeZone, req.TimeZoneField, schema, defaultZone, load, inst)
}

func resolveZoneSlots(slots []zoneSlot, requestZone, requestZoneField string, schema *encoding.Schema, defaultZone string, load ZoneLoader, inst *InstanceSnapshot) ([]descriptor.ResolvedZone, error) {
	if load == nil {
		load = temporal.LoadZone
	}
	out := []descriptor.ResolvedZone{}

	// The inherited zone: request → options → default. Validated up
	// front so an unknown name is refused even with no capable slot.
	inherited, inheritedSource := "UTC", descriptor.ZoneSourceDefault
	switch {
	case requestZone != "":
		inherited, inheritedSource = requestZone, descriptor.ZoneSourceRequest
	case defaultZone != "":
		inherited, inheritedSource = defaultZone, descriptor.ZoneSourceOptions
	}
	if requestZone != "" {
		if _, err := load(requestZone); err != nil {
			return nil, err
		}
	}
	if defaultZone != "" {
		if _, err := load(defaultZone); err != nil {
			return nil, err
		}
	}

	for _, s := range slots {
		if s.operator == "" {
			// No operator type (smart defaults disabled, or no default
			// rule fits): not a zone question. Type validation reports
			// the empty Type with the same error it gives without a
			// `tz`, so the slot is not inspected here.
			continue
		}
		capable := !s.hidden && IsZoneCapable(s.operator)
		if !capable {
			if s.tz != "" {
				return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
					fmt.Sprintf("%s: operator %s does not accept `tz`; %s", s.slot, s.operator, zoneCapableAdvice(inst)),
					map[string]any{"slot": s.slot, "operator": s.operator, errors.DetailTimeZone: s.tz})
			}
			if s.tzField != "" {
				return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
					fmt.Sprintf("%s: operator %s does not accept `tz_field`; %s", s.slot, s.operator, zoneCapableAdvice(inst)),
					map[string]any{"slot": s.slot, "operator": s.operator, detailZoneField: s.tzField})
			}
			continue
		}
		if s.tz != "" && s.tzField != "" {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				fmt.Sprintf("%s: %s sets both `tz` (%q) and `tz_field` (%q); a slot reads one zone — set `tz` for one zone, or `tz_field` for a per-row zone", s.slot, s.operator, s.tz, s.tzField),
				map[string]any{"slot": s.slot, "operator": s.operator, errors.DetailTimeZone: s.tz, detailZoneField: s.tzField})
		}

		// A date-family slot over a vector field is not a zone question:
		// the field-reference walk, which runs right after this pass on
		// both sides, refuses it (PULSE_VECTOR_FIELD_NOT_SCALAR) whatever
		// the zone, so judging it here too would report it twice.
		if _, _, isVec := vectors.VectorField(schema, s.field); isVec {
			continue
		}
		fieldType, known := "", false
		if schema != nil && s.field != "" {
			if f := schema.Field(s.field); f != nil {
				fieldType, known = f.Type.String(), true
			}
		}
		instants := !known || fieldType == encoding.FieldTypeDateTime.String()
		// A nil schema means the caller cannot see the field types (a
		// validator without a cohort schema). The field-dependent
		// refusals (explicit `tz` on a non-datetime field, a non-UTC
		// zone onto instants) are then not decided here, so a
		// schema-less check never refuses what the runtime — which
		// always has the schema — accepts.
		schemaKnown := schema != nil

		// A per-row zone field: the slot's own `tz_field`, else the
		// request's `time_zone_field` — both outrank every zone name but
		// the slot's own `tz`.
		if zoneField, explicit := s.tzField, s.tzField != ""; s.tz == "" && (explicit || requestZoneField != "") {
			if !explicit {
				zoneField = requestZoneField
			}
			rz, err := resolveFieldZone(s, zoneField, explicit, fieldType, known, instants, schema)
			if err != nil {
				return nil, err
			}
			out = append(out, rz)
			continue
		}

		name, source := inherited, inheritedSource
		if s.tz != "" {
			if !instants && schemaKnown {
				return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
					fmt.Sprintf("%s: `tz` is set on %s over field %q of type %s; a time zone applies only to a datetime field (a calendar date carries no instant)", s.slot, s.operator, s.field, fieldType),
					map[string]any{"slot": s.slot, "operator": s.operator, errors.DetailTimeZone: s.tz, "field": s.field, "field_type": fieldType})
			}
			if _, err := load(s.tz); err != nil {
				if ce, ok := err.(*errors.CodedError); ok {
					details := map[string]any{"slot": s.slot}
					for k, v := range ce.Details {
						details[k] = v
					}
					return nil, errors.NewCodedErrorWithDetails(ce.Code, ce.Message, details)
				}
				return nil, err
			}
			name, source = s.tz, descriptor.ZoneSourceSlot
		}

		rz := descriptor.ResolvedZone{Slot: s.slot, Operator: s.operator, FieldType: fieldType, Source: source}
		if !instants {
			// Inherited zone on a calendar-date field: not applied.
			out = append(out, rz)
			continue
		}
		z, err := load(name)
		if err != nil {
			return nil, err
		}
		if !z.IsUTC() && schemaKnown && !known {
			return nil, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				fmt.Sprintf("%s: %s resolves time zone %q (from %s) onto field %q, which is not in the schema; a zone cannot be applied to a derived field — name a datetime schema field, or use UTC (or a fixed-zero alias such as \"Etc/UTC\")", s.slot, s.operator, name, source, s.field),
				map[string]any{"slot": s.slot, "operator": s.operator, errors.DetailTimeZone: name})
		}
		n := name
		rz.TZ = &n
		out = append(out, rz)
	}
	return out, nil
}

// detailZoneField is the CodedError.Details key carrying a slot's
// per-row zone field (`tz_field`, or the inherited `time_zone_field`).
const detailZoneField = "tz_field"

// resolveFieldZone resolves one zone-capable slot whose zone is read
// per row from zoneField — its own `tz_field` (explicit) or the
// request's inherited `time_zone_field`. The echo is source "field",
// tz_field named and tz null (the zone varies per row). Rules, mirroring
// a zone name's:
//
//   - an explicit `tz_field` on a field in the schema that is not
//     `datetime` → PROCESSING_CONFIG {slot, operator, tz_field, field,
//     field_type}; an inherited one is not applied (nor judged);
//   - the zone field, when in the schema, must be `categorical_*` →
//     else PROCESSING_CONFIG {slot, operator, tz_field, field,
//     field_type} (field and field_type the zone field's);
//   - every dictionary entry must be a zone name LoadZone accepts
//     (temporal.ValidZoneName — no table is built) → else
//     PULSE_TIMEZONE_UNKNOWN {slot, field, tz, dictionary_index};
//   - onto a slot field absent from the schema (a derived column) →
//     PROCESSING_CONFIG {slot, operator, tz_field}: a per-row zone
//     cannot be applied there any more than a zone name.
//
// A zone field absent from the schema is not judged here: the
// field-reference pass (checkZoneFields), which every mode runs right
// after this one, refuses it — against the joined schema of a join and
// the synthesised schema of a chain stage alike. A nil schema skips
// every field-dependent rule.
func resolveFieldZone(s zoneSlot, zoneField string, explicit bool, fieldType string, known, instants bool, schema *encoding.Schema) (descriptor.ResolvedZone, error) {
	rz := descriptor.ResolvedZone{Slot: s.slot, Operator: s.operator, FieldType: fieldType, TZField: zoneField, Source: descriptor.ZoneSourceField}
	if schema == nil {
		return rz, nil
	}
	if !instants {
		if explicit {
			return rz, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				fmt.Sprintf("%s: `tz_field` is set on %s over field %q of type %s; a time zone applies only to a datetime field (a calendar date carries no instant)", s.slot, s.operator, s.field, fieldType),
				map[string]any{"slot": s.slot, "operator": s.operator, detailZoneField: zoneField, "field": s.field, "field_type": fieldType})
		}
		// Inherited zone field on a calendar-date field: not applied.
		return rz, nil
	}
	if zf := schema.Field(zoneField); zf != nil {
		if !zf.Type.IsCategorical() {
			return rz, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
				fmt.Sprintf("%s: %s reads its per-row zone from field %q of type %s; a zone field must be categorical (categorical_u8 / u16 / u32) with IANA zone names as its values", s.slot, s.operator, zoneField, zf.Type),
				map[string]any{"slot": s.slot, "operator": s.operator, detailZoneField: zoneField, "field": zoneField, "field_type": zf.Type.String()})
		}
		if zf.Dictionary != nil {
			for i, name := range zf.Dictionary.Values() {
				if err := temporal.ValidZoneName(name); err != nil {
					ce, ok := err.(*errors.CodedError)
					if !ok {
						return rz, err
					}
					return rz, errors.NewCodedErrorWithDetails(ce.Code,
						fmt.Sprintf("%s: zone field %q entry %d: %s", s.slot, zoneField, i, ce.Message),
						map[string]any{"slot": s.slot, "field": zoneField, errors.DetailTimeZone: name, temporal.DetailDictionaryIndex: i})
				}
			}
		}
	}
	if !known {
		return rz, errors.NewCodedErrorWithDetails(errors.PROCESSING_CONFIG,
			fmt.Sprintf("%s: %s reads a per-row zone from field %q onto field %q, which is not in the schema; a zone cannot be applied to a derived field — name a datetime schema field", s.slot, s.operator, zoneField, s.field),
			map[string]any{"slot": s.slot, "operator": s.operator, detailZoneField: zoneField})
	}
	return rz, nil
}

// RefusalAt returns a located request-level refusal: err with its
// location inside a multi-request root added to its details — key
// "request" (0-based Compose slot) or "stage" (0-based chain stage) set
// to idx. A *errors.CodedError anywhere in err's chain is copied (code
// and message unchanged, so the code still survives errors.As); any
// other error is returned as is. The located refusals are the zone
// refusals (ResolveZones / ResolveFacetZones) and the join-count rule
// (JoinCountRefusal); the runtime marks them where they are raised and
// the validators call RefusalAt at the same points, so both sides add
// the same location.
func RefusalAt(err error, key string, idx int) error {
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		return err
	}
	details := make(map[string]any, len(ce.Details)+1)
	for k, v := range ce.Details {
		details[k] = v
	}
	details[key] = idx
	return errors.NewCodedErrorWithDetails(ce.Code, ce.Message, details)
}

// resolveRequestZones is the validators' mirror of one runtime
// resolution: smart defaults on a clone (unless opts.DisableDefaults)
// against schema, then ResolveZones with the options' default zone and
// loader — the order every execution mode uses.
func resolveRequestZones(req *types.Request, schema *encoding.Schema, opts *PredictOptions) ([]descriptor.ResolvedZone, error) {
	if opts == nil {
		opts = &PredictOptions{}
	}
	return ResolveZones(defaultedForValidation(req, schema, opts), schema, opts.DefaultTimeZone, opts.ZoneLoader, opts.instance())
}

// addCodedError records err on env under its own code (a
// *errors.CodedError keeps its Code and Details), falling back to
// PROCESSING_CONFIG for an uncoded error.
func addCodedError(env *descriptor.Envelope, err error) {
	if ce, ok := err.(*errors.CodedError); ok {
		env.AddError(string(ce.Code), ce.Message, ce.Details)
		return
	}
	env.AddError(string(errors.PROCESSING_CONFIG), err.Error(), nil)
}

// validatorRequestSchema is the schema a validator judges one
// Request's zones and field references against: base (the cohort
// schema), or for a single JoinSpec the joined schema the runtime
// executes over. A join whose right cohort opts.SchemaLoader cannot
// read (or no loader at all) yields nil — the schema-less mode in which
// ResolveZones applies only the field-independent refusals and
// FieldRefRefusals none, so a validator never refuses what the runtime
// accepts. A readable join also returns the join-key rule's refusals
// (internal/encoding.JoinKeysRefusals) — the runtime meets them before
// zones or field references, so a caller reports them and stops there.
func validatorRequestSchema(req *types.Request, base *encoding.Schema, opts *PredictOptions) (*encoding.Schema, []*errors.CodedError) {
	if base == nil || req == nil || len(req.Joins) == 0 {
		return base, nil
	}
	if len(req.Joins) != 1 || req.Joins[0] == nil || opts == nil || opts.SchemaLoader == nil {
		return nil, nil
	}
	right, err := opts.SchemaLoader(req.Joins[0].Right)
	if err != nil {
		return nil, nil
	}
	if refusals := encx.JoinKeysRefusals(base, right, req.Joins[0]); len(refusals) > 0 {
		return nil, refusals
	}
	joined, err := encx.JoinedSchema(base, right, req.Joins[0].As)
	if err != nil {
		return nil, nil
	}
	return joined, nil
}

// defaultedForValidation is the Request a validator judges: a clone
// with the shared smart-defaults pass run against schema (the
// runtime's applyDefaults), unless opts.DisableDefaults or the schema
// is unknown. The caller's request is never mutated.
func defaultedForValidation(req *types.Request, schema *encoding.Schema, opts *PredictOptions) *types.Request {
	clone := cloneRequestForDefaults(req)
	if (opts == nil || !opts.DisableDefaults) && schema != nil {
		ResolveDefaults(clone, schema, opts.instance())
	}
	return clone
}

// cohortSchemaFor loads the schema of the cohort a Compose slot names
// through opts.SchemaLoader, joining path segments the way the runtime
// does (Cohort.DataDir + "/" + Cohort.Filename). Nil when there is no
// loader, no cohort, or the read fails.
func cohortSchemaFor(c *types.Cohort, opts *PredictOptions) *encoding.Schema {
	if c == nil || opts == nil || opts.SchemaLoader == nil {
		return nil
	}
	schema, err := opts.SchemaLoader(cohortPathFor(c))
	if err != nil {
		return nil
	}
	return schema
}

// cohortPathFor is the path a Compose slot's cohort resolves to, joined
// the way the runtime joins it (Cohort.DataDir + "/" + Cohort.Filename).
func cohortPathFor(c *types.Cohort) string {
	if c.DataDir != "" {
		return c.DataDir + "/" + c.Filename
	}
	return c.Filename
}

// zoneCapableOperators is the zone-capable set in the order the
// not-capable refusal lists it.
var zoneCapableOperators = []string{
	string(types.GROUP_DATE),
	string(types.GROUP_DATE_RANGES),
	string(types.FILTER_DATE_RANGES),
	string(types.ATTR_DATE_PART),
	string(types.FEAT_DATE_FEATURES),
}

// zoneCapableAdvice is the not-capable refusal's advice clause, listing
// only the zone-capable operators inst offers: a refusal never names an
// operator the instance hides.
func zoneCapableAdvice(inst *InstanceSnapshot) string {
	var offered []string
	for _, n := range zoneCapableOperators {
		if !inst.Hidden(n) {
			offered = append(offered, n)
		}
	}
	if len(offered) == 0 {
		return "no operator this instance offers takes a per-slot time zone"
	}
	return "only zone-capable operators (" + strings.Join(offered, ", ") + ") take a per-slot time zone"
}

// ZonedRequest returns the Request an execution arm runs: req itself
// when no INHERITED zone resolved onto a slot that must carry it,
// otherwise a shallow copy whose affected slots are copies carrying it.
// Two write-sets: an inherited zone NAME (request `time_zone` or the
// options default) that is not UTC-equivalent is written into the
// slot's `tz`; an inherited per-row zone FIELD (request
// `time_zone_field`) resolved onto a `datetime` slot is written into
// the slot's `tz_field`. zones is ResolveZones' result for req; load
// resolves a name (nil: temporal.LoadZone).
//
// The operator factories see only the slot and the schema, so the zone
// rides on the slot: every arm (buffered, streaming, fused crosstab,
// parallel decode, shard reduce, chain stages) builds its operators
// from the returned request and sees the zone with no further
// plumbing. An explicit slot `tz` / `tz_field` is already on the slot,
// an inherited zone (name or field) on a non-`datetime` field is never
// written, and a UTC-equivalent zone name is never written — so the
// zone-free request executes the unchanged object. req is never
// mutated: the caller's request (request echo, request hashing, the
// Compose echo) stays exactly as the caller wrote it plus smart
// defaults.
func ZonedRequest(req *types.Request, zones []descriptor.ResolvedZone, load ZoneLoader) *types.Request {
	writes := inheritedZoneWrites(zones, load)
	if req == nil || writes == nil {
		return req
	}
	c := *req
	changed := false
	if s, ok := zonedSlots(req.Filterers, "filterers", writes, zonedFilterer); ok {
		c.Filterers, changed = s, true
	}
	if s, ok := zonedSlots(req.Features, "features", writes, func(f *types.Feature, w zoneWrite) (*types.Feature, bool) {
		if !w.changes(f.TimeZone, f.TimeZoneField) {
			return f, false
		}
		cp := *f
		w.apply(&cp.TimeZone, &cp.TimeZoneField)
		return &cp, true
	}); ok {
		c.Features, changed = s, true
	}
	if s, ok := zonedSlots(req.Attributes, "attributes", writes, func(a *types.Attribute, w zoneWrite) (*types.Attribute, bool) {
		if !w.changes(a.TimeZone, a.TimeZoneField) {
			return a, false
		}
		cp := *a
		w.apply(&cp.TimeZone, &cp.TimeZoneField)
		return &cp, true
	}); ok {
		c.Attributes, changed = s, true
	}
	if s, ok := zonedSlots(req.Groups, "groups", writes, zonedGroup); ok {
		c.Groups, changed = s, true
	}
	if req.Crosstab != nil {
		rows, rok := zonedSlots(req.Crosstab.Rows, "crosstab.rows", writes, zonedGroup)
		cols, cok := zonedSlots(req.Crosstab.Columns, "crosstab.columns", writes, zonedGroup)
		if rok || cok {
			ct := *req.Crosstab
			if rok {
				ct.Rows = rows
			}
			if cok {
				ct.Columns = cols
			}
			c.Crosstab, changed = &ct, true
		}
	}
	if m, ok := zonedMatrices(req.Matrices, writes); ok {
		c.Matrices, changed = m, true
	}
	if !changed {
		return req
	}
	return &c
}

// zoneWrite is one inherited zone to carry onto a slot: a zone name
// (into `tz`) or a per-row zone field (into `tz_field`) — exactly one
// is set.
type zoneWrite struct {
	tz      string
	tzField string
}

// changes reports whether writing w onto a slot carrying tz / tzField
// changes it.
func (w zoneWrite) changes(tz, tzField string) bool {
	if w.tzField != "" {
		return tzField != w.tzField
	}
	return tz != w.tz
}

// apply writes w into a slot copy's `tz` / `tz_field`.
func (w zoneWrite) apply(tz, tzField *string) {
	if w.tzField != "" {
		*tzField = w.tzField
		return
	}
	*tz = w.tz
}

// zonedMatrices returns a copy of in with each MAT_DISTANCE spec whose
// params.between inherited a zone (writes key
// "matrices[i].params.between") carrying it as the between's `tz` (or
// `tz_field`), and whether any spec was; in (and its Params bytes) is
// never mutated. The params are re-encoded with that key set and every
// other key kept; a between that does not decode as an object is left
// as is (the matrix resolver refuses it).
func zonedMatrices(in []types.MatrixSpec, writes map[string]zoneWrite) ([]types.MatrixSpec, bool) {
	var out []types.MatrixSpec
	for i := range in {
		w, ok := writes[fmt.Sprintf("matrices[%d].params.between", i)]
		if !ok {
			continue
		}
		key, value := "tz", w.tz
		if w.tzField != "" {
			key, value = "tz_field", w.tzField
		}
		params, ok := betweenWithZone(in[i].Params, key, value)
		if !ok {
			continue
		}
		if out == nil {
			out = append([]types.MatrixSpec(nil), in...)
		}
		out[i].Params = params
	}
	return out, out != nil
}

// betweenWithZone re-encodes MAT_DISTANCE params with params.between's
// key (`tz` or `tz_field`) set to value.
func betweenWithZone(raw json.RawMessage, key, value string) (json.RawMessage, bool) {
	var params map[string]json.RawMessage
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, false
	}
	var between map[string]json.RawMessage
	if err := json.Unmarshal(params["between"], &between); err != nil || between == nil {
		return nil, false
	}
	name, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	between[key] = name
	if params["between"], err = json.Marshal(between); err != nil {
		return nil, false
	}
	out, err := json.Marshal(params)
	if err != nil {
		return nil, false
	}
	return out, true
}

// ZonedFacetRequest is ZonedRequest for a FacetRequest's filterers;
// zones is ResolveFacetZones' result for req.
func ZonedFacetRequest(req *types.FacetRequest, zones []descriptor.ResolvedZone, load ZoneLoader) *types.FacetRequest {
	writes := inheritedZoneWrites(zones, load)
	if req == nil || writes == nil {
		return req
	}
	s, ok := zonedSlots(req.Filterers, "filterers", writes, zonedFilterer)
	if !ok {
		return req
	}
	c := *req
	c.Filterers = s
	return &c
}

// inheritedZoneWrites maps each slot path that must carry an inherited
// zone to it; nil when there is none (the zone-free and UTC paths,
// which then allocate nothing). A zone name is written when it is not
// UTC-equivalent and did not come from the slot (a slot-sourced zone is
// already on its slot; the default source is always "UTC"). A per-row
// zone field is written when it applies — the slot's field is
// `datetime` (or not judged) — and the write is skipped by the slot
// writer when the slot already names that field (its own `tz_field`).
//
// The operators read a slot's `tz_field` through a temporal.ZoneTable
// (internal/processing/date_field.go, the FEAT_DATE_FEATURES computer)
// and the projection decodes it (processing.NeededFields).
func inheritedZoneWrites(zones []descriptor.ResolvedZone, load ZoneLoader) map[string]zoneWrite {
	if load == nil {
		load = temporal.LoadZone
	}
	var writes map[string]zoneWrite
	for _, z := range zones {
		var w zoneWrite
		switch {
		case z.Source == descriptor.ZoneSourceField:
			if z.TZField == "" || (z.FieldType != "" && z.FieldType != encoding.FieldTypeDateTime.String()) {
				continue
			}
			w = zoneWrite{tzField: z.TZField}
		case z.TZ == nil || z.Source == descriptor.ZoneSourceSlot || z.Source == descriptor.ZoneSourceDefault:
			continue
		default:
			zone, err := load(*z.TZ)
			if err != nil || zone.IsUTC() {
				continue
			}
			w = zoneWrite{tz: *z.TZ}
		}
		if writes == nil {
			writes = make(map[string]zoneWrite)
		}
		writes[z.Slot] = w
	}
	return writes
}

func zonedGroup(g *types.Group, w zoneWrite) (*types.Group, bool) {
	if !w.changes(g.TimeZone, g.TimeZoneField) {
		return g, false
	}
	cp := *g
	w.apply(&cp.TimeZone, &cp.TimeZoneField)
	return &cp, true
}

func zonedFilterer(f *types.Filterer, w zoneWrite) (*types.Filterer, bool) {
	if !w.changes(f.TimeZone, f.TimeZoneField) {
		return f, false
	}
	cp := *f
	w.apply(&cp.TimeZone, &cp.TimeZoneField)
	return &cp, true
}

// zonedSlots returns a copy of in with each slot named in writes
// replaced by with(slot, write) when that changes it, and whether any
// slot was; in is never mutated.
func zonedSlots[T any](in []*T, family string, writes map[string]zoneWrite, with func(*T, zoneWrite) (*T, bool)) ([]*T, bool) {
	var out []*T
	for i, s := range in {
		w, ok := writes[fmt.Sprintf("%s[%d]", family, i)]
		if !ok || s == nil {
			continue
		}
		cp, changed := with(s, w)
		if !changed {
			continue
		}
		if out == nil {
			out = append([]*T(nil), in...)
		}
		out[i] = cp
	}
	return out, out != nil
}
