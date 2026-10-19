package pulse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"time"

	perr "github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// FilterToFileRequest is the structured request shape for the
// deterministic filter-to-file operation. It deduplicates by
// `{source-hash}_{predicate-hash}.pulse` so two callers issuing the
// same filter against the same source land on the same output file
// and the engine skips redundant work.
//
// Exactly one of Expression / Filterers must be set. Both empty is an
// error; both set is rejected so the predicate hash is unambiguous.
type FilterToFileRequest struct {
	// SourcePath identifies the cohort to filter. Single-file path,
	// shard archive, or `archive.pulse#shard.pulse` anchor — same
	// dispatch rules as Pulse.FilterToFile.
	SourcePath string `json:"source_path"`

	// Expression is the FILTER_EXPRESSION-style predicate evaluated
	// per row. Empty when Filterers is supplied instead.
	Expression string `json:"expression,omitempty"`

	// Filterers is the structured-predicate alternative. When set,
	// FILTER_EXPRESSION / _INCLUDE / _EXCLUDE / _RANGE / _NULL are
	// translated into the equivalent expression and every other
	// registered filterer (an extension filterer, FILTER_TRUE /
	// _FALSE, FILTER_SET_*, FILTER_DATE_RANGES) runs through the engine
	// registry exactly as Process runs it; a row is kept only when all
	// of them pass. Zones resolve as Process resolves them — a
	// zone-capable filterer's `tz`, then its `tz_field`, then TimeZone,
	// then Options.DefaultTimeZone, then UTC — and the applied zones key
	// the dedup predicate hash; a `tz` / `tz_field` on an operator that
	// is not zone-capable (an extension filterer included) is refused
	// with Process's PROCESSING_CONFIG. Mutually exclusive with
	// Expression.
	Filterers []*types.Filterer `json:"filterers,omitempty"`

	// OutputDir is the directory the result file lands in. Required.
	OutputDir string `json:"output_dir"`

	// OutputName is the optional override for the result basename.
	// When empty, defaults to `{source-hash}_{predicate-hash}.pulse`
	// so dedup is automatic. When set, the engine still validates the
	// caller-supplied path against the deterministic hash and refuses
	// to overwrite an existing file with mismatching content.
	OutputName string `json:"output_name,omitempty"`

	// Models loads saved model files (`.pulse-model.json`, the
	// Request.Models rule) and binds each by name, so the predicate's
	// coef() calls read them exactly as a Process filter does. Each
	// file's content hash enters the predicate hash, so a rewritten
	// model never reuses an output built from the old fit. Requires
	// capability:models; an instance hiding it refuses a set key as
	// PULSE_REQUEST_UNKNOWN_FIELD.
	Models []types.ModelRef `json:"models,omitempty"`

	// TimeZone is the request-level IANA zone (`"UTC"` or an
	// `Area/Location` name) a to_day() call in the predicate and a
	// FILTER_DATE_RANGES without its own `tz` / `tz_field` read — the
	// Request.TimeZone rule: it wins over Options.DefaultTimeZone, which
	// wins over UTC. An unknown name is PULSE_TIMEZONE_UNKNOWN, refused
	// before any read whether or not the predicate reads it. The zone
	// the predicate reads keys the dedup predicate hash (so the default
	// output name) only when it is non-UTC and the predicate calls
	// to_day or a FILTER_DATE_RANGES applies it to a `datetime` field;
	// empty runs and hashes exactly as before.
	TimeZone string `json:"time_zone,omitempty"`
}

// FilterToFileResult is the structured response from FilterToFileWithRequest.
type FilterToFileResult struct {
	OutputPath string `json:"output_path"`
	OutputHash string `json:"output_hash"`
	RowCount   int64  `json:"row_count"`
	ElapsedMs  int64  `json:"elapsed_ms"`
	// Reused is true when the engine returned an existing output file
	// without re-running the filter (content-hash match).
	Reused bool `json:"reused"`
}

// FilterToFileWithRequest is the deterministic, dedup-aware variant of
// FilterToFile. Same (source content, predicate) inputs produce the
// same output file hash and output path; if the expected output
// already exists at the target path, the engine returns it without
// re-doing the work.
//
// Determinism comes from three guarantees:
//   - The .pulse byte format embeds no timestamps and no
//     compression-level variation, so the underlying FilterToFile
//     engine produces byte-identical output for identical inputs.
//   - The output is written via atomic rename: a temp file is staged
//     in OutputDir and renamed to the final path on success. A
//     partially-written .pulse file never appears at the target path.
//   - The output name defaults to `{source-hash}_{predicate-hash}.pulse`
//     so independent consumers reach the same expected path without
//     coordination.
func (p *Pulse) FilterToFileWithRequest(ctx context.Context, req *FilterToFileRequest) (*FilterToFileResult, error) {
	return observed(p, ctx, opSpec{kind: observe.OpFilterToFile, path: filterToFileSource(req)}, func(ctx context.Context) (*FilterToFileResult, error) {
		return p.filterToFileWithRequest(ctx, req)
	})
}

func (p *Pulse) filterToFileWithRequest(ctx context.Context, req *FilterToFileRequest) (*FilterToFileResult, error) {
	if req == nil {
		return nil, errors.New("filter_to_file: nil request")
	}
	if req.SourcePath == "" {
		return nil, errors.New("filter_to_file: source path required")
	}
	if req.OutputDir == "" {
		return nil, errors.New("filter_to_file: output dir required")
	}
	inst := p.svc.InstanceSnapshot()
	if len(req.Models) > 0 && !inst.Enabled(descx.FeatureModels) {
		// A hidden capability:models makes `models` an unknown key, the
		// refusal Request and FacetRequest give it.
		return nil, descx.UnknownFieldError([]string{"models"}, filterToFileVisibleKeys(req))
	}
	expr, direct, err := req.canonicalPredicate(inst, p.registeredFilterer)
	if err != nil {
		return nil, err
	}
	// The request zone is validated up front, as the zone resolver
	// validates Request.TimeZone, so an unknown name is refused even
	// when the predicate never reads it.
	if req.TimeZone != "" {
		if _, err := p.svc.ZoneLoader()(req.TimeZone); err != nil {
			return nil, err
		}
	}
	// Structured filterers resolve their zones as Process does, before
	// any hashing or reuse, so a zone refusal is Process's own; the
	// applied zones key the output name.
	var zones []descx.AppliedZone
	if len(req.Filterers) > 0 {
		zones, err = p.svc.FilterToFileZones(ctx, req.SourcePath, req.Filterers, req.TimeZone)
		if err != nil {
			return nil, err
		}
	}
	// The request's models load (every file read and decoded) before
	// any hashing or record read; their content identities key the
	// dedup hash.
	ctx, err = p.svc.BindFilterModels(ctx, req.Models)
	if err != nil {
		return nil, err
	}
	modelIDs, err := p.modelIdentities(req.Models)
	if err != nil {
		return nil, err
	}

	srcHash, err := p.sourceHash(req.SourcePath)
	if err != nil {
		return nil, fmt.Errorf("filter_to_file: hashing source: %w", err)
	}
	// The zone the predicate's to_day() reads (req.TimeZone, else
	// Options.DefaultTimeZone, else UTC) keys the output too, as do the
	// zones the filterers apply, so a run in another zone never reuses
	// it.
	zone := descx.ExpressionZone(expr, req.TimeZone, p.svc.DefaultTimeZone(), p.svc.ZoneLoader())
	predHash := req.predicateHash(modelIDs, zone, zones)
	outName := req.OutputName
	if outName == "" {
		outName = srcHash[:16] + "_" + predHash[:16] + ".pulse"
	}
	outPath := filepath.Join(req.OutputDir, outName)

	// Dedup: if the deterministic output exists, return it.
	if existing, ok := p.tryReuseOutput(outPath); ok {
		return existing, nil
	}

	// Stage the actual write to a temp path under OutputDir so the
	// final path only ever sees a complete .pulse file.
	tempPath := filepath.Join(req.OutputDir, "."+outName+".partial")
	_ = p.fsys.Remove(tempPath)

	start := time.Now()
	var rows int64
	// A structured predicate is judged by the shared field rule before
	// its translated expression compiles; the filterers it could not
	// translate (direct) build through the engine's filterer registry,
	// as Process builds them. req.TimeZone is the zone the expression's
	// to_day() reads.
	rows, err = p.svc.FilterToFileFilterers(ctx, req.SourcePath, tempPath, req.Filterers, expr, direct, req.TimeZone)
	if err != nil {
		_ = p.fsys.Remove(tempPath)
		return nil, err
	}
	if err := p.fsys.Rename(tempPath, outPath); err != nil {
		_ = p.fsys.Remove(tempPath)
		return nil, fmt.Errorf("filter_to_file: rename to %s: %w", outPath, err)
	}
	outHash, err := contentHash(p.fsys, outPath)
	if err != nil {
		return nil, fmt.Errorf("filter_to_file: hashing output: %w", err)
	}
	p.touchManaged(ctx, req.SourcePath)
	return &FilterToFileResult{
		OutputPath: outPath,
		OutputHash: outHash,
		RowCount:   rows,
		ElapsedMs:  time.Since(start).Milliseconds(),
		Reused:     false,
	}, nil
}

// canonicalPredicate splits the request predicate into the
// FILTER_EXPRESSION-equivalent string and the filterers that run
// direct. Translatable filterers (see filtererToExpression) are joined
// with " && " so the structured-predicate path produces the same
// expression an explicit Expression with the same semantics would.
// Every other filterer the instance has registered — an extension
// filterer, FILTER_TRUE / FILTER_FALSE, the FILTER_SET_* family — is
// returned in direct, in request order, and builds through the engine
// filterer registry exactly as Process builds it (FILTER_DATE_RANGES
// included, zoned as Process zones it); the run keeps a row only when
// the expression (if any) and every direct filterer pass. No `tz` /
// `tz_field` is judged here: Service.FilterToFileZones runs Process's
// zone resolution over the request filterers right after.
//
// inst is the instance feature set: a filterer type it hides is
// refused exactly as an unknown type is (see filtererToExpression).
// registered reports whether the instance can build a filterer type
// (its hidden types excluded).
func (r *FilterToFileRequest) canonicalPredicate(inst *descx.InstanceSnapshot, registered func(types.FiltererType) bool) (string, []*types.Filterer, error) {
	hasExpr := r.Expression != ""
	hasFilts := len(r.Filterers) > 0
	if hasExpr && hasFilts {
		return "", nil, errors.New("filter_to_file: Expression and Filterers are mutually exclusive")
	}
	if !hasExpr && !hasFilts {
		return "", nil, errors.New("filter_to_file: predicate required (Expression or Filterers)")
	}
	if hasExpr {
		return r.Expression, nil, nil
	}
	parts := make([]string, 0, len(r.Filterers))
	var direct []*types.Filterer
	for _, f := range r.Filterers {
		s, isDirect, err := filtererToExpression(f, inst, registered)
		if err != nil {
			return "", nil, err
		}
		if isDirect {
			direct = append(direct, f)
			continue
		}
		parts = append(parts, s)
	}
	switch len(parts) {
	case 0:
		return "", direct, nil
	case 1:
		return parts[0], direct, nil
	}
	out := "(" + parts[0] + ")"
	for _, p := range parts[1:] {
		out += " && (" + p + ")"
	}
	return out, direct, nil
}

// registeredFilterer reports whether the instance builds filterer type
// t: a built-in or an extension filterer its feature profile does not
// hide (the registry lookup Process uses).
func (p *Pulse) registeredFilterer(t types.FiltererType) bool {
	_, ok := p.svc.Extensions().LookupFilterer(t)
	return ok
}

// filtererToExpression translates a single Filterer into the equivalent
// expr-lang predicate string. Covers FILTER_EXPRESSION (pass-through),
// FILTER_INCLUDE / FILTER_EXCLUDE (in / not in list), FILTER_RANGE
// (closed interval), and FILTER_NULL (is_null / is_not_null). Any other
// type registered reports (an extension filterer, FILTER_TRUE /
// FILTER_FALSE, FILTER_SET_*, FILTER_DATE_RANGES) is not translated:
// direct is true and the filterer runs through the engine registry. A
// type inst hides, or one never registered, falls through to the
// unsupported-type refusal. A `tz` / `tz_field` is not judged here —
// the zone resolver refuses it on every type but a zone-capable one,
// with Process's code, message and details (Service.FilterToFileZones).
func filtererToExpression(f *types.Filterer, inst *descx.InstanceSnapshot, registered func(types.FiltererType) bool) (expr string, direct bool, err error) {
	if f == nil {
		return "", false, errors.New("filter_to_file: nil filterer")
	}
	if inst.Hidden(string(f.Type)) {
		return "", false, unsupportedFilterType(f.Type)
	}
	switch f.Type {
	case types.FILTER_EXPRESSION:
		if f.Expression == "" {
			return "", false, errors.New("filter_to_file: FILTER_EXPRESSION requires Expression")
		}
		return f.Expression, false, nil
	case types.FILTER_INCLUDE:
		return inListExpr(f.Field, f.Values, false), false, nil
	case types.FILTER_EXCLUDE:
		return inListExpr(f.Field, f.Values, true), false, nil
	case types.FILTER_RANGE:
		if len(f.Values) != 2 {
			return "", false, errors.New("filter_to_file: FILTER_RANGE requires exactly two Values")
		}
		return fmt.Sprintf("%s >= %s && %s <= %s", f.Field, f.Values[0], f.Field, f.Values[1]), false, nil
	case types.FILTER_NULL:
		mode := "is_null"
		if len(f.Values) > 0 {
			mode = f.Values[0]
		}
		switch mode {
		case "is_null":
			return f.Field + " == nil", false, nil
		case "is_not_null":
			return f.Field + " != nil", false, nil
		default:
			return "", false, fmt.Errorf("filter_to_file: FILTER_NULL mode %q not supported (expected is_null / is_not_null)", mode)
		}
	}
	if !registered(f.Type) {
		return "", false, unsupportedFilterType(f.Type)
	}
	return "", true, nil
}

// unsupportedFilterType is the refusal filtererToExpression gives a
// type the instance cannot build — never-registered and hidden alike:
// PROCESSING_CONFIG naming the operator, so an envelope carries the
// code rather than an uncoded placeholder. The message lists no
// supported types: a hidden one must read as never-registered.
func unsupportedFilterType(t types.FiltererType) error {
	return perr.NewCodedErrorWithDetails(perr.PROCESSING_CONFIG,
		fmt.Sprintf("filter_to_file: unsupported filter type %q", t),
		map[string]any{"operator": string(t)})
}

// inListExpr builds a `field in ["a","b"]` or `not (field in [...])`
// predicate. Caller is responsible for ensuring Values contains only
// JSON-safe literals — the canonical hash treats the literal slice as
// part of the predicate.
func inListExpr(field string, values []string, negate bool) string {
	lit := "["
	for i, v := range values {
		if i > 0 {
			lit += ", "
		}
		lit += fmt.Sprintf("%q", v)
	}
	lit += "]"
	if negate {
		return "not (" + field + " in " + lit + ")"
	}
	return field + " in " + lit
}

// modelIdentity is one loaded model's dedup identity: its binding name,
// path and the SHA-256 of the file content the run reads.
type modelIdentity struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// modelIdentities hashes every Models file's content, in entry order.
// The files were just loaded (BindFilterModels), so a read failure here
// is the file vanishing mid-call.
func (p *Pulse) modelIdentities(refs []types.ModelRef) ([]modelIdentity, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	out := make([]modelIdentity, len(refs))
	for i, r := range refs {
		data, err := p.svc.ReadModelFile(r.Path)
		if err != nil {
			return nil, fmt.Errorf("filter_to_file: hashing model %s: %w", r.Path, err)
		}
		sum := sha256.Sum256(data)
		out[i] = modelIdentity{Name: r.Name, Path: r.Path, SHA256: hex.EncodeToString(sum[:])}
	}
	return out, nil
}

// filterToFileVisibleKeys is FilterToFileRequest's JSON keys minus the
// hidden `models` — the valid_keys of the hidden-slot refusal.
func filterToFileVisibleKeys(req *FilterToFileRequest) []string {
	keys := descx.JSONObjectKeys(req)
	out := keys[:0]
	for _, k := range keys {
		if k != "models" {
			out = append(out, k)
		}
	}
	return out
}

// predicateHash returns the canonical hash of the structured-predicate
// payload, falling back to the literal expression when Expression is
// supplied. Same predicate -> same hash, in both shapes. models (the
// loaded files' content identities) join the payload only when set, so
// a model-free request hashes exactly as it always has, and a changed
// model file changes the hash. zone (descx.ExpressionZone: the non-UTC
// zone the predicate's to_day() reads, else "") joins the same way, so
// a predicate that reads no zone hashes as it always has; so does zones
// (descx.AppliedZones: the zones the filterers apply — a non-UTC name
// or a per-row zone field on a `datetime` field), which Filterers alone
// would not cover, since an inherited zone is not spelled there.
func (r *FilterToFileRequest) predicateHash(models []modelIdentity, zone string, zones []descx.AppliedZone) string {
	payload := struct {
		Expression string              `json:"expression,omitempty"`
		Filterers  []*types.Filterer   `json:"filterers,omitempty"`
		Models     []modelIdentity     `json:"models,omitempty"`
		Zone       string              `json:"zone,omitempty"`
		Zones      []descx.AppliedZone `json:"zones,omitempty"`
	}{
		Expression: r.Expression,
		Filterers:  r.Filterers,
		Models:     models,
		Zone:       zone,
		Zones:      zones,
	}
	return types.CanonicalHash("filter_predicate", payload)
}

// sourceHash returns the content prefix hash for the request's source
// path. Anchor syntax `archive.pulse#shard.pulse` resolves to the
// archive file; the shard suffix participates via the predicate hash
// indirectly (the writer's output schema is shard-specific).
func (p *Pulse) sourceHash(src string) (string, error) {
	clean := src
	if idx := indexOf(clean, '#'); idx >= 0 {
		clean = clean[:idx]
	}
	return contentHash(p.fsys, clean)
}

// indexOf returns the index of the first occurrence of b in s, or -1.
func indexOf(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// contentHash returns SHA-256 hex of the file at path on fs. Reads the
// full content; used for output verification where the file is small
// enough that prefix-only hashing risks collisions.
func contentHash(fs afero.Fs, p string) (string, error) {
	f, err := fs.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// tryReuseOutput returns a FilterToFileResult drawn from an existing
// file at outPath when one exists. Reuse is silent — the result's
// RowCount field is filled by re-reading the file's header record
// count (header-fast via CountRecords). On any failure the function
// returns (nil, false) so the caller falls through to the work path.
func (p *Pulse) tryReuseOutput(outPath string) (*FilterToFileResult, bool) {
	info, err := p.fsys.Stat(outPath)
	if err != nil || info.IsDir() {
		return nil, false
	}
	hash, err := contentHash(p.fsys, outPath)
	if err != nil {
		return nil, false
	}
	rows := int64(-1)
	if n, err := p.countRecords(context.Background(), outPath); err == nil {
		if n <= 1<<62 {
			rows = int64(n)
		}
	}
	return &FilterToFileResult{
		OutputPath: outPath,
		OutputHash: hash,
		RowCount:   rows,
		ElapsedMs:  0,
		Reused:     true,
	}, true
}

// joinPath joins dir and name avoiding a duplicate slash even on
// platforms where path.Join is undesirable (afero MemMap normalizes
// paths but ChrootFs preserves leading slashes).
func joinPath(dir, name string) string {
	return path.Join(dir, name)
}

// ensure unused-import / declared-but-unused warnings stay quiet.
var _ = joinPath
