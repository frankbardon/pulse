package encoding

// WidenReport describes a completed widen. It is the value both
// callers of the engine (shard auto-widen and the `pulse widen` leaf)
// report from, so it carries the strides as well as the identities: the
// stride delta is the only observable difference between a widen that
// re-laid-out every record and one that did nothing.
type WidenReport struct {
	// Field is the name of the widened set field.
	Field string
	// From is the set rung the field carried before the rewrite.
	From FieldType
	// To is the set rung it carries after.
	To FieldType
	// Records is the number of records re-laid-out.
	Records int64
	// StrideBefore is the source record stride in bytes, bitmap included.
	StrideBefore int
	// StrideAfter is the rewritten record stride in bytes, bitmap included.
	StrideAfter int
}
