package io

import (
	"github.com/frankbardon/pulse/encoding"
)

// ConvertSource is what a convert knows about its SOURCE that the rendered
// row stream between the two halves cannot carry.
//
// # Why it exists
//
// ConvertJob is import-half then export-half with no cohort in between: the
// source yields `[]string` rows and the target consumes them. That is
// sufficient for a text target, and insufficient for a target that has to
// rebuild a cohort in order to emit at all — io/spss's `.sav` writer is the
// only one today (see its pio.CohortWriter row path). Such a target
// re-derives a schema by INFERRING it from the very text the source just
// rendered, and inference cannot recover what the source DECLARED:
//
//   - A multiple-dichotomy `set_*` column round-trips its selections as
//     "Q1A|Q1C" tokens, and a column whose observed tokens happen to be few
//     re-infers as a narrow rung — or, with few enough distinct cells, as a
//     categorical. The selections survive; the declared width does not, and
//     with it goes the dictionary entry per UNSELECTED member.
//   - The SPSS metadata sidecar is the only place the code / label /
//     dictionary-ID triple lives (.claude/reference/byte-layout.md). A
//     rebuilt intermediate cohort has none, so a derived column cannot be
//     recognised as derived, and the `.sav` writer's synthesise-a-default
//     path re-emits a multiple-dichotomy set as member variables that
//     collide by name with the constituents already in the cohort —
//     PULSE_SPSS_NAME_COLLISION for the wide case, and a bogus extra
//     variable for the narrow one.
//
// Both are the same missing channel, so both ride the same struct.
//
// # What a recipient may assume
//
// ConvertJob.Run populates it ONLY when the row stream it is about to emit
// is faithful to Schema — one cell per schema field, in field order, with
// no projection (ConvertJob.Includes) and no label augmentation or
// replacement rewriting the cells. Where it is not, nothing is carried and
// the target takes the unchanged inference path. So a recipient may rely on
// Schema.Fields[i] describing emitted column i, and on
// Schema.Fields[i].CsvColumnIdx being i.
type ConvertSource struct {
	// Schema is the source's DECLARED schema — a pio.SchemaAwareReader's
	// own dictionary, or the caller's explicit ConvertJob.Schema — never
	// an inferred one. Nil when the source declared nothing, which is the
	// genuinely schema-less case where inference is the honest answer and
	// remains the fallback.
	//
	// It is a copy: the fields are a fresh slice with CsvColumnIdx
	// re-based onto the emitted row. The encoding.Dictionary pointers are
	// SHARED, deliberately — dictionary entry order is the on-wire
	// encoding (position i is the categorical ID and the set_* mask bit),
	// and handing over the source's own instance is what makes a rebuilt
	// cohort agree with the source on every ID.
	Schema *encoding.Schema

	// Sidecar is the source Reader's own [SidecarEmitter], or nil when the
	// source carries no format-native metadata sidecar. A recipient that
	// rebuilds a cohort calls WriteSidecar against THAT cohort, which both
	// re-fingerprints the document and places it where the recipient's own
	// read path looks for it.
	Sidecar SidecarEmitter
}

// SourceAwareWriter is an optional extension of Writer for convert targets
// that rebuild a `.pulse` cohort from the row stream instead of writing the
// rows out directly.
//
// It is the convert-time peer of [SchemaAwareWriter], and deliberately NOT
// that interface: SetPulseSchema means "here is the cohort you are
// exporting from" and is answered by the typed-column adapters (Arrow,
// Parquet, Excel) by changing what they emit. This one means "here is what
// your source declared, for the cohort you are about to rebuild", is
// answered only by a target that rebuilds one, and changes nothing for a
// target that does not implement it — ConvertJob type-asserts it and does
// nothing at all when the assertion fails, so every other adapter's convert
// output is byte-identical to the pre-interface shape.
//
// ConvertJob.Run calls SetConvertSource BEFORE WriteHeader, matching the
// ordering every other push-shaped optional interface uses, so a recipient
// may consult it from the first buffered row onwards.
type SourceAwareWriter interface {
	Writer
	// SetConvertSource hands over the source facts. A recipient retains
	// the value; ConvertJob does not mutate it afterwards.
	SetConvertSource(src ConvertSource)
}
