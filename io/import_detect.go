package io

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// Measured import predict and candidate parent-group detection.
//
// Plain ImportJob.Predict reads every row but converts nothing: it
// counts rows and finalizes nullability. When the job declares Groups,
// asks for ElideConstants, or sets SuggestGroups, Predict instead runs
// the MEASURED pass: every row is converted through the same
// rowConverter Run uses, and each group is measured over the exact
// bytes the import would write. The figures are then derived by the
// same encoding.DedupGate / encoding.AssessGroup / PlanConstantElision
// code the import calls, so predict, import and inspect cannot disagree
// about the same data. Nothing is written, and detection never changes
// what an import does — a candidate is a SUGGESTION the user declares
// with --group.
//
// Detection is two-phase because functional-dependency discovery is
// quadratic in the field count:
//
//  1. Nominate over a bounded WINDOW of the first rows (up to
//     detectWindowMaxRows, fewer on a wide schema — see detectWindow).
//     A single field K is a candidate key when it repeats (at least one
//     row in five carries a key value already seen) and at least one
//     other field is constant within every K value in the window. At
//     most detectMaxKeys keys are evaluated, the highest-cardinality
//     first (entity IDs before small enums), and at most
//     detectMaxCandidates candidates go forward.
//  2. Confirm and measure over the FULL pass, window rows included. A
//     member that varies within its key anywhere in the file is dropped
//     from the candidate (reported under rejected_members), and the
//     candidate's dictionary is built exactly as the encoder would build
//     it. The reported figures are therefore not a sample estimate:
//     they are what this import would produce.
//
// The 500-row inference sample is NOT used for nomination: at 12-13x
// fanout it holds ~40 parents, and a column constant across so few
// parents is indistinguishable from a true parent attribute. The window
// is 20x larger, and nomination only proposes — the full pass decides.

const (
	// detectWindowMaxRows / detectWindowMinRows bound the nomination
	// window; detectWindowCells bounds rows × fields and
	// detectWindowBytes the buffered bytes, so a wide schema gets a
	// shorter window instead of a quadratic blow-up.
	detectWindowMaxRows = 10000
	detectWindowMinRows = 1000
	detectWindowCells   = 4_000_000
	detectWindowBytes   = 64 << 20
	// detectMaxKeys caps the candidate keys whose dependencies are
	// evaluated over the window: the nomination cost is
	// O(keys × fields × window rows).
	detectMaxKeys = 64
	// detectMaxCandidates caps the candidates confirmed on the full
	// pass, ranked by their window-projected saving.
	detectMaxCandidates = 16
	// detectMemoryBudget bounds the dictionaries the confirming pass
	// holds, split evenly across candidates. A candidate that outgrows
	// its share stops being measured (verdict unmeasured).
	detectMemoryBudget = 512 << 20
	// detectMapOverhead approximates the per-entry cost of a Go map
	// entry keyed by a short string, for the memory budget.
	detectMapOverhead = 64
)

// Candidate verdicts beyond the gate's own (encoding.GroupVerdict*).
const (
	// CandidateVerdictUnmeasured: the candidate outgrew its share of
	// the detection memory budget before the pass ended; no figures.
	CandidateVerdictUnmeasured = "unmeasured"
	// CandidateReasonMemoryBound is the Reason on an unmeasured
	// candidate.
	CandidateReasonMemoryBound = "detection_memory_bound"
)

// GroupDetection is import predict's candidate parent-group report
// (ImportJob.SuggestGroups). Candidates are SUGGESTIONS: declaring one
// is always the user's decision (--group / ImportJob.Groups).
type GroupDetection struct {
	// Rows is the number of rows every candidate was measured over —
	// the rows the import would write (row-error rows excluded).
	Rows int64 `json:"rows"`
	// WindowRows is how many leading rows nomination looked at;
	// WindowBound is the most it would have looked at for this schema.
	WindowRows  int `json:"window_rows"`
	WindowBound int `json:"window_bound"`
	// FieldsConsidered counts the fields eligible as key or member (not
	// declared in a group, not constant across the window).
	FieldsConsidered int `json:"fields_considered"`
	// KeysEvaluated / KeysOverBound: candidate keys whose dependencies
	// were evaluated, and those skipped past detectMaxKeys.
	KeysEvaluated int `json:"keys_evaluated"`
	KeysOverBound int `json:"keys_over_bound"`
	// CandidatesOverBound counts nominated candidates not confirmed
	// because detectMaxCandidates were already carried forward.
	CandidatesOverBound int `json:"candidates_over_bound"`
	// CandidatesDisproved counts nominated candidates every one of
	// whose members varied within the key later in the file.
	CandidatesDisproved int `json:"candidates_disproved"`
	// Candidates, best projected saving first. Every measured candidate
	// is listed with its verdict — one below the width or ratio floor
	// says so rather than disappearing.
	Candidates []GroupCandidate `json:"candidates"`
	// Suggested holds the --group values of the admitted candidates
	// that do not overlap a better one, in rank order: the declaration
	// set detection recommends. Empty when no candidate is viable.
	Suggested []string `json:"suggested"`
}

// GroupCandidate is one detected parent group and its measured figures.
// The figures carry the same names and meaning as GroupReport's.
type GroupCandidate struct {
	Label string `json:"label"`
	// Key and Members are the structured declaration — the GroupDecl
	// shape {key, members}; Declaration is the same as a ready-to-paste
	// --group value (empty when a name holds ',' or ':', which the CLI
	// form cannot carry).
	Key         []string `json:"key"`
	Members     []string `json:"members"`
	Declaration string   `json:"declaration"`
	// Suggested marks a candidate in GroupDetection.Suggested.
	// OverlapsWith names the better-ranked suggested candidate sharing
	// a field with this one (a field belongs to at most one group).
	Suggested    bool   `json:"suggested"`
	OverlapsWith string `json:"overlaps_with,omitempty"`
	// RejectedMembers were constant within the key across the
	// nomination window but varied later in the file.
	RejectedMembers []string `json:"rejected_members,omitempty"`
	// Verdict / Reason as the viability gate judges the group
	// (encoding.GroupVerdict*), or CandidateVerdictUnmeasured.
	Verdict         string  `json:"verdict"`
	Reason          string  `json:"reason,omitempty"`
	EntryCount      int     `json:"entry_count"`
	EntryWidth      int     `json:"entry_width"`
	MemberRowBytes  int     `json:"member_row_bytes"`
	IndexWidth      int     `json:"index_width"`
	DictionaryBytes int64   `json:"dictionary_bytes"`
	Ratio           float64 `json:"ratio"`
	BreakEvenRatio  float64 `json:"break_even_ratio"`
	RatioFloor      float64 `json:"ratio_floor"`
	ByteDelta       int64   `json:"byte_delta"`
	GrowsFile       bool    `json:"grows_file"`
	// ProjectedFileBytes is the exact cohort size with this group alone
	// declared (compare ImportProjection.FlatFileBytes).
	ProjectedFileBytes int64 `json:"projected_file_bytes"`
}

// ImportProjection is the measured pass's view of the file the import
// would write.
type ImportProjection struct {
	// RowsImported / RowErrors: rows that would import, and rows that
	// would be skipped with a row error.
	RowsImported int `json:"rows_imported"`
	RowErrors    int `json:"row_errors"`
	// FlatFileBytes is the exact size of the 0x01 cohort (no groups,
	// no elision).
	FlatFileBytes int64 `json:"flat_file_bytes"`
	// ProjectedFileBytes is the exact size of the cohort this job would
	// write: its admitted Groups plus, with ElideConstants, the
	// constant group. Equal to FlatFileBytes when neither applies.
	ProjectedFileBytes int64 `json:"projected_file_bytes"`
	// ElisionBytesSaved is what ElideConstants would save (0 when off
	// or nothing is elided).
	ElisionBytesSaved int64 `json:"elision_bytes_saved,omitempty"`
}

// measuring reports whether Predict must run the measured pass.
func (j *ImportJob) measuring() bool {
	return j.SuggestGroups || len(j.Groups) > 0 || j.ElideConstants
}

// measuredLayout is where each field of an import's logical row sits. The
// measured pass always lays rows out with a full ceil(fields/8) null
// bitmap after the field bytes, whatever the final nullability — a
// later out-of-sample null can still promote a field, so the final
// layout is not known until the pass ends.
type measuredLayout struct {
	offs   []int
	widths []int
	bmOff  int
	stride int
}

func newMeasuredLayout(s *encoding.Schema) measuredLayout {
	l := measuredLayout{offs: make([]int, len(s.Fields)), widths: make([]int, len(s.Fields))}
	for i := range s.Fields {
		w := s.Fields[i].Type.ByteSize()
		if s.Fields[i].Type.IsBitPacked() {
			w = 1
		}
		l.offs[i], l.widths[i] = l.bmOff, w
		l.bmOff += w
	}
	l.stride = l.bmOff + (len(s.Fields)+7)/8
	return l
}

func (l *measuredLayout) cell(row []byte, fi int) []byte {
	return row[l.offs[fi] : l.offs[fi]+l.widths[fi]]
}

func (l *measuredLayout) isNull(row []byte, fi int) bool {
	return encoding.BitmapIsNull(row[l.bmOff:], fi)
}

// fdTracker builds one group's dictionary over the measured pass, in
// the encoder's entry layout (member bytes in ascending field order,
// then a member null bitmap), keyed by the key fields' bytes and null
// bits exactly as encoding.GroupEncoder keys it. A declared keyed group
// reports the first member that varies within its key (Run fails there
// with PULSE_GROUP_MEMBER_NOT_CONSTANT); a candidate drops that member
// and carries on.
type fdTracker struct {
	l        *measuredLayout
	fields   []int  // key ∪ members, ascending field index
	isKey    []bool // parallel to fields
	alive    []bool // parallel; a key is always alive
	nMembers int    // alive non-key fields
	keyAll   bool   // tuple group: every field is key
	entryLen int
	keyOf    map[string]uint32
	entries  []byte
	key      []byte
	keyBM    []byte
	maxRows  int // entry cap; 0 = none
	over     bool
}

func newFDTracker(l *measuredLayout, keys, members []int, maxEntries int) *fdTracker {
	t := &fdTracker{l: l, keyOf: map[string]uint32{}, maxRows: maxEntries}
	isKey := map[int]bool{}
	for _, k := range keys {
		isKey[k] = true
	}
	all := append(append([]int(nil), keys...), members...)
	sort.Ints(all)
	t.keyAll = len(members) == 0
	for _, fi := range all {
		t.fields = append(t.fields, fi)
		t.isKey = append(t.isKey, isKey[fi] || t.keyAll)
		t.alive = append(t.alive, true)
		t.entryLen += l.widths[fi]
		if !isKey[fi] && !t.keyAll {
			t.nMembers++
		}
	}
	t.entryLen += (len(all) + 7) / 8
	return t
}

// entryCost is the budget cost of one dictionary entry.
func (t *fdTracker) entryCost() int {
	kl := 0
	for k, fi := range t.fields {
		if t.isKey[k] {
			kl += t.l.widths[fi] + 1
		}
	}
	return t.entryLen + kl + detectMapOverhead
}

// observe folds one measured row in and returns the field index of the
// first member that disagreed with the entry its key selected (-1 when
// none). A disagreeing member stops being tracked.
func (t *fdTracker) observe(row []byte) int {
	if t.over || (!t.keyAll && t.nMembers == 0) {
		return -1
	}
	t.key = t.key[:0]
	nk := 0
	for k, fi := range t.fields {
		if t.isKey[k] {
			t.key = append(t.key, t.l.cell(row, fi)...)
			nk++
		}
	}
	bm := t.keyBM[:0]
	for i := 0; i < (nk+7)/8; i++ {
		bm = append(bm, 0)
	}
	t.keyBM = bm
	b := 0
	for k, fi := range t.fields {
		if t.isKey[k] {
			if t.l.isNull(row, fi) {
				encoding.BitmapSetNull(bm, b)
			}
			b++
		}
	}
	t.key = append(t.key, bm...)
	if idx, ok := t.keyOf[string(t.key)]; ok {
		if t.keyAll {
			return -1
		}
		ent := t.entries[int(idx)*t.entryLen : (int(idx)+1)*t.entryLen]
		ebm := ent[t.entryLen-(len(t.fields)+7)/8:]
		off, first := 0, -1
		for k, fi := range t.fields {
			w := t.l.widths[fi]
			if !t.isKey[k] && t.alive[k] {
				if !bytes.Equal(ent[off:off+w], t.l.cell(row, fi)) ||
					encoding.BitmapIsNull(ebm, k) != t.l.isNull(row, fi) {
					t.alive[k] = false
					t.nMembers--
					if first < 0 {
						first = fi
					}
				}
			}
			off += w
		}
		if !t.keyAll && t.nMembers == 0 {
			t.keyOf, t.entries = nil, nil
		}
		return first
	}
	if t.maxRows > 0 && len(t.keyOf) >= t.maxRows {
		t.over = true
		t.keyOf, t.entries = nil, nil
		return -1
	}
	start := len(t.entries)
	for _, fi := range t.fields {
		t.entries = append(t.entries, t.l.cell(row, fi)...)
	}
	t.entries = append(t.entries, make([]byte, (len(t.fields)+7)/8)...)
	ebm := t.entries[start+t.entryLen-(len(t.fields)+7)/8:]
	for k, fi := range t.fields {
		if t.l.isNull(row, fi) {
			encoding.BitmapSetNull(ebm, k)
		}
	}
	t.keyOf[string(t.key)] = uint32(len(t.keyOf))
	return -1
}

// finalEntries re-lays the dictionary out as the encoder would write it
// over final (the schema after every promotion) for the live fields
// only: their bytes in ascending order, then a member null bitmap iff
// any live field is nullable in final.
func (t *fdTracker) finalEntries(final *encoding.Schema) []byte {
	var live []int
	nullable := false
	for k, fi := range t.fields {
		if t.alive[k] {
			live = append(live, k)
			nullable = nullable || final.Fields[fi].Nullable
		}
	}
	width := 0
	for _, k := range live {
		width += t.l.widths[t.fields[k]]
	}
	bmLen := 0
	if nullable {
		bmLen = (len(live) + 7) / 8
	}
	n := len(t.keyOf)
	out := make([]byte, 0, n*(width+bmLen))
	srcOffs := make([]int, len(t.fields))
	off := 0
	for k, fi := range t.fields {
		srcOffs[k] = off
		off += t.l.widths[fi]
	}
	for e := 0; e < n; e++ {
		ent := t.entries[e*t.entryLen : (e+1)*t.entryLen]
		ebm := ent[t.entryLen-(len(t.fields)+7)/8:]
		for _, k := range live {
			out = append(out, ent[srcOffs[k]:srcOffs[k]+t.l.widths[t.fields[k]]]...)
		}
		if nullable {
			bm := make([]byte, bmLen)
			for m, k := range live {
				if encoding.BitmapIsNull(ebm, k) {
					encoding.BitmapSetNull(bm, m)
				}
			}
			out = append(out, bm...)
		}
	}
	return out
}

// nominee is a candidate group proposed from the window.
type nominee struct {
	key     int
	members []int
	saving  int64
}

// detectWindow is the nomination window for a schema of nFields fields
// and measured stride bytes.
func detectWindow(nFields, stride int) int {
	w := detectWindowMaxRows
	if nFields > 0 && detectWindowCells/nFields < w {
		w = detectWindowCells / nFields
	}
	if stride > 0 && detectWindowBytes/stride < w {
		w = detectWindowBytes / stride
	}
	if w < detectWindowMinRows {
		w = detectWindowMinRows
	}
	return w
}

// nominate proposes single-field-key candidates from the buffered
// window rows. eligible marks fields that may be a key or member.
func nominate(l *measuredLayout, window []byte, eligible []bool, det *GroupDetection) []nominee {
	n := len(window) / l.stride
	if n < 2 {
		return nil
	}
	row := func(r int) []byte { return window[r*l.stride : (r+1)*l.stride] }
	// narrowCell is a cell of at most 8 bytes and its null bit — a
	// map key with no allocation; wider cells (decimal128, wide sets)
	// key by string.
	type narrowCell struct {
		v    uint64
		null bool
	}
	nf := len(eligible)
	distinct := make([]int, nf)
	// firstOf[f][r] is the first window row holding row r's value of
	// field f — a canonical value id, so "C is constant within K" is
	// firstOf[C][r] == firstOf[C][firstOf[K][r]] for every row r:
	// integer compares, no byte re-reads.
	firstOf := make([][]int32, nf)
	var keys []int
	for fi := 0; fi < nf; fi++ {
		if !eligible[fi] {
			continue
		}
		par := make([]int32, n)
		if l.widths[fi] <= 8 {
			seen := make(map[narrowCell]int32, 64)
			for r := 0; r < n; r++ {
				var k narrowCell
				for _, b := range l.cell(row(r), fi) {
					k.v = k.v<<8 | uint64(b)
				}
				k.null = l.isNull(row(r), fi)
				p, ok := seen[k]
				if !ok {
					p = int32(r)
					seen[k] = p
				}
				par[r] = p
			}
			distinct[fi] = len(seen)
		} else {
			seen := make(map[string]int32, 64)
			for r := 0; r < n; r++ {
				k := string(l.cell(row(r), fi))
				if l.isNull(row(r), fi) {
					k += "\x01"
				}
				p, ok := seen[k]
				if !ok {
					p = int32(r)
					seen[k] = p
				}
				par[r] = p
			}
			distinct[fi] = len(seen)
		}
		if distinct[fi] == 1 {
			eligible[fi] = false // constant across the window
			continue
		}
		firstOf[fi] = par
	}
	for fi := 0; fi < nf; fi++ {
		if !eligible[fi] {
			continue
		}
		det.FieldsConsidered++
		// A key must repeat: at least one row in five carries a value
		// already seen. A near-unique field "determines" every other
		// field trivially — most of its values occur once.
		if repeats := n - distinct[fi]; repeats >= 1 && repeats*5 >= n {
			keys = append(keys, fi)
		}
	}
	// Highest cardinality first: entity identifiers before small enums.
	sort.SliceStable(keys, func(a, b int) bool { return distinct[keys[a]] > distinct[keys[b]] })
	if len(keys) > detectMaxKeys {
		det.KeysOverBound = len(keys) - detectMaxKeys
		keys = keys[:detectMaxKeys]
	}
	det.KeysEvaluated = len(keys)

	var out []nominee
	// equivalent[k]: k is a member of an evaluated key with the same
	// distinct count — the two are 1:1 over the window, so k's candidate
	// would be the same field set: it is neither evaluated nor listed
	// twice.
	equivalent := make([]bool, nf)
	for _, k := range keys {
		if equivalent[k] {
			continue
		}
		par := firstOf[k]
		var members []int
		for c := 0; c < nf; c++ {
			if c == k || !eligible[c] {
				continue
			}
			pc := firstOf[c]
			ok := true
			for r := 0; r < n; r++ {
				if pc[r] != pc[par[r]] {
					ok = false
					break
				}
			}
			if ok {
				members = append(members, c)
				if distinct[c] == distinct[k] {
					equivalent[c] = true
				}
			}
		}
		if len(members) == 0 {
			continue
		}
		width := l.widths[k]
		for _, fi := range members {
			width += l.widths[fi]
		}
		saving := int64(n)*int64(width-encoding.GroupIndexWidth) - int64(distinct[k])*int64(width)
		out = append(out, nominee{key: k, members: members, saving: saving})
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].saving > out[b].saving })
	if len(out) > detectMaxCandidates {
		det.CandidatesOverBound = len(out) - detectMaxCandidates
		out = out[:detectMaxCandidates]
	}
	return out
}

// predictMeasured is Predict's measured pass over an already-resolved
// schema. The reader is positioned at the first data row.
func (j *ImportJob) predictMeasured(ctx context.Context, schema *encoding.Schema, inferred bool, delimFor func(string) string, report *PredictReport) error {
	gate := j.dedupGate()
	var (
		specs      []encoding.GroupSpec
		screen     []encoding.GroupViability
		groupWarns []*errors.CodedError
	)
	if declared := groupSpecs(j.Groups); len(declared) > 0 {
		if _, err := encoding.NewGroupEncoder(schema, declared); err != nil {
			return err
		}
		var err error
		if specs, screen, groupWarns, err = gate.ScreenWidths(schema, declared); err != nil {
			return err
		}
	}

	// Private dictionaries: the reported schema is left exactly as the
	// plain predict reports it, while conversion assigns the same IDs
	// Run would (clones start from the same entries).
	dicts := make(map[int]*encoding.Dictionary)
	for i := range schema.Fields {
		if !schema.Fields[i].Type.HasDictionary() {
			continue
		}
		d := encoding.NewDictionary()
		if src := schema.Fields[i].Dictionary; src != nil {
			for _, v := range src.Values() {
				_, _ = d.Add(v)
			}
		}
		dicts[i] = d
	}
	conv := newRowConverter(schema, inferred, dicts, delimFor)
	l := newMeasuredLayout(schema)
	byName := make(map[string]int, len(schema.Fields))
	for i := range schema.Fields {
		byName[schema.Fields[i].Name] = i
	}

	// Declared groups, tracked exactly as the encoder keys them.
	declTrackers := make([]*fdTracker, len(specs))
	reserved := make([]bool, len(schema.Fields))
	for g, sp := range specs {
		var keys, members []int
		isKey := map[string]bool{}
		for _, k := range sp.Key {
			isKey[k] = true
		}
		for _, name := range sp.Members {
			reserved[byName[name]] = true
			if len(sp.Key) == 0 || isKey[name] {
				keys = append(keys, byName[name])
			} else {
				members = append(members, byName[name])
			}
		}
		declTrackers[g] = newFDTracker(&l, keys, members, 0)
	}

	// Constancy over every row, as encoding.ConstantDetector decides it.
	// The detector sees the measured layout (full bitmap), which is the
	// logical row of an all-nullable twin of the schema: a field that
	// ends non-nullable has a zero null bit on every row, so the verdict
	// matches the final schema's.
	twin := &encoding.Schema{Fields: append([]encoding.Field(nil), schema.Fields...)}
	for i := range twin.Fields {
		twin.Fields[i].Nullable = true
	}
	det, err := encoding.NewConstantDetector(twin)
	if err != nil {
		return err
	}

	var detection *GroupDetection
	window := 0
	var windowBuf []byte
	nominated := false
	var cands []*fdTracker
	var nominees []nominee
	if j.SuggestGroups {
		detection = &GroupDetection{Candidates: []GroupCandidate{}, Suggested: []string{}}
		window = detectWindow(len(schema.Fields), l.stride)
		detection.WindowBound = window
		windowBuf = make([]byte, 0, window*l.stride)
	}
	startCandidates := func() {
		nominated = true
		n := len(windowBuf) / l.stride
		detection.WindowRows = n
		eligible := make([]bool, len(schema.Fields))
		for i := range eligible {
			eligible[i] = !reserved[i]
		}
		nominees = nominate(&l, windowBuf, eligible, detection)
		for _, nm := range nominees {
			t := newFDTracker(&l, []int{nm.key}, nm.members, 0)
			t.maxRows = detectMemoryBudget / len(nominees) / t.entryCost()
			cands = append(cands, t)
		}
		for r := 0; r < n; r++ {
			for _, t := range cands {
				t.observe(windowBuf[r*l.stride : (r+1)*l.stride])
			}
		}
		windowBuf = nil
	}

	var (
		rowNum, rows int
		rowErrs      []RowError
		lrow         bytes.Buffer
		bm           = make([]byte, (len(schema.Fields)+7)/8)
	)
	nullSource, _ := j.Source.(NullAwareReader)
	err = j.Source.ReadRows(ctx, func(row []string) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		rowNum++
		var declaredNulls []bool
		if nullSource != nil {
			declaredNulls = nullSource.RowNulls()
		}
		if re := conv.convert(rowNum, row, declaredNulls); re != nil {
			rowErrs = append(rowErrs, RowError{Row: re.Row})
			return nil
		}
		lrow.Reset()
		if err := conv.writeFields(&lrow); err != nil {
			return err
		}
		clear(bm)
		conv.fillBitmap(bm)
		lrow.Write(bm)
		b := lrow.Bytes()
		if err := det.Observe(b); err != nil {
			return err
		}
		for g, t := range declTrackers {
			if fi := t.observe(b); fi >= 0 {
				label := specs[g].Label(g)
				return withSourceRow(errors.NewCodedErrorWithDetails(errors.PULSE_GROUP_MEMBER_NOT_CONSTANT,
					fmt.Sprintf("parent group: %s: member is not constant within the group's key: field %q at record %d", label, schema.Fields[fi].Name, rows),
					map[string]any{"group": g, "group_label": label, "field": schema.Fields[fi].Name, "row": int64(rows)}), rowErrs)
			}
		}
		if detection != nil {
			if !nominated {
				windowBuf = append(windowBuf, b...)
				if len(windowBuf)/l.stride >= window {
					startCandidates()
				}
			} else {
				for _, t := range cands {
					t.observe(b)
				}
			}
		}
		rows++
		return nil
	})
	if err != nil {
		return err
	}
	report.EstimatedRows = rowNum
	if detection != nil && !nominated {
		startCandidates()
	}

	for _, name := range conv.promotedNames() {
		report.Warnings = append(report.Warnings, InferenceWarning{
			Column:  name,
			Message: "null found outside the inference sample window; field promoted to nullable",
		})
	}

	// sizing is the final schema carrying the dictionaries the pass
	// built — what Run would write for the flat cohort.
	sizing := &encoding.Schema{Fields: append([]encoding.Field(nil), schema.Fields...)}
	for i, d := range dicts {
		sizing.Fields[i].Dictionary = d
	}
	flatPre, err := preambleBytes(sizing)
	if err != nil {
		return err
	}
	proj := &ImportProjection{
		RowsImported:  rows,
		RowErrors:     len(rowErrs),
		FlatFileBytes: flatPre + int64(rows)*int64(sizing.RecordByteSize()),
	}
	proj.ProjectedFileBytes = proj.FlatFileBytes
	report.Projection = proj

	// Declared groups: the same ratio gate Run applies, over the
	// dictionaries Run would build.
	allSpecs := append([]encoding.GroupSpec(nil), specs...)
	if j.ElideConstants {
		plan, err := encoding.PlanConstantElisionFor(sizing, det.ConstantFields(), int64(rows), groupMemberNames(specs))
		if err != nil {
			return err
		}
		if plan.Spec != nil {
			report.ElidedConstants = plan.Fields
			proj.ElisionBytesSaved = plan.BytesSaved
			allSpecs = append(allSpecs, *plan.Spec)
		}
	}
	grouped := sizing
	if len(allSpecs) > 0 {
		if grouped, err = groupedSizing(sizing, allSpecs, declTrackers); err != nil {
			return err
		}
		pre, err := preambleBytes(grouped)
		if err != nil {
			return err
		}
		proj.ProjectedFileBytes = pre + int64(rows)*int64(grouped.RecordByteSize())
	}
	if len(j.Groups) > 0 {
		ratio, ratioWarns, err := gate.AssessRatios(grouped, specs, int64(rows))
		if err != nil {
			return err
		}
		report.Groups = groupReports(grouped, j.Groups, screen, ratio)
		report.GroupWarnings = append(groupWarns, ratioWarns...)
	}

	if detection != nil {
		detection.Rows = int64(rows)
		if err := measureCandidates(detection, sizing, nominees, cands, gate.Floor(), int64(rows), proj.FlatFileBytes); err != nil {
			return err
		}
		report.GroupCandidates = detection
	}
	return nil
}

// groupedSizing builds the grouped schema specs would produce over
// sizing, with the first len(trackers) groups' dictionaries taken from
// the trackers and any remaining (constant) group holding a stand-in
// entry of the right width.
func groupedSizing(sizing *encoding.Schema, specs []encoding.GroupSpec, trackers []*fdTracker) (*encoding.Schema, error) {
	enc, err := encoding.NewGroupEncoder(sizing, specs)
	if err != nil {
		return nil, err
	}
	if _, err := enc.EncodeRow(nil, make([]byte, enc.LogicalStride())); err != nil {
		return nil, err
	}
	grouped := enc.Schema()
	for g, t := range trackers {
		grouped.Groups[g].Entries = t.finalEntries(sizing)
	}
	return grouped, nil
}

// measureCandidates turns the confirmed trackers into ranked, judged
// candidates and picks the non-overlapping suggested set.
func measureCandidates(det *GroupDetection, sizing *encoding.Schema, nominees []nominee, cands []*fdTracker, floor float64, rows, flatBytes int64) error {
	gate := encoding.DedupGate{RatioFloor: floor} // candidates are never strict
	for i, t := range cands {
		key := sizing.Fields[nominees[i].key].Name
		var members, rejected []string
		for k, fi := range t.fields {
			if t.isKey[k] {
				continue
			}
			if t.alive[k] {
				members = append(members, sizing.Fields[fi].Name)
			} else {
				rejected = append(rejected, sizing.Fields[fi].Name)
			}
		}
		if len(members) == 0 {
			det.CandidatesDisproved++
			continue
		}
		c := GroupCandidate{Key: []string{key}, Members: members, RejectedMembers: rejected, RatioFloor: gate.Floor()}
		if !strings.ContainsAny(key+strings.Join(members, ""), ",:") {
			c.Declaration = key + ":" + strings.Join(members, ",")
		}
		if t.over {
			c.Verdict, c.Reason = CandidateVerdictUnmeasured, CandidateReasonMemoryBound
			det.Candidates = append(det.Candidates, c)
			continue
		}
		spec := GroupDecl{Key: c.Key, Members: members}.spec()
		_, screen, _, err := gate.ScreenWidths(sizing, []encoding.GroupSpec{spec})
		if err != nil {
			return err
		}
		v := screen[0]
		if v.Verdict != encoding.GroupVerdictDroppedTooNarrow {
			grouped, err := groupedSizing(sizing, []encoding.GroupSpec{spec}, []*fdTracker{t})
			if err != nil {
				return err
			}
			views, _, err := gate.AssessRatios(grouped, []encoding.GroupSpec{spec}, rows)
			if err != nil {
				return err
			}
			v = views[0]
			pre, err := preambleBytes(grouped)
			if err != nil {
				return err
			}
			c.ProjectedFileBytes = pre + rows*int64(grouped.RecordByteSize())
			_, c.GrowsFile = v.Finding()
		} else {
			c.ProjectedFileBytes = flatBytes
		}
		c.Verdict, c.Reason = v.Verdict, v.Reason
		c.EntryCount, c.EntryWidth, c.MemberRowBytes, c.IndexWidth = v.EntryCount, v.EntryWidth, v.MemberRowBytes, v.IndexWidth
		c.DictionaryBytes, c.Ratio, c.BreakEvenRatio, c.ByteDelta = v.DictionaryBytes, v.Ratio, v.BreakEvenRatio, v.ByteDelta
		det.Candidates = append(det.Candidates, c)
	}

	// Rank: measured candidates by saving (most negative byte delta
	// first), then the dropped and unmeasured ones.
	rank := func(c GroupCandidate) int {
		switch c.Verdict {
		case encoding.GroupVerdictAdmitted, encoding.GroupVerdictLowRatio:
			return 0
		case encoding.GroupVerdictDroppedTooNarrow:
			return 1
		}
		return 2
	}
	sort.SliceStable(det.Candidates, func(a, b int) bool {
		ca, cb := det.Candidates[a], det.Candidates[b]
		if rank(ca) != rank(cb) {
			return rank(ca) < rank(cb)
		}
		return ca.ByteDelta < cb.ByteDelta
	})
	claimed := map[string]string{}
	for i := range det.Candidates {
		c := &det.Candidates[i]
		c.Label = fmt.Sprintf("candidate %d [key: %s]", i+1, strings.Join(c.Key, ","))
		if c.Verdict != encoding.GroupVerdictAdmitted {
			continue
		}
		fields := append(append([]string(nil), c.Key...), c.Members...)
		for _, f := range fields {
			if by, ok := claimed[f]; ok {
				c.OverlapsWith = by
				break
			}
		}
		if c.OverlapsWith != "" || c.Declaration == "" {
			continue
		}
		c.Suggested = true
		det.Suggested = append(det.Suggested, c.Declaration)
		for _, f := range fields {
			claimed[f] = c.Label
		}
	}
	return nil
}

// preambleBytes is the size of s's header + schema block.
func preambleBytes(s *encoding.Schema) (int64, error) {
	var cw byteCounter
	if err := encoding.WritePreamble(&cw, s); err != nil {
		return 0, err
	}
	return cw.n, nil
}

type byteCounter struct{ n int64 }

func (c *byteCounter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}
