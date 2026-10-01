package encoding

import (
	"io"

	"github.com/frankbardon/pulse/internal/encodingbridge"
)

// init hands internal/encoding the unexported helpers it shares with
// this package (see package encodingbridge).
func init() {
	encodingbridge.WriteHeaderVersion = writeHeaderVersion
	encodingbridge.WriteSchemaVersion = func(w io.Writer, s any, v byte) error {
		return writeSchemaVersion(w, s.(*Schema), v)
	}
	encodingbridge.ValidateGroupShape = func(s any) error { return s.(*Schema).validateGroupShape() }
	encodingbridge.MemberSets = func(s any) ([]int, []int) { return s.(*Schema).memberSets() }
	encodingbridge.GroupEntryGeometry = func(s any, g int) (int, int) { return s.(*Schema).groupEntryGeometry(g) }
	encodingbridge.GroupDescriptorBytes = groupDescriptorBytes
}
