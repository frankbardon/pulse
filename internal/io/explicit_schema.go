package io

import (
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// This file holds the schema-level checks of an explicit (caller-
// authored) schema that ImportJob and the cohort builder share.
// ImportJob runs checkSchemaDescriptions on a user-supplied
// ImportJob.Schema before the row pass; the builder runs it too, plus
// its own structural and quality checks (build_schema.go).

// checkSchemaDescriptions refuses a field description past
// encoding.MaxDescriptionBytes with PULSE_IMPORT_DESCRIPTION_TOO_LONG —
// the code the schema writer raises — before any row is read, so a long
// description fails fast instead of after a whole pass.
func checkSchemaDescriptions(schema *encoding.Schema) error {
	for i := range schema.Fields {
		f := &schema.Fields[i]
		if n := len(f.Description); n > encoding.MaxDescriptionBytes {
			return errors.NewCodedErrorWithDetails(
				errors.PULSE_IMPORT_DESCRIPTION_TOO_LONG,
				fmt.Sprintf("field %q description is %d bytes, max %d", f.Name, n, encoding.MaxDescriptionBytes),
				map[string]any{"field": f.Name, "length": n, "max": encoding.MaxDescriptionBytes},
			)
		}
	}
	return nil
}
