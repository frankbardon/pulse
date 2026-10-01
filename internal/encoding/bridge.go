package encoding

import (
	"fmt"
	"io"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/encodingbridge"
)

// Typed wrappers over the public package's unexported helpers, reached
// through internal/encodingbridge (installed by the public package's
// init). They keep one definition of each helper on the public side.

func writeHeaderVersion(w io.Writer, v byte) error {
	return encodingbridge.WriteHeaderVersion(w, v)
}

func writeSchemaVersion(w io.Writer, s *encoding.Schema, v byte) error {
	return encodingbridge.WriteSchemaVersion(w, s, v)
}

func validateGroupShape(s *encoding.Schema) error { return encodingbridge.ValidateGroupShape(s) }

func memberSets(s *encoding.Schema) (groupOf, slotOf []int) { return encodingbridge.MemberSets(s) }

func groupEntryGeometry(s *encoding.Schema, g int) (width, bmOff int) {
	return encodingbridge.GroupEntryGeometry(s, g)
}

func groupDescriptorBytes(members int) int { return encodingbridge.GroupDescriptorBytes(members) }

// groupErr is the coded error every parent-group fault raises (twin of
// the public package's helper of the same name).
func groupErr(msg string, details map[string]any) error {
	return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID, "parent group: "+msg, details)
}

// checkSetMaskFits rejects a mask carrying a bit the declared rung cannot
// hold (twin of the public package's helper of the same name).
func checkSetMaskFits(ft encoding.FieldType, m encoding.SetMask) error {
	if m.FitsFieldType(ft) {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.ENCODING_INVALID,
		fmt.Sprintf("set mask has bit %d beyond the capacity of %s", m.HighestBit(), ft),
		map[string]any{
			"type":         ft.String(),
			"capacity":     ft.MaxSetEntries(),
			"highest_bit":  m.HighestBit(),
			"population_n": m.PopCount(),
		})
}
