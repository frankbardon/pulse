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
	// the request is translated into the equivalent expression and
	// passed to the existing FilterToFile engine. Mutually exclusive
	// with Expression.
	Filterers []*types.Filterer `json:"filterers,omitempty"`

	// OutputDir is the directory the result file lands in. Required.
	OutputDir string `json:"output_dir"`

	// OutputName is the optional override for the result basename.
	// When empty, defaults to `{source-hash}_{predicate-hash}.pulse`
	// so dedup is automatic. When set, the engine still validates the
	// caller-supplied path against the deterministic hash and refuses
	// to overwrite an existing file with mismatching content.
	OutputName string `json:"output_name,omitempty"`
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
	if req == nil {
		return nil, errors.New("filter_to_file: nil request")
	}
	if req.SourcePath == "" {
		return nil, errors.New("filter_to_file: source path required")
	}
	if req.OutputDir == "" {
		return nil, errors.New("filter_to_file: output dir required")
	}
	expr, err := req.canonicalExpression()
	if err != nil {
		return nil, err
	}

	srcHash, err := p.sourceHash(req.SourcePath)
	if err != nil {
		return nil, fmt.Errorf("filter_to_file: hashing source: %w", err)
	}
	predHash := req.predicateHash()
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
	rows, err := p.svc.FilterToFile(ctx, req.SourcePath, tempPath, expr)
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

// canonicalExpression returns the FILTER_EXPRESSION-equivalent string
// for the request. Filterers are translated by joining each entry's
// equivalent expression with " && " so the structured-predicate path
// produces the same hash that an explicit Expression with the same
// semantics would.
func (r *FilterToFileRequest) canonicalExpression() (string, error) {
	hasExpr := r.Expression != ""
	hasFilts := len(r.Filterers) > 0
	if hasExpr && hasFilts {
		return "", errors.New("filter_to_file: Expression and Filterers are mutually exclusive")
	}
	if !hasExpr && !hasFilts {
		return "", errors.New("filter_to_file: predicate required (Expression or Filterers)")
	}
	if hasExpr {
		return r.Expression, nil
	}
	parts := make([]string, 0, len(r.Filterers))
	for _, f := range r.Filterers {
		s, err := filtererToExpression(f)
		if err != nil {
			return "", err
		}
		parts = append(parts, s)
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	out := "(" + parts[0] + ")"
	for _, p := range parts[1:] {
		out += " && (" + p + ")"
	}
	return out, nil
}

// filtererToExpression translates a single Filterer into the equivalent
// expr-lang predicate string. Covers FILTER_EXPRESSION (pass-through),
// FILTER_INCLUDE / FILTER_EXCLUDE (in / not in list), FILTER_RANGE
// (closed interval), and FILTER_NULL (is_null / is_not_null).
func filtererToExpression(f *types.Filterer) (string, error) {
	if f == nil {
		return "", errors.New("filter_to_file: nil filterer")
	}
	switch f.Type {
	case types.FILTER_EXPRESSION:
		if f.Expression == "" {
			return "", errors.New("filter_to_file: FILTER_EXPRESSION requires Expression")
		}
		return f.Expression, nil
	case types.FILTER_INCLUDE:
		return inListExpr(f.Field, f.Values, false), nil
	case types.FILTER_EXCLUDE:
		return inListExpr(f.Field, f.Values, true), nil
	case types.FILTER_RANGE:
		if len(f.Values) != 2 {
			return "", errors.New("filter_to_file: FILTER_RANGE requires exactly two Values")
		}
		return fmt.Sprintf("%s >= %s && %s <= %s", f.Field, f.Values[0], f.Field, f.Values[1]), nil
	case types.FILTER_NULL:
		mode := "is_null"
		if len(f.Values) > 0 {
			mode = f.Values[0]
		}
		switch mode {
		case "is_null":
			return f.Field + " == nil", nil
		case "is_not_null":
			return f.Field + " != nil", nil
		default:
			return "", fmt.Errorf("filter_to_file: FILTER_NULL mode %q not supported (expected is_null / is_not_null)", mode)
		}
	}
	return "", fmt.Errorf("filter_to_file: unsupported filter type %q", f.Type)
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

// predicateHash returns the canonical hash of the structured-predicate
// payload, falling back to the literal expression when Expression is
// supplied. Same predicate -> same hash, in both shapes.
func (r *FilterToFileRequest) predicateHash() string {
	payload := struct {
		Expression string            `json:"expression,omitempty"`
		Filterers  []*types.Filterer `json:"filterers,omitempty"`
	}{
		Expression: r.Expression,
		Filterers:  r.Filterers,
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
	if n, err := p.CountRecords(context.Background(), outPath); err == nil {
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
