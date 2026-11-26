package descriptor

import (
	"bytes"
	"io"
	"strconv"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
)

// The cohort union (types.Request.Union) — its ONE rule set, shared by
// predict and the runtime so the two cannot drift:
//
//   - UnionShapeRefusal: the schema-free request rules, judged before
//     any member is opened.
//   - MergeUnion: the member schemas folded under the shard-archive
//     merge rules (encx.MergeUnionSchema) plus the virtual source field.
//
// Predict reads each member's header and schema only (PredictUnion);
// the runtime opens the same members and merges through the same
// function.

// UnionSourceFieldName is the virtual source field req's union adds:
// req.UnionSourceField, else types.DefaultUnionSourceField.
func UnionSourceFieldName(req *types.Request) string {
	if req != nil && req.UnionSourceField != "" {
		return req.UnionSourceField
	}
	return types.DefaultUnionSourceField
}

// UnionShapeRefusal reports the first schema-free union rule req
// breaks, or nil (UnionShapeRefusalOf over req's cohort and union
// slots).
func UnionShapeRefusal(req *types.Request) error {
	if req == nil {
		return nil
	}
	return UnionShapeRefusalOf(req.Cohort, req.Union, req.UnionSourceField)
}

// UnionShapeRefusalOf reports the first schema-free union rule a root's
// cohort / union / union_source_field slots break, or nil — the one
// rule every root carrying them (Request, ChainRequest, FacetRequest,
// SampleRequest, LookupRequest) shares. In order: a source field named
// without a union (PULSE_UNION_SOURCE_FIELD_INVALID, reason
// without_union); then, with a union, both cohort and union set
// (PULSE_UNION_COHORT_CONFLICT), more members than an archive holds
// shards (PULSE_UNION_MEMBER_LIMIT), and per member in order a missing
// cohort (PULSE_UNION_MEMBER_INVALID) or an empty / repeated label
// (PULSE_UNION_LABEL_INVALID). No union and no source field is nil —
// byte-identical to before the slots existed.
func UnionShapeRefusalOf(cohort *types.Cohort, union []types.UnionMember, sourceField string) error {
	req := &types.Request{Cohort: cohort, Union: union, UnionSourceField: sourceField}
	if len(req.Union) == 0 {
		if req.UnionSourceField != "" {
			return errors.NewCodedErrorWithDetails(errors.PULSE_UNION_SOURCE_FIELD_INVALID,
				"union_source_field "+strconv.Quote(req.UnionSourceField)+" is set but the request has no union",
				map[string]any{"reason": "without_union", "field": req.UnionSourceField})
		}
		return nil
	}
	if req.Cohort != nil {
		return errors.NewCodedErrorWithDetails(errors.PULSE_UNION_COHORT_CONFLICT,
			"request sets both cohort and union; a union replaces the request cohort",
			map[string]any{"member_count": len(req.Union)})
	}
	if n := len(req.Union); n > encx.MaxArchiveShards {
		return errors.NewCodedErrorWithDetails(errors.PULSE_UNION_MEMBER_LIMIT,
			"union lists "+strconv.Itoa(n)+" members; a union reads at most "+strconv.Itoa(encx.MaxArchiveShards)+" cohorts (the shard archive ceiling)",
			map[string]any{"member_count": n, "limit": encx.MaxArchiveShards})
	}
	first := make(map[string]int, len(req.Union))
	for i, m := range req.Union {
		if m.Cohort == nil || m.Cohort.Filename == "" {
			return errors.NewCodedErrorWithDetails(errors.PULSE_UNION_MEMBER_INVALID,
				"union member "+strconv.Itoa(i)+" names no cohort",
				map[string]any{"member": i, "label": m.Label})
		}
		if m.Label == "" {
			return errors.NewCodedErrorWithDetails(errors.PULSE_UNION_LABEL_INVALID,
				"union member "+strconv.Itoa(i)+" has an empty label",
				map[string]any{"reason": "empty", "member": i})
		}
		if j, dup := first[m.Label]; dup {
			return errors.NewCodedErrorWithDetails(errors.PULSE_UNION_LABEL_INVALID,
				"union member "+strconv.Itoa(i)+" repeats label "+strconv.Quote(m.Label)+" of member "+strconv.Itoa(j),
				map[string]any{"reason": "duplicate", "member": i, "first": j, "label": m.Label})
		}
		first[m.Label] = i
	}
	return nil
}

// UnionUnsupported is the PULSE_UNION_UNSUPPORTED refusal of an
// operation that does not read a union; operation names it.
func UnionUnsupported(operation string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_UNION_UNSUPPORTED,
		operation+" does not read a union of cohorts",
		map[string]any{"operation": operation})
}

// UnionRunRefusal is the union gate of an operation that does not (yet)
// read a union: req's UnionShapeRefusal, else PULSE_UNION_UNSUPPORTED
// naming operation when req carries a union, else nil. The runtime and
// the operation's predict both call it, so the two refuse alike.
func UnionRunRefusal(req *types.Request, operation string) error {
	if err := UnionShapeRefusal(req); err != nil {
		return err
	}
	if req != nil && len(req.Union) > 0 {
		return UnionUnsupported(operation)
	}
	return nil
}

// UnionExecRefusal is the union gate of an operation that reads a
// union as one record stream (process, process stream, a Compose slot,
// a join's LEFT side — a union is never a join's right side, which
// names one cohort path: a `union` key there is the strict decoder's
// PULSE_UNION_JOIN_RIGHT): req's UnionShapeRefusal. The runtime
// (Service.process) and predict (PredictUnion, a Compose slot's
// validation) both call it at the same point, right after the slot
// gate, so the two refuse alike.
func UnionExecRefusal(req *types.Request) error {
	return UnionShapeRefusal(req)
}

// UnionSourceFieldNameOf is UnionSourceFieldName over a root's
// union_source_field slot.
func UnionSourceFieldNameOf(sourceField string) string {
	if sourceField != "" {
		return sourceField
	}
	return types.DefaultUnionSourceField
}

// UnionMemberSchema is one opened union member: its label, its schema
// (a shard archive's canonical schema) and how many shards it
// contributes once flattened (1 for a single file or an anchor).
type UnionMemberSchema struct {
	Label  string
	Schema *encoding.Schema
	Shards int
}

// MergeUnion folds the members' schemas, in declared order, under the
// shard-archive merge rules and appends the virtual source field —
// sourceField, a non-nullable categorical whose dictionary is the
// labels in declared order (categorical_u8 up to 256 members, else
// categorical_u16). Refusals, in order: more flattened shards than an
// archive holds (PULSE_UNION_MEMBER_LIMIT, details.shard_count); a
// member whose schema the archive merge refuses
// (PULSE_UNION_SCHEMA_MISMATCH: the archive refusal's details plus
// archive_code, member and label); a source field a member already
// carries (PULSE_UNION_SOURCE_FIELD_INVALID, reason collision). The
// warnings are the archive merge's description-class divergences. No
// member schema is modified.
func MergeUnion(members []UnionMemberSchema, sourceField string) (*encoding.Schema, []encx.CohesionWarning, error) {
	if len(members) == 0 {
		return nil, nil, errors.NewCodedError(errors.PULSE_UNION_MEMBER_INVALID, "union has no members")
	}
	shards := 0
	for _, m := range members {
		shards += max(m.Shards, 1)
	}
	if shards > encx.MaxArchiveShards {
		return nil, nil, errors.NewCodedErrorWithDetails(errors.PULSE_UNION_MEMBER_LIMIT,
			"union reads "+strconv.Itoa(shards)+" shards once its shard-archive members are flattened; a union reads at most "+strconv.Itoa(encx.MaxArchiveShards),
			map[string]any{"shard_count": shards, "member_count": len(members), "limit": encx.MaxArchiveShards})
	}
	merged := members[0].Schema.Logical()
	var warnings []encx.CohesionWarning
	for i := 1; i < len(members); i++ {
		next, warns, err := encx.MergeUnionSchema(merged, members[i].Schema)
		for _, w := range warns {
			if w.Details == nil {
				w.Details = map[string]any{}
			}
			w.Details["member"] = i
			w.Details["label"] = members[i].Label
			warnings = append(warnings, w)
		}
		if err != nil {
			return nil, warnings, unionMismatch(err, i, members[i].Label)
		}
		merged = next
	}
	for _, f := range merged.Fields {
		if f.Name == sourceField {
			return nil, warnings, errors.NewCodedErrorWithDetails(errors.PULSE_UNION_SOURCE_FIELD_INVALID,
				"union source field "+strconv.Quote(sourceField)+" is already a field of the member cohorts; name another with union_source_field",
				map[string]any{"reason": "collision", "field": sourceField})
		}
	}
	return withSourceField(merged, sourceField, members), warnings, nil
}

// unionMismatch re-codes an archive merge refusal for member i as
// PULSE_UNION_SCHEMA_MISMATCH, keeping the archive's message and
// details (details.dimension included) and naming the archive code.
func unionMismatch(err error, i int, label string) error {
	details := map[string]any{"member": i, "label": label}
	msg := err.Error()
	code := string(errors.PULSE_SHARD_SCHEMA_MISMATCH)
	if ce, ok := err.(*errors.CodedError); ok {
		for k, v := range ce.Details {
			details[k] = v
		}
		msg, code = ce.Message, string(ce.Code)
	}
	details["member"], details["label"] = i, label
	details["archive_code"] = code
	return errors.NewCodedErrorWithDetails(errors.PULSE_UNION_SCHEMA_MISMATCH,
		"union member "+strconv.Itoa(i)+" ("+strconv.Quote(label)+") does not merge with the members before it: "+msg,
		details)
}

// withSourceField returns a copy of merged with the virtual source
// field appended after every member field.
func withSourceField(merged *encoding.Schema, name string, members []UnionMemberSchema) *encoding.Schema {
	ft := encoding.FieldTypeCategoricalU8
	if uint32(len(members)) > ft.MaxDictEntries() {
		ft = encoding.FieldTypeCategoricalU16
	}
	dict := encoding.NewDictionary()
	for _, m := range members {
		_, _ = dict.Add(m.Label)
	}
	out := &encoding.Schema{
		Fields:    append(make([]encoding.Field, 0, len(merged.Fields)+1), merged.Fields...),
		Vectors:   merged.Vectors,
		ZoneField: merged.ZoneField,
	}
	offset := 0
	for i := range merged.Fields {
		offset += merged.FieldWidth(i)
	}
	out.Fields = append(out.Fields, encoding.Field{
		Name:        name,
		Type:        ft,
		ByteOffset:  offset,
		Description: "Union source: the label of the member cohort the record was read from.",
		Dictionary:  dict,
	})
	return out
}

// unionMemberRead is one member as predict reads it: header and schema
// only (a shard archive's canonical entry and shard headers).
type unionMemberRead struct {
	schema  *encoding.Schema
	records []int64 // per flattened shard; nil when a count is unknown
}

// readUnionMember reads the member at rs header-only: a single file's
// header + schema (its record count from the length, as Inspect derives
// it), or a shard archive's canonical schema and each shard's peeked
// record count.
func readUnionMember(rs io.ReadSeeker) (unionMemberRead, error) {
	if src, ok := sniffArchive(rs); ok {
		arch, err := encx.OpenArchive(src.r, src.size)
		if err != nil {
			return unionMemberRead{}, errors.NewCodedError(errors.PULSE_ARCHIVE_CORRUPT, "invalid pulse shard archive: "+err.Error())
		}
		rc, err := arch.Open(encx.ReservedSchemaName)
		if err != nil {
			return unionMemberRead{}, errors.NewCodedError(errors.PULSE_SHARD_MISSING,
				"archive missing reserved schema entry "+encx.ReservedSchemaName+": "+err.Error())
		}
		canonical, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return unionMemberRead{}, errors.NewCodedError(errors.ENCODING_INVALID,
				"reading canonical schema entry "+encx.ReservedSchemaName+": "+err.Error())
		}
		schema, err := readUnionHeaderSchema(bytes.NewReader(canonical))
		if err != nil {
			return unionMemberRead{}, err
		}
		out := unionMemberRead{schema: schema}
		for _, entry := range arch.Entries() {
			if entry.Name == encx.ReservedSchemaName {
				continue
			}
			count, perr := arch.PeekShardRecordCount(entry.Name)
			if perr != nil {
				return unionMemberRead{}, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_HEADER_INVALID,
					"peeking record count for shard "+entry.Name+": "+perr.Error(),
					map[string]any{"entry": entry.Name})
			}
			out.records = append(out.records, count)
		}
		if out.records == nil {
			out.records = []int64{}
		}
		return out, nil
	}
	schema, err := readUnionHeaderSchema(rs)
	if err != nil {
		return unionMemberRead{}, err
	}
	out := unionMemberRead{schema: schema}
	if n, _, ok := deriveSingleFileRecordCount(rs, schema); ok {
		out.records = []int64{n}
	}
	return out, nil
}

// readUnionHeaderSchema reads a .pulse header and schema block from rs,
// coded as predict codes them.
func readUnionHeaderSchema(rs io.Reader) (*encoding.Schema, error) {
	v, err := encoding.ReadHeader(rs)
	if err != nil {
		return nil, errors.NewCodedError(headerErrorCode(err), "invalid pulse file header: "+err.Error())
	}
	schema, err := encoding.ReadSchema(rs, v)
	if err != nil {
		return nil, errors.NewCodedError(errors.ENCODING_INVALID, "invalid pulse schema: "+err.Error())
	}
	return schema, nil
}

// UnionSource is one union member handed to PredictUnion: the member's
// label, its cohort as written, and its bytes positioned at the start
// (a single file, a shard archive, or an anchor's extracted shard).
type UnionSource struct {
	Label  string
	Cohort *types.Cohort
	R      io.ReadSeeker
}

// PredictUnion is Predict for a request whose Union names the
// members: the hidden-slot gate and item expansion as Predict runs
// them, then the schema-free union rules (UnionShapeRefusal), then every
// member read header-only and merged (MergeUnion), and the whole request
// judged over the merged schema — the virtual source field included —
// with the summed record count bounding sizes, limits and matrix
// estimates. PredictResult.RecordCount is the union's total;
// PredictResult.Union lists each member. members must align with
// req.Union (the facade opens them in declared order).
func PredictUnion(members []UnionSource, req *types.Request, opts *PredictOptions) *descriptor.Envelope {
	if opts == nil {
		opts = &PredictOptions{}
	}
	if serr := SlotRefusal(req, opts.instance()); serr != nil {
		return unionRefused(req, opts, serr)
	}
	if uerr := UnionExecRefusal(req); uerr != nil {
		return unionRefused(req, opts, uerr)
	}
	written := req
	expanded, fan, finding, xerr := ExpandRequestItems(req, opts.instance().Limits(), opts.instance().SweepOptions())
	if xerr != nil {
		env := predictForEachRefusal(req, xerr, finding)
		if result, ok := env.Data.(*descriptor.PredictResult); ok {
			result.PostWindow = earlyPostWindow(req, opts)
		}
		return env
	}
	env := predictUnionMembers(members, expanded, opts)
	if result, ok := env.Data.(*descriptor.PredictResult); ok {
		if len(fan) > 0 {
			result.Request = written
			result.ForEach = forEachSummaries(fan)
		}
		if result.PostWindow == nil {
			result.PostWindow = earlyPostWindow(expanded, opts)
		}
	}
	return env
}

// unionRefused is the envelope of a union refused before any member is
// read.
func unionRefused(req *types.Request, opts *PredictOptions, err error) *descriptor.Envelope {
	result := newPredictResult(req)
	result.Valid = false
	result.PostWindow = earlyPostWindow(req, opts)
	env := descriptor.NewEnvelope(result)
	addCodedError(env, err)
	return env
}

// predictUnionMembers reads and merges the members, then predicts req
// over the merged schema.
func predictUnionMembers(members []UnionSource, req *types.Request, opts *PredictOptions) *descriptor.Envelope {
	result := newPredictResult(req)
	env := descriptor.NewEnvelope(result)
	schemas := make([]UnionMemberSchema, 0, len(members))
	infos := make([]descriptor.UnionMemberPredict, 0, len(members))
	var shardRecords []int64
	var total int64
	known := true
	for i, m := range members {
		read, err := readUnionMember(m.R)
		if err != nil {
			addCodedError(env, LocateUnionMember(err, i, m.Label))
			result.Valid = false
			return env
		}
		info := descriptor.UnionMemberPredict{Label: m.Label, Shards: max(len(read.records), 1)}
		if m.Cohort != nil {
			info.Cohort = m.Cohort.Filename
		}
		if read.records == nil {
			known = false
		}
		for _, n := range read.records {
			info.RecordCount += n
		}
		total += info.RecordCount
		shardRecords = append(shardRecords, read.records...)
		infos = append(infos, info)
		schemas = append(schemas, UnionMemberSchema{Label: m.Label, Schema: read.schema, Shards: info.Shards})
	}
	merged, warnings, err := MergeUnion(schemas, UnionSourceFieldName(req))
	for _, w := range warnings {
		env.AddWarning(w.Code, w.Message, w.Details)
	}
	if err != nil {
		addCodedError(env, err)
		result.Valid = false
		return env
	}
	var records *int64
	if known {
		records = &total
	} else {
		shardRecords = nil
	}
	predictOverSchema(env, result, merged, req, opts, records, shardRecords, 0, nil)
	result.RecordCount = total
	result.Union = infos
	return env
}

// LocateUnionMember adds the member index and label to a member read
// fault's details (a fault that is not a *errors.CodedError is
// returned unchanged). Predict and the runtime locate a member they
// cannot read the same way.
func LocateUnionMember(err error, i int, label string) error {
	ce, ok := err.(*errors.CodedError)
	if !ok {
		return err
	}
	details := map[string]any{}
	for k, v := range ce.Details {
		details[k] = v
	}
	details["member"], details["label"] = i, label
	return errors.NewCodedErrorWithDetails(ce.Code, ce.Message, details)
}

// unionCounts sums the members' header counts into the union's: the
// flattened per-shard counts (a single file contributes its one count)
// and their total, with no archive terms (the union holds no central
// directory and no recorded distinct counts) — the figures the
// runtime's union cohort reports to its limit pre-flight. Any member's
// unknown count makes the union's unknown.
func unionCounts(members []CohortCounts) CohortCounts {
	out := CohortCounts{}
	for _, c := range members {
		if c.Records < 0 {
			return unknownCounts
		}
		if len(c.ShardRecords) == 0 {
			out.ShardRecords = append(out.ShardRecords, c.Records)
		} else {
			out.ShardRecords = append(out.ShardRecords, c.ShardRecords...)
		}
		out.Records += c.Records
	}
	return out
}

// unionRequestSchema reads a union request's members through
// opts.SchemaLoader (each member's counts through opts.cohortCounts)
// and merges them (MergeUnion) — the schema a Compose slot carrying a
// union executes over, with its counts. ok is false when a member's
// schema cannot be read (no judgement, as for an unreadable single
// cohort); err is the merge's refusal.
func unionRequestSchema(req *types.Request, opts *PredictOptions) (schema *encoding.Schema, counts CohortCounts, ok bool, err error) {
	if req == nil || len(req.Union) == 0 || opts == nil || opts.SchemaLoader == nil {
		return nil, unknownCounts, false, nil
	}
	schemas := make([]UnionMemberSchema, 0, len(req.Union))
	memberCounts := make([]CohortCounts, 0, len(req.Union))
	for _, m := range req.Union {
		if m.Cohort == nil {
			return nil, unknownCounts, false, nil
		}
		s, lerr := opts.SchemaLoader(cohortPathFor(m.Cohort))
		if lerr != nil || s == nil {
			return nil, unknownCounts, false, nil
		}
		c := opts.cohortCounts(m.Cohort)
		memberCounts = append(memberCounts, c)
		schemas = append(schemas, UnionMemberSchema{Label: m.Label, Schema: s, Shards: max(len(c.ShardRecords), 1)})
	}
	merged, _, merr := MergeUnion(schemas, UnionSourceFieldName(req))
	if merr != nil {
		return nil, unknownCounts, false, merr
	}
	return merged, unionCounts(memberCounts), true, nil
}

// requestSchemaFor is the schema a Compose slot (or fit request) reads:
// its cohort's (cohortSchemaFor) or, for a union, the merged one; nil
// when it cannot be read or the union does not merge.
func requestSchemaFor(req *types.Request, opts *PredictOptions) *encoding.Schema {
	if req == nil {
		return nil
	}
	if len(req.Union) == 0 {
		return cohortSchemaFor(req.Cohort, opts)
	}
	if UnionShapeRefusal(req) != nil {
		return nil
	}
	schema, _, ok, _ := unionRequestSchema(req, opts)
	if !ok {
		return nil
	}
	return schema
}

// requestCounts is the counts a Compose slot's (or fit request's)
// limit pre-flight reads: its cohort's, or the union's summed ones.
func requestCounts(req *types.Request, opts *PredictOptions) CohortCounts {
	if req == nil {
		return unknownCounts
	}
	if len(req.Union) == 0 {
		return opts.cohortCounts(req.Cohort)
	}
	if UnionShapeRefusal(req) != nil {
		return unknownCounts
	}
	_, counts, ok, _ := unionRequestSchema(req, opts)
	if !ok {
		return unknownCounts
	}
	return counts
}

// cohortHead is how a non-Request predict root (facet, chain stage 0,
// materialize) reads the cohort it judges: its schema and header
// counts, or the fault it reports. readerHead reads one cohort's bytes;
// unionHead a union's members.
type cohortHead func(distinct bool) (*encoding.Schema, CohortCounts, *cohortHeadFault)

// readerHead is the cohortHead of one cohort's bytes (readCohortHead).
// A root carrying a union is never judged over one source: the shape
// rules, then PULSE_UNION_UNSUPPORTED naming operation (the facade
// routes a union root to its union twin instead); a source field set
// without a union is the shape rule's refusal too, as the runtime
// raises it when it opens the cohort.
func readerHead(fileData io.ReadSeeker, cohort *types.Cohort, union []types.UnionMember, sourceField, operation string) cohortHead {
	return func(distinct bool) (*encoding.Schema, CohortCounts, *cohortHeadFault) {
		if err := UnionShapeRefusalOf(cohort, union, sourceField); err != nil {
			return nil, CohortCounts{}, codedHeadFault(err)
		}
		if len(union) > 0 {
			return nil, CohortCounts{}, codedHeadFault(UnionUnsupported(operation + " over a single cohort source"))
		}
		return readCohortHead(fileData, distinct)
	}
}

// unionHead is the cohortHead of a union root: the shape rules
// (UnionShapeRefusalOf), then every member read header-only in
// declared order (readUnionMember; a fault located by member) and
// merged (MergeUnion — the runtime's rule). The counts are the union's:
// each flattened shard's header count and their sum, no archive terms
// (the union holds no central directory and no recorded distinct
// counts), unknown when any member's is — the figures the runtime's
// union cohort reports to its pre-flight. members must align with
// union (nil when the shape is refused: none is opened). The merge's
// description-class divergences are returned beside the head; the
// caller warns them on its envelope.
func unionHead(members []UnionSource, cohort *types.Cohort, union []types.UnionMember, sourceField string, warnings *[]encx.CohesionWarning) cohortHead {
	return func(bool) (*encoding.Schema, CohortCounts, *cohortHeadFault) {
		if err := UnionShapeRefusalOf(cohort, union, sourceField); err != nil {
			return nil, CohortCounts{}, codedHeadFault(err)
		}
		schemas := make([]UnionMemberSchema, 0, len(members))
		counts := CohortCounts{}
		known := true
		for i, m := range members {
			if _, err := m.R.Seek(0, io.SeekStart); err != nil {
				return nil, CohortCounts{}, codedHeadFault(LocateUnionMember(
					errors.WrapCodedError(err, errors.SERVICE_RESOURCE, "rewinding union member"), i, m.Label))
			}
			read, err := readUnionMember(m.R)
			if err != nil {
				return nil, CohortCounts{}, codedHeadFault(LocateUnionMember(err, i, m.Label))
			}
			if read.records == nil {
				known = false
			}
			for _, n := range read.records {
				counts.Records += n
			}
			counts.ShardRecords = append(counts.ShardRecords, read.records...)
			schemas = append(schemas, UnionMemberSchema{Label: m.Label, Schema: read.schema, Shards: max(len(read.records), 1)})
		}
		merged, warns, err := MergeUnion(schemas, UnionSourceFieldNameOf(sourceField))
		if warnings != nil {
			*warnings = warns
		}
		if err != nil {
			return nil, CohortCounts{}, codedHeadFault(err)
		}
		if !known {
			counts = unknownCounts
		}
		return merged, counts, nil
	}
}

// codedHeadFault is err as a validator's head fault: a coded error's
// code, message and details; any other error PROCESSING_INTERNAL.
func codedHeadFault(err error) *cohortHeadFault {
	if ce, ok := err.(*errors.CodedError); ok {
		return &cohortHeadFault{code: ce.Code, message: ce.Message, details: ce.Details}
	}
	return &cohortHeadFault{code: errors.PROCESSING_INTERNAL, message: err.Error()}
}

// addUnionWarnings warns each merge divergence on env, as predict over
// a union request and the runtime's union cohort raise them.
func addUnionWarnings(env *descriptor.Envelope, warnings []encx.CohesionWarning) {
	for _, w := range warnings {
		env.AddWarning(string(w.Code), w.Message, w.Details)
	}
}

// MaterializeSourceName is the source a materialize run names in its
// derived columns' descriptions ("derived from <base>"): the request
// cohort's path, or "union" for a union request — one spelling the
// run and its predict share.
func MaterializeSourceName(req *types.Request) string {
	if req != nil && len(req.Union) > 0 {
		return "union"
	}
	if req == nil || req.Cohort == nil {
		return ""
	}
	return cohortPathFor(req.Cohort)
}
