// Package imports manages tabular-source imports with a TTL-tracked
// on-disk pool. It is the home of the pulse_import / pulse_drop tool
// semantics: convert a CSV / TSV / NDJSON / JSON-array / Parquet /
// Arrow / Excel file into a .pulse file in $PULSE_DATA_DIR/imports/,
// write a sidecar with an expiry, and provide hooks for sliding-window
// TTL renewal on every subsequent operation that touches the handle.
//
// Pulse-native sources (.pulse files) are short-circuited: they pass
// through with no copy and no sidecar, so the user-curated pool stays
// outside the managed area's sweeper.
package imports

import (
	"time"

	"github.com/frankbardon/pulse/encoding"
)

// DefaultImportsDir is the relative directory inside the Pulse fs where
// managed imports live. Overridable via PULSE_IMPORTS_DIR. The directory
// is created lazily on first import.
const DefaultImportsDir = "imports"

// DefaultTTL is the lifetime applied to a managed import when the
// caller does not pass one explicitly. Sliding window: every subsequent
// operation against the handle bumps the expiry forward by TTL.
const DefaultTTL = 7 * 24 * time.Hour

// SidecarSuffix is appended to a managed .pulse handle's path to form
// the sidecar metadata filename. "data.pulse" + SidecarSuffix =
// "data.pulse.meta.json".
const SidecarSuffix = ".meta.json"

// Spec describes one import request — the input to Manager.Open. Either
// SourcePath is set (the common path: import from a file on the Pulse
// filesystem) or InlineBytes is set (rare: import from an in-memory
// blob, supplying Format explicitly). Mixing both errors.
type Spec struct {
	// SourcePath is the filesystem path of the source file, relative to
	// the Pulse fs root. Required when InlineBytes is unset.
	SourcePath string

	// Format overrides extension-based detection. Use the identifiers
	// from io/format (csv, tsv, ndjson, jsonarray, parquet, arrow,
	// excel, pulse). When empty, FromExt(SourcePath) is used.
	Format string

	// Handle is the desired managed name (without extension). Defaults
	// to the source basename with the original extension stripped.
	// Collisions error unless Overwrite is true.
	Handle string

	// TTL governs how long the import survives in the managed pool.
	// Zero falls back to DefaultTTL. Negative values pin the import
	// (never sweep, never expire).
	TTL time.Duration

	// Sheet is honoured only for Excel sources; ignored otherwise.
	Sheet string

	// Overwrite replaces an existing managed handle of the same name.
	// Defaults to false (collision → PULSE_IMPORT_HANDLE_EXISTS).
	Overwrite bool

	// InlineBytes carries the raw source bytes for in-memory imports.
	// Format must be set explicitly when this is non-nil.
	InlineBytes []byte

	// SetInferenceMinPct configures the import-side delimited-cell
	// heuristic for set_* inference. Zero accepts the default (30).
	// Persisted onto the sidecar so repeated opens use the same value.
	SetInferenceMinPct int

	// ColumnTypeOverrides force-types specific columns (matching
	// header names), bypassing inference for those columns. Values
	// are canonical FieldType strings (e.g. "set_u8",
	// "categorical_u16"). Persisted onto the sidecar.
	ColumnTypeOverrides map[string]string
}

// Result describes the outcome of a managed-import call. Managed=false
// indicates pulse-passthrough (no sidecar, no TTL); the handle's Path
// is the source path verbatim. Otherwise Path points inside the
// managed area and ExpiresAt / Sidecar are populated.
type Result struct {
	Handle       string           `json:"handle"`
	Path         string           `json:"path"`
	Format       string           `json:"format"`
	Managed      bool             `json:"managed"`
	RowsImported int              `json:"rows_imported,omitempty"`
	ImportedAt   time.Time        `json:"imported_at,omitzero"`
	ExpiresAt    *time.Time       `json:"expires_at,omitempty"`
	TTLSeconds   int64            `json:"ttl_seconds,omitempty"`
	Schema       *encoding.Schema `json:"-"`
}

// Sidecar is the JSON payload written next to a managed .pulse file.
// One sidecar per managed handle. Pinned handles set TTLSeconds=0 and
// leave ExpiresAt as the zero time (omitted from JSON via omitempty).
type Sidecar struct {
	Handle       string    `json:"handle"`
	Format       string    `json:"format"`
	SourcePath   string    `json:"source_path"`
	SourceFormat string    `json:"source_format"`
	ImportedAt   time.Time `json:"imported_at"`
	ExpiresAt    time.Time `json:"expires_at,omitzero"`
	TTLSeconds   int64     `json:"ttl_seconds"`
	RowsImported int       `json:"rows_imported"`
	// ColumnTypeOverrides is the force-type escape hatch for
	// inference. Maps column name (matching the source header) to a
	// canonical Pulse FieldType.String() value — e.g. "set_u8",
	// "categorical_u16", "f64". When non-empty, the importer skips
	// inference for the named columns and uses the override for the
	// column type (dictionary is still built from observed values
	// during the row pass). Empty / nil means no overrides.
	ColumnTypeOverrides map[string]string `json:"column_type_overrides,omitempty"`
}

// Entry is one row of Manager.List output — the snapshot returned to
// the CLI's `imports list` leaf and the MCP introspection paths.
type Entry struct {
	Handle    string    `json:"handle"`
	Path      string    `json:"path"`
	Sidecar   Sidecar   `json:"sidecar"`
	ExpiresIn string    `json:"expires_in,omitempty"`
	Pinned    bool      `json:"pinned"`
	Expired   bool      `json:"expired"`
	Now       time.Time `json:"-"`
}

// Pinned reports whether the sidecar represents an expiry-free import.
// Pinned imports survive sweeps. TTLSeconds <= 0 is the canonical
// signal; ExpiresAt is also zero in that case.
func (s Sidecar) Pinned() bool { return s.TTLSeconds <= 0 }
