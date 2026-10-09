package descriptor

import (
	stderrors "errors"
	"fmt"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/temporal"
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
	// hidden: the instance hides operator, so it is judged as a
	// never-registered (not zone-capable) name; messages keep naming it.
	hidden bool
}

// ResolveZones is the single zone-resolution pass for a Request. The
// runtime (internal/service, every execution mode) and predict both
// call it, so a request is refused or accepted identically by both.
//
// Precedence per zone-capable slot: slot `tz` → req.TimeZone →
// defaultZone (pulse.Options.DefaultTimeZone) → "UTC". Rules, in
// evaluation order:
//
//   - an unknown req.TimeZone or defaultZone → PULSE_TIMEZONE_UNKNOWN,
//     whether or not a zone-capable slot is present;
//   - an explicit slot `tz` on an operator that is not zone-capable
//     (every extension operator included) → PROCESSING_CONFIG;
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
	var slots []zoneSlot
	for i, f := range req.Filterers {
		if f != nil {
			slots = append(slots, zoneSlot{fmt.Sprintf("filterers[%d]", i), string(f.Type), f.Field, f.TimeZone, inst.Hidden(string(f.Type))})
		}
	}
	for i, f := range req.Features {
		if f != nil {
			slots = append(slots, zoneSlot{fmt.Sprintf("features[%d]", i), string(f.Type), f.Field, f.TimeZone, inst.Hidden(string(f.Type))})
		}
	}
	for i, a := range req.Attributes {
		if a != nil {
			slots = append(slots, zoneSlot{fmt.Sprintf("attributes[%d]", i), string(a.Type), a.Field, a.TimeZone, inst.Hidden(string(a.Type))})
		}
	}
	for i, g := range req.Groups {
		if g != nil {
			slots = append(slots, zoneSlot{fmt.Sprintf("groups[%d]", i), string(g.Type), g.Field, g.TimeZone, inst.Hidden(string(g.Type))})
		}
	}
	if ct := req.Crosstab; ct != nil {
		for i, g := range ct.Rows {
			if g != nil {
				slots = append(slots, zoneSlot{fmt.Sprintf("crosstab.rows[%d]", i), string(g.Type), g.Field, g.TimeZone, inst.Hidden(string(g.Type))})
			}
		}
		for i, g := range ct.Columns {
			if g != nil {
				slots = append(slots, zoneSlot{fmt.Sprintf("crosstab.columns[%d]", i), string(g.Type), g.Field, g.TimeZone, inst.Hidden(string(g.Type))})
			}
		}
	}
	return resolveZoneSlots(slots, req.TimeZone, schema, defaultZone, load, inst)
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
			slots = append(slots, zoneSlot{fmt.Sprintf("filterers[%d]", i), string(f.Type), f.Field, f.TimeZone, inst.Hidden(string(f.Type))})
		}
	}
	return resolveZoneSlots(slots, req.TimeZone, schema, defaultZone, load, inst)
}

func resolveZoneSlots(slots []zoneSlot, requestZone string, schema *encoding.Schema, defaultZone string, load ZoneLoader, inst *InstanceSnapshot) ([]descriptor.ResolvedZone, error) {
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
// when no INHERITED zone (request `time_zone` or the options default)
// that is not UTC-equivalent resolved onto a slot, otherwise a shallow
// copy whose affected slots are copies carrying that zone in their
// `tz`. zones is ResolveZones' result for req; load resolves a name
// (nil: temporal.LoadZone).
//
// The operator factories see only the slot and the schema, so the zone
// rides on the slot: every arm (buffered, streaming, fused crosstab,
// parallel decode, shard reduce, chain stages) builds its operators
// from the returned request and sees the zone with no further
// plumbing. An explicit slot `tz` is already on the slot, an inherited
// zone on a non-`datetime` field resolved to a null TZ and is never
// written, and a UTC-equivalent zone is never written — so the zone-free
// request executes the unchanged object. req is never mutated: the
// caller's request (request echo, request hashing, the Compose echo)
// stays exactly as the caller wrote it plus smart defaults.
func ZonedRequest(req *types.Request, zones []descriptor.ResolvedZone, load ZoneLoader) *types.Request {
	writes := inheritedZoneWrites(zones, load)
	if req == nil || writes == nil {
		return req
	}
	c := *req
	c.Filterers = zonedFilterers(req.Filterers, writes)
	if s, ok := zonedSlots(req.Features, "features", writes, func(f *types.Feature, tz string) *types.Feature { cp := *f; cp.TimeZone = tz; return &cp }); ok {
		c.Features = s
	}
	if s, ok := zonedSlots(req.Attributes, "attributes", writes, func(a *types.Attribute, tz string) *types.Attribute { cp := *a; cp.TimeZone = tz; return &cp }); ok {
		c.Attributes = s
	}
	if s, ok := zonedSlots(req.Groups, "groups", writes, zonedGroup); ok {
		c.Groups = s
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
			c.Crosstab = &ct
		}
	}
	return &c
}

// ZonedFacetRequest is ZonedRequest for a FacetRequest's filterers;
// zones is ResolveFacetZones' result for req.
func ZonedFacetRequest(req *types.FacetRequest, zones []descriptor.ResolvedZone, load ZoneLoader) *types.FacetRequest {
	writes := inheritedZoneWrites(zones, load)
	if req == nil || writes == nil {
		return req
	}
	c := *req
	c.Filterers = zonedFilterers(req.Filterers, writes)
	return &c
}

// inheritedZoneWrites maps each slot path that inherited a zone which is
// not UTC-equivalent to that zone's name; nil when there is none (the
// zone-free and UTC paths, which then allocate nothing). A slot-sourced
// zone is already on its slot and the default source is always "UTC".
func inheritedZoneWrites(zones []descriptor.ResolvedZone, load ZoneLoader) map[string]string {
	if load == nil {
		load = temporal.LoadZone
	}
	var writes map[string]string
	for _, z := range zones {
		if z.TZ == nil || z.Source == descriptor.ZoneSourceSlot || z.Source == descriptor.ZoneSourceDefault {
			continue
		}
		zone, err := load(*z.TZ)
		if err != nil || zone.IsUTC() {
			continue
		}
		if writes == nil {
			writes = make(map[string]string)
		}
		writes[z.Slot] = *z.TZ
	}
	return writes
}

func zonedGroup(g *types.Group, tz string) *types.Group {
	cp := *g
	cp.TimeZone = tz
	return &cp
}

func zonedFilterers(in []*types.Filterer, writes map[string]string) []*types.Filterer {
	out, ok := zonedSlots(in, "filterers", writes, func(f *types.Filterer, tz string) *types.Filterer { cp := *f; cp.TimeZone = tz; return &cp })
	if !ok {
		return in
	}
	return out
}

// zonedSlots returns a copy of in with each slot named in writes
// replaced by with(slot, zone), and whether any slot was; in is never
// mutated.
func zonedSlots[T any](in []*T, family string, writes map[string]string, with func(*T, string) *T) ([]*T, bool) {
	var out []*T
	for i, s := range in {
		tz, ok := writes[fmt.Sprintf("%s[%d]", family, i)]
		if !ok || s == nil {
			continue
		}
		if out == nil {
			out = append([]*T(nil), in...)
		}
		out[i] = with(s, tz)
	}
	return out, out != nil
}
