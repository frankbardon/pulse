package io

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/iocore"
)

// The tabular I/O contracts live in the iocore leaf so the format adapters
// can implement them without importing this package (whose jobs the
// adapters' own tests drive). Every name is an alias: the types are
// identical, so an optional-interface assertion against either spelling
// resolves the same way.
type (
	Reader                = iocore.Reader
	ResetReader           = iocore.ResetReader
	Writer                = iocore.Writer
	DiscardableWriter     = iocore.DiscardableWriter
	SchemaAwareWriter     = iocore.SchemaAwareWriter
	SchemaAwareReader     = iocore.SchemaAwareReader
	NullAwareReader       = iocore.NullAwareReader
	NullAwareWriter       = iocore.NullAwareWriter
	OverlayAwareWriter    = iocore.OverlayAwareWriter
	SourceWarningEmitter  = iocore.SourceWarningEmitter
	SidecarEmitter        = iocore.SidecarEmitter
	CohortSource          = iocore.CohortSource
	CohortWriter          = iocore.CohortWriter
	CohortValidator       = iocore.CohortValidator
	TargetWarningEmitter  = iocore.TargetWarningEmitter
	OverlayWarningEmitter = iocore.OverlayWarningEmitter
	ConvertSource         = iocore.ConvertSource
	SourceAwareWriter     = iocore.SourceAwareWriter
)

// DefaultSetDelimiter is iocore.DefaultSetDelimiter.
const DefaultSetDelimiter = iocore.DefaultSetDelimiter

// EmptySetCell is iocore.EmptySetCell.
const EmptySetCell = iocore.EmptySetCell

// DiscardWriter is iocore.DiscardWriter.
func DiscardWriter(w Writer) error { return iocore.DiscardWriter(w) }

// IsNullCell is iocore.IsNullCell.
func IsNullCell(v any, explicit bool) bool { return iocore.IsNullCell(v, explicit) }

// SetTypeFor is iocore.SetTypeFor.
func SetTypeFor(elements int) (encoding.FieldType, bool) { return iocore.SetTypeFor(elements) }

// WidestSetType is iocore.WidestSetType.
func WidestSetType() encoding.FieldType { return iocore.WidestSetType() }

// MaxSetElements is iocore.MaxSetElements.
func MaxSetElements() int { return iocore.MaxSetElements() }
