package cli

import (
	"github.com/frankbardon/pulse/encoding"
	iio "github.com/frankbardon/pulse/internal/io"
)

// parseFieldType converts the type name a schema file declares for field
// to an encoding.FieldType. Delegates to iio.ParseSchemaFieldType so the
// canonical type-name table lives in one place. An unknown name is
// refused with PULSE_IMPORT_SCHEMA_TYPE_UNKNOWN — never coerced to f64.
func parseFieldType(field, name string) (encoding.FieldType, error) {
	return iio.ParseSchemaFieldType(field, name)
}
