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

// ReadSchemaLayout reads a schema block at version v without its
// dictionaries or group entry tables (nil Field.Dictionary and
// Group.Entries) — enough for the record stride, at a fraction of a
// full decode's allocation. r is left at the first record byte. Used
// wherever a shard's own schema block is only read past: the
// record-count peek (Archive.PeekShardRecordCount) and the archive scan
// (internal/service shardIter.openShard / processOneShard), which
// decodes every row through the canonical schema, the lookup locator
// (openRecordLocator), the shard-admin count (recordCountFromBytes) and
// the shard-archive filter (skipShardPreamble).
func ReadSchemaLayout(r io.Reader, v byte) (*encoding.Schema, error) {
	s, err := encodingbridge.ReadSchemaLayout(r, v)
	if err != nil {
		return nil, err
	}
	return s.(*encoding.Schema), nil
}

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
