package spss

// THE `.sav` WRITER, mounted on the io/ adapter contract.
//
// E5-S1 through E5-S5 built the encoder: the sidecar read, the dictionary
// emitter, the data section, the charset transcode and the name boundary.
// None of it was reachable from `pulse export` or `pulse convert`, because
// there was no pio.Writer. This file is that writer.
//
// # The contract mismatch, stated plainly
//
// pio.Writer is ROW-oriented — WriteHeader, then WriteRow per record, then
// Close — and [DataEncoder] is not. It reads a whole cohort, because a `.sav`
// variable's on-wire value is derived from things a rendered row no longer
// carries:
//
//   - A categorical's value comes from its DICTIONARY ID, which indexes the
//     plan's recorded SPSS code. io/export.go's row loop has already resolved
//     that ID to its label text, and the text cannot be looked up again: two
//     SPSS codes may legitimately share one label.
//   - A `set_*` column's members come from the MASK's bits. The row form is
//     the mask printed as a decimal string, and bit 63 does not survive the
//     float the renderer goes through.
//   - A null arrives as "", which a string categorical can hold as a real
//     value — the E4-S2 `<var>_missing` sibling uses exactly "" for "present".
//     Null and empty are the same cell in a row stream and different cells in
//     a `.sav`.
//
// So this writer takes BOTH paths, and which one it is on decides the
// fidelity it can offer. The choice is not a preference:
//
//  1. The COHORT path. ExportJob.Run hands over the `.pulse` path through
//     pio.CohortWriter and skips its row loop entirely. The encoder reads raw
//     storage, the metadata sidecar is consulted, and the emitted file is the
//     one E5-S2..S5 tested. This is `pulse export spss`.
//  2. The ROW path. There is no cohort — `pulse convert data.csv out.sav`
//     streams a CSV straight through ConvertJob, which never writes one. The
//     writer buffers the rows, builds an intermediate cohort in memory through
//     the ordinary import path, and then runs the cohort path over that. The
//     schema is INFERRED, because a CSV has no better one to offer, and there
//     is no sidecar, because a CSV never had one.
//
// The row path is not a lesser implementation of the same thing; it is the
// honest answer to a source that carries less. It is also why the writer
// buffers rather than streams: an intermediate cohort cannot be written until
// the last row has been seen. A convert of a very large CSV to `.sav` holds
// the table in memory, which ConvertJob's own KeepPulseAt path already does.
//
// # What it refuses
//
// A projection (ExportJob.Includes) or a label binding (ExportJob.Labels) is
// an output-time transformation of the ROW stream, and the cohort path never
// sees that stream. Silently emitting every column in answer to
// `--include age` would be the quiet wrong answer this effort exists to
// avoid, so both are refused with a coded error naming what was asked for.

import (
	"bufio"
	"bytes"
	"context"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// rowPathCohort is the path the row path's intermediate cohort is written
// to, on a filesystem that exists only for the duration of one Close.
//
// The name is chosen to be readable in a diagnostic, because it does
// surface in one: the row path has no metadata sidecar by construction, so
// the export raises PULSE_SPSS_SIDECAR_ABSENT naming this path. A caller
// converting a CSV should be able to read that message and understand that
// the cohort in question is the one their rows were just turned into,
// rather than a file they have lost.
const rowPathCohort = "converted-rows.pulse"

// Writer emits a `.sav` file.
//
// It satisfies pio.Writer, pio.SchemaAwareWriter, pio.CohortWriter,
// pio.CohortValidator and pio.TargetWarningEmitter. See the file comment for
// why the middle two are there — the row-oriented contract alone cannot
// express what a `.sav` variable's value is derived from, nor whether it
// could be derived at all.
//
// A Writer is single-use: one Writer, one file. Close is where the bytes
// land, matching every other adapter in io/.
type Writer struct {
	fs   afero.Fs
	path string
	opts WriterOptions

	// schema is the source `.pulse` schema, delivered by SetPulseSchema
	// before WriteHeader. It is retained for diagnostics; the cohort path
	// re-reads the schema from the cohort itself, because that is the
	// schema the record stream is actually laid out to.
	schema *encoding.Schema

	// columns and rows are the ROW path's buffer. Empty on the cohort path,
	// where WriteRow is never called.
	columns []string
	rows    [][]string

	// out is the emitted file, and done reports that the encode has run.
	// Close writes out; a second Close is a no-op rather than a second
	// encode.
	out  []byte
	done bool

	// overlays are the layers the export pipeline asked to embed. They
	// are recorded and never written — see overlay.go.
	overlays []*types.OverlayLayer

	cases    int
	renames  []NameRename
	warnings []*errors.CodedError
}

// Compile-time assertions. The last three are the whole point of this file:
// a Writer that satisfied only pio.Writer would be handed rendered rows and
// would have to guess at the values that decide SPSS fidelity — and one that
// dropped pio.CohortValidator would go back to being predicted as though it
// could never refuse anything.
var (
	_ pio.Writer               = (*Writer)(nil)
	_ pio.SchemaAwareWriter    = (*Writer)(nil)
	_ pio.CohortWriter         = (*Writer)(nil)
	_ pio.CohortValidator      = (*Writer)(nil)
	_ pio.TargetWarningEmitter = (*Writer)(nil)
)

// NewWriter creates a `.sav` writer that lands its bytes at path on fs.
//
// opts is the caller's knob bag — [WriterOptions] — passed by value because
// it is settled before the first byte is written and nothing here mutates it.
// The CLI's four flags map onto it one for one.
func NewWriter(fs afero.Fs, path string, opts WriterOptions) *Writer {
	return &Writer{fs: fs, path: path, opts: opts}
}

// NewWriterToBuffer creates a `.sav` writer with no filesystem target; the
// emitted bytes are read back with [Writer.Bytes]. It is the fs-free unit-test
// path every adapter in io/ offers.
func NewWriterToBuffer(opts WriterOptions) *Writer {
	return &Writer{opts: opts}
}

// SetPulseSchema receives the source cohort's schema. pio.SchemaAwareWriter.
func (w *Writer) SetPulseSchema(s *encoding.Schema) { w.schema = s }

// WriteHeader records the emitted column names.
//
// On the cohort path the names are informational — the dictionary is built
// from the cohort's schema and, where there is one, its metadata sidecar. On
// the row path they are the header the intermediate import infers against.
func (w *Writer) WriteHeader(columns []string) error {
	w.columns = append([]string(nil), columns...)
	return nil
}

// WriteRow buffers one rendered row for the ROW path.
//
// It is never called on the cohort path: ExportJob.Run runs no row loop for
// a pio.CohortWriter. Values are stringified through the same canonical
// rendering every text adapter uses.
func (w *Writer) WriteRow(values []any) error {
	row := make([]string, len(values))
	for i, v := range values {
		row[i] = formatCell(v)
	}
	w.rows = append(w.rows, row)
	return nil
}

// WriteCohort encodes the cohort described by src. pio.CohortWriter.
//
// It is the fidelity path: raw storage in, the metadata sidecar consulted,
// nothing re-derived from rendered text.
func (w *Writer) WriteCohort(ctx context.Context, src pio.CohortSource) (int, error) {
	if len(src.Includes) > 0 {
		return 0, w.cannotProject("--include", strings.Join(src.Includes, ", "))
	}
	if src.Labelled {
		return 0, w.cannotProject("--labels",
			"a label binding rewrites or augments cells in the rendered row stream")
	}
	if err := w.encodeCohort(ctx, src.FS, src.Path); err != nil {
		return 0, err
	}
	return w.cases, nil
}

// ValidateCohort reports whether this cohort could be written as a `.sav`,
// without writing one. pio.CohortValidator.
//
// It runs the writer's NON-DATA pass — the sidecar resolution, the dictionary
// build, the name policy, the charset transcode, the derived fold and the
// encoder's own column checks — and throws the result away. That is the same
// code [Writer.WriteCohort] runs, called through the same helper rather than
// re-implemented, so a refusal predicted here is by construction the refusal
// the export returns: same check, same message, same code.
//
// What it CANNOT see is anything that needs a record. A value too wide for a
// declared string, a character the target charset cannot encode, a
// categorical ID the plan records no SPSS code for — every one of those is
// found by the data pass, which is never entered. Predict is a sound but
// incomplete filter, and that asymmetry is deliberate: refusing on a guess
// would block exports that would have succeeded.
//
// It leaves the Writer untouched — no bytes, no recorded warnings, no
// renames, and `done` unset — so validating is not a half-performed encode
// and a Writer stays usable afterwards.
func (w *Writer) ValidateCohort(ctx context.Context, src pio.CohortSource) ([]*errors.CodedError, error) {
	if len(src.Includes) > 0 {
		return nil, w.cannotProject("--include", strings.Join(src.Includes, ", "))
	}
	if src.Labelled {
		return nil, w.cannotProject("--labels",
			"a label binding rewrites or augments cells in the rendered row stream")
	}
	pass, err := w.planCohort(ctx, src.FS, src.Path)
	if err != nil {
		return nil, err
	}
	defer pass.close()
	return pass.warnings, nil
}

// Warnings returns the encode's non-fatal diagnostics.
// pio.TargetWarningEmitter.
//
// It is a pure accessor: it triggers nothing and is safe to call twice. The
// set is complete once the encode has run — which is inside WriteCohort on
// the cohort path and inside Close on the row path, so a caller that wants
// the row path's warnings must ask after Close.
func (w *Writer) Warnings() []*errors.CodedError { return w.warnings }

// Renames returns the variable renames [WriterOptions.SanitiseNames]
// performed, in emission order. Nil when the flag was unset or nothing
// needed rewriting. The same set rides Warnings as a single
// PULSE_SPSS_NAME_SANITISED; this is the typed form.
func (w *Writer) Renames() []NameRename { return w.renames }

// Close runs the ROW path's encode if it has not already run, then writes the
// emitted bytes.
//
// A second Close does not re-encode and does not re-write.
func (w *Writer) Close() error {
	if !w.done {
		if err := w.encodeRows(context.Background()); err != nil {
			return err
		}
	}
	if w.fs == nil || w.path == "" {
		return nil
	}
	return afero.WriteFile(w.fs, w.path, w.out, 0644)
}

// Bytes returns the emitted file. Empty until the encode has run.
func (w *Writer) Bytes() []byte { return w.out }

// ---------------------------------------------------------------------------
// The cohort path
// ---------------------------------------------------------------------------

// cohortPass is everything the writer knows about a cohort BEFORE it has read
// a single record: the schema, the emitted dictionary, an encoder bound to
// both, and the open record stream positioned at the first case.
//
// It is a type rather than five return values because it has two consumers
// that must not drift apart — encodeCohort, which goes on to run the data
// pass, and ValidateCohort, which closes it and reports. Splitting the pass
// in two is what lets `pulse export predict` answer with the export's own
// checks instead of a second implementation of them.
type cohortPass struct {
	schema *encoding.Schema
	plan   *DictionaryPlan
	enc    *DataEncoder
	// r is positioned at the first record. Nil is not a valid pass.
	r *bufio.Reader
	// closeFn releases the cohort file. Always non-nil; call it exactly
	// once, through close.
	closeFn func() error
	// renames and warnings are the plan's, lifted out so a consumer can
	// decide whether to record them on the Writer. ValidateCohort does not.
	renames  []NameRename
	warnings []*errors.CodedError
}

func (p *cohortPass) close() {
	if p != nil && p.closeFn != nil {
		_ = p.closeFn()
	}
}

// planCohort runs everything the encode does up to, but not including, the
// first record: the sidecar resolution, the schema read, the dictionary build
// and the encoder's own column checks.
//
// Every refusal reachable from schema and sidecar facts alone lives here —
// a stale or unreadable sidecar, an illegal or colliding SPSS name, a name or
// label the target charset cannot encode, a cohort column the derived
// registry cannot account for, an unwritable compression mode. That is
// precisely the set `pulse export predict` can answer, which is why this is a
// seam rather than an inline prefix of encodeCohort.
//
// The schema comes from the COHORT rather than from SetPulseSchema even
// though both are available, and deliberately: the record stream about to be
// walked is laid out to the schema in the file, so reading it from there
// makes the two agree by construction instead of by two callers staying in
// step.
//
// It mutates nothing on the Writer. A successful call hands back an OPEN
// file; the caller owns closing it through [cohortPass.close].
func (w *Writer) planCohort(ctx context.Context, fs afero.Fs, path string) (*cohortPass, error) {
	if fs == nil {
		return nil, errors.NewCodedErrorWithDetails(errors.DATA_FILE,
			"spss: the .sav writer was given no filesystem to read the cohort from",
			map[string]any{errors.DetailSPSSCohort: path})
	}

	// The sidecar read is the writer's first act, and it is the one that can
	// refuse: a STALE sidecar stops the export outright. See LoadSidecar.
	res, err := LoadSidecar(fs, path, w.opts)
	if err != nil {
		return nil, err
	}

	f, err := fs.Open(path)
	if err != nil {
		return nil, errors.NewCodedErrorWithDetails(errors.DATA_FILE,
			"spss: cannot open the cohort to export: "+err.Error(),
			map[string]any{errors.DetailSPSSCohort: path})
	}
	// Every failure below has to close the file it was handed; only the
	// success path transfers ownership to the returned pass.
	fail := func(err error) (*cohortPass, error) {
		_ = f.Close()
		return nil, err
	}

	r := bufio.NewReader(f)
	if err := encoding.ReadHeader(r); err != nil {
		return fail(err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		return fail(err)
	}
	if err := w.checkAnnouncedSchema(schema, path); err != nil {
		return fail(err)
	}

	// Cases: -1 is the format's "unknown", which is legal and every reader
	// handles. Finish patches the header count once the last case is
	// written, so the emitted file states the real number either way.
	plan, err := BuildDictionary(DictionaryRequest{
		Schema:      schema,
		Sidecar:     res,
		Cases:       -1,
		Compression: w.opts.Compression(),
		Options:     w.opts,
	})
	if err != nil {
		return fail(err)
	}

	enc, err := NewDataEncoder(plan, schema)
	if err != nil {
		return fail(err)
	}
	select {
	case <-ctx.Done():
		return fail(ctx.Err())
	default:
	}

	return &cohortPass{
		schema:   schema,
		plan:     plan,
		enc:      enc,
		r:        r,
		closeFn:  f.Close,
		renames:  plan.Renames,
		warnings: plan.Warnings,
	}, nil
}

// encodeCohort is E5-S1..S5 assembled: run the non-data pass, encode every
// case, concatenate.
func (w *Writer) encodeCohort(ctx context.Context, fs afero.Fs, path string) error {
	pass, err := w.planCohort(ctx, fs, path)
	if err != nil {
		return err
	}
	defer pass.close()

	w.renames = pass.renames
	w.warnings = append(w.warnings, pass.warnings...)

	if err := pass.enc.WriteCohort(pass.r); err != nil {
		return err
	}
	data, err := pass.enc.Finish()
	if err != nil {
		return err
	}

	out := make([]byte, 0, len(pass.plan.Bytes)+len(data))
	out = append(out, pass.plan.Bytes...)
	out = append(out, data...)
	w.out = out
	w.cases = int(pass.enc.Cases())
	w.done = true
	return nil
}

// checkAnnouncedSchema cross-checks the schema SetPulseSchema announced
// against the one the cohort actually carries.
//
// ExportJob.Run reads both from the same file, so the two agree there by
// construction and this never fires. It exists for the direct caller who
// wires the writer by hand and announces one cohort's schema while pointing
// WriteCohort at another: the encoder indexes a case by FIELD INDEX, so a
// mismatch would not fail — it would write the wrong column's value into
// every variable, which is the silent wrong answer with no downstream check.
func (w *Writer) checkAnnouncedSchema(actual *encoding.Schema, path string) error {
	if w.schema == nil || actual == nil {
		return nil
	}
	mismatch := len(w.schema.Fields) != len(actual.Fields)
	if !mismatch {
		for i := range actual.Fields {
			if w.schema.Fields[i].Name != actual.Fields[i].Name ||
				w.schema.Fields[i].Type != actual.Fields[i].Type {
				mismatch = true
				break
			}
		}
	}
	if !mismatch {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.DATA_FILE,
		"spss: the schema handed to the .sav writer through SetPulseSchema does not describe the cohort it was "+
			"asked to export; the encoder resolves a case by field index, so writing anyway would put every "+
			"variable's value in the wrong column",
		map[string]any{errors.DetailSPSSCohort: path})
}

// ---------------------------------------------------------------------------
// The row path
// ---------------------------------------------------------------------------

// encodeRows turns the buffered rows into a cohort and then encodes that.
//
// The intermediate cohort is written to a MemMapFs that lives and dies inside
// this call: nothing lands on the caller's filesystem, and the ordinary
// import path — inference, dictionary building, the null bitmap — is the one
// that builds it, so a converted `.sav` carries the same types a
// `pulse import csv` of the same file would.
//
// A row path with no rows at all is a refusal rather than an empty file. A
// zero-row CSV gives inference nothing to work with, so the schema would be
// invented outright, and a `.sav` full of invented variables is worse than
// no `.sav`.
func (w *Writer) encodeRows(ctx context.Context) error {
	if w.done {
		return nil
	}
	if len(w.rows) == 0 {
		return errors.NewCodedError(errors.PULSE_SPSS_EXPORT_UNSUPPORTED,
			"spss: the .sav writer was given no cohort and no rows. It encodes from a cohort's raw storage — "+
				"dictionary IDs, set masks, the null bitmap — so it needs either a .pulse cohort "+
				"(`pulse export spss -i cohort.pulse -o out.sav`) or at least one row to build one from")
	}

	mem := afero.NewMemMapFs()
	job := pio.NewImportJob(&rowReader{columns: w.columns, rows: w.rows}, rowPathCohort)
	job.FS = mem
	if _, err := job.Run(ctx); err != nil {
		return err
	}
	return w.encodeCohort(ctx, mem, rowPathCohort)
}

// rowReader replays a buffered header + rows as a pio.ResetReader, so the
// row path can reach the ordinary import machinery without inventing a
// second one. It holds the caller's slices rather than copying them: the
// buffer is the Writer's and outlives this reader.
type rowReader struct {
	columns []string
	rows    [][]string
	at      int
}

func (r *rowReader) ReadHeader() ([]string, error) { return r.columns, nil }

func (r *rowReader) ReadRows(ctx context.Context, fn func(row []string) error) error {
	for ; r.at < len(r.rows); r.at++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := fn(r.rows[r.at]); err != nil {
			return err
		}
	}
	return nil
}

func (r *rowReader) Reset() error { r.at = 0; return nil }
func (r *rowReader) Close() error { return nil }

var (
	_ pio.Reader      = (*rowReader)(nil)
	_ pio.ResetReader = (*rowReader)(nil)
)

// ---------------------------------------------------------------------------
// Rendering and refusals
// ---------------------------------------------------------------------------

// formatCell renders one exported cell to the canonical text the import path
// parses back, matching io/csv's own helper. It exists so the row path's
// buffer is text rather than `any`, which is what the intermediate import
// consumes.
func formatCell(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case uint64:
		return strconv.FormatUint(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(t), 'g', -1, 32)
	case encoding.Decimal128:
		// Scale is the field's, not the value's, and the writer does not
		// have it here; String(0) is the unscaled mantissa, which is what
		// a decimal128 cell round-trips as through the import path.
		return t.String(0)
	default:
		var b bytes.Buffer
		b.WriteString(strings.TrimSpace(stringify(t)))
		return b.String()
	}
}

// stringify is the last-resort rendering for a cell type this writer does not
// know. It is deliberately narrow: an unknown type reaching here means the
// export dispatch grew a value shape the `.sav` writer has not been taught,
// and a plausible-looking rendering would hide that.
func stringify(v any) string {
	if s, ok := v.(interface{ String() string }); ok {
		return s.String()
	}
	return ""
}

// cannotProject refuses an output-time row transformation the cohort path
// cannot honour.
//
// It names the flag rather than describing the mechanism, because the caller
// asked for the flag. See the file comment for why silently ignoring it is
// not on the table.
func (w *Writer) cannotProject(flag, what string) error {
	return errors.NewCodedErrorWithDetails(errors.PULSE_SPSS_EXPORT_UNSUPPORTED,
		"spss: "+flag+" is not available on a .sav export ("+what+"). The .sav writer encodes from the "+
			"cohort's raw storage rather than from the rendered row stream those options transform, so honouring "+
			"them would mean silently emitting something other than what was asked for. Project or relabel first — "+
			"`pulse api process` writing a narrowed cohort — then export that.",
		map[string]any{"option": flag})
}
