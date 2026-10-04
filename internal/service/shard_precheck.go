package service

import (
	"bytes"
	"context"
	"fmt"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// PrecheckShardCohesion answers, before a single row exists, whether a
// shard laid out by schema could be appended to the archive at
// archivePath — the up-front check an anchored CohortBuilder runs at
// construction so a schema AddShard is certain to refuse fails there,
// not after every row has been spooled.
//
// It runs AddShard's own merge over a ZERO-ROW shard of schema: the
// same set-rung plan before strict structural cohesion (reconcileFlat),
// then the same dictionary union (encx.MergeDictUnion), on the LOGICAL
// schemas when either side is grouped, exactly as mergeShard and
// mergeGroupedShard do. A refusal therefore carries AddShard's code,
// message and details unchanged (PULSE_SHARD_SCHEMA_MISMATCH,
// PULSE_SHARD_DICT_WIDTH_OVERFLOW, …).
//
// It never refuses what AddShard would accept: set widening, a
// dictionary union and a group re-layout are all legal here, and every
// verdict it can reach is monotone in the rows still to come — a
// builder's dictionaries only grow from their pre-seeded entries, so a
// union that already overflows or diverges stays that way. What only
// rows decide (the group re-encode, a key violation, an overflow the
// appended labels cause) is left to AddShard at Close, which stays the
// authority: the archive may change between the two. Nothing is
// written.
func (s *Service) PrecheckShardCohesion(ctx context.Context, archivePath string, schema *encoding.Schema) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	archiveBytes, err := afero.ReadFile(s.fs.Fs(), archivePath)
	if err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("AddShard: reading archive %s", archivePath))
	}
	arch, err := encx.OpenArchive(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		return err
	}
	canonicalDoc, err := readSchemaDocEntry(arch)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := encx.WritePreamble(&buf, schema); err != nil {
		return err
	}
	return logicalShardCohesion(canonicalDoc.Schema, buf.Bytes(), archivePath)
}

// logicalShardCohesion is the row-independent prefix of mergeShard /
// mergeGroupedShard with no stored shards: flatten when either side is
// grouped, reconcileFlat, then the dictionary union. Its refusals are
// the merge's own.
func logicalShardCohesion(canonical *encoding.Schema, incoming []byte, archivePath string) error {
	incSchema, err := readSinglePulseSchema(incoming)
	if err != nil {
		return err
	}
	canon := cloneSchemaForArchive(canonical)
	if canonical.HasGroups() || incSchema.HasGroups() {
		flat, logical, ferr := encx.FlattenCohortBytes(incoming)
		if ferr != nil {
			return ferr
		}
		incoming, incSchema = flat, logical
		canon = cloneSchemaForArchive(canonical.Logical())
	}
	canon, _, incSchema, err = reconcileFlat(&shardMerge{}, canon, nil, incoming, incSchema, archivePath)
	if err != nil {
		return err
	}
	_, _, err = encx.MergeDictUnion(canon, incSchema)
	return err
}
