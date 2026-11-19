package service

import (
	"context"
	"io"
	"strconv"

	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// A union cohort (types.Request.Union) is read as an IN-MEMORY shard
// archive: its shards are the members' shards, flattened in declared
// order (a single file or an anchored shard is one shard, a shard
// archive contributes each of its own), and its schema is the members'
// merged schema (descx.MergeUnion — the rule predict judges) with the
// virtual source field appended. Each member shard's records are
// rewritten into the merged layout as they are read
// (encx.UnionRowPlan: dictionary remap, set widening, the source
// field's value), so every arm that reads an archive — the serial
// shard iterator, the per-shard parallel reducer, the crosstab arms —
// reads a union unchanged, with the merge positions an archive of the
// same shards gives its rows. Nothing is written: no merged file, no
// index, no sidecar; a union never uses a cohort's point-lookup index.

// unionView is a union cohort's members and how its flattened shards
// resolve to them.
type unionView struct {
	members []unionMember
	// refs resolves a flattened shard name (Cohort.shards Filename) to
	// its member and entry.
	refs map[string]unionShardRef
	// warnings are the merge's description-class divergences, raised on
	// the run's response as predict raises them on its envelope.
	warnings []encx.CohesionWarning
}

// unionMember is one opened member.
type unionMember struct {
	label  string
	cohort *Cohort
	plan   *encx.UnionRowPlan
}

// unionShardRef locates one flattened shard: the member, and the
// member archive's entry ("" for a member read as one file — a single
// file or an anchored shard).
type unionShardRef struct {
	member int
	entry  string
}

// unionShardName is the flattened shard name of member i's entry: the
// member index keeps names unique across members.
func unionShardName(i int, entry string) string {
	if entry == "" {
		return "union[" + strconv.Itoa(i) + "]"
	}
	return "union[" + strconv.Itoa(i) + "]/" + entry
}

// openRequestCohort opens the cohort req reads and the path its
// response names: req.Cohort through Open, or — for a union — every
// member, merged (openUnion; path "").
func (s *Service) openRequestCohort(ctx context.Context, req *types.Request) (*Cohort, string, error) {
	if len(req.Union) > 0 {
		c, err := s.openUnion(ctx, req.Union, req.UnionSourceField)
		return c, "", err
	}
	path := resolveCohortPath(req.Cohort)
	c, err := s.Open(ctx, path)
	return c, path, err
}

// openRootCohort is openRequestCohort for a root that is not a Request
// (a chain's stage 0, a facet, a sample, a lookup): the union shape
// rules first (descx.UnionShapeRefusalOf — the rule the root's predict
// judges where it reads the cohort), then the cohort or the union.
// required names the root in the SERVICE_VALIDATION refusal of a root
// naming neither.
func (s *Service) openRootCohort(ctx context.Context, cohort *types.Cohort, union []types.UnionMember, sourceField, required string) (*Cohort, string, error) {
	if err := descx.UnionShapeRefusalOf(cohort, union, sourceField); err != nil {
		return nil, "", err
	}
	if len(union) > 0 {
		c, err := s.openUnion(ctx, union, sourceField)
		return c, "", err
	}
	if cohort == nil {
		return nil, "", errors.NewCodedError(errors.SERVICE_VALIDATION, required+" requires a cohort")
	}
	path := resolveCohortPath(cohort)
	c, err := s.Open(ctx, path)
	return c, path, err
}

// openUnion opens every member of union in declared order — each
// exactly as a single request cohort is opened (a single file, a shard
// archive, an anchored shard) — merges their schemas through
// descx.MergeUnion (predict's rule, so a refusal here is predict's)
// and returns the union cohort, its source field named sourceField
// (empty: the default). A member that cannot be opened is its own
// fault, located with the member index and label. No record is read: a
// member file's record count comes from its header and length.
func (s *Service) openUnion(ctx context.Context, union []types.UnionMember, sourceField string) (*Cohort, error) {
	members := make([]unionMember, 0, len(union))
	schemas := make([]descx.UnionMemberSchema, 0, len(union))
	for i, m := range union {
		c, err := s.Open(ctx, resolveCohortPath(m.Cohort))
		if err != nil {
			return nil, descx.LocateUnionMember(err, i, m.Label)
		}
		members = append(members, unionMember{label: m.Label, cohort: c})
		schemas = append(schemas, descx.UnionMemberSchema{Label: m.Label, Schema: c.Schema(), Shards: max(len(c.shards), 1)})
	}
	merged, warnings, err := descx.MergeUnion(schemas, descx.UnionSourceFieldNameOf(sourceField))
	if err != nil {
		return nil, err
	}
	view := &unionView{members: members, refs: map[string]unionShardRef{}, warnings: warnings}
	var shards []ShardEntry
	for i := range view.members {
		m := &view.members[i]
		plan, err := encx.NewUnionRowPlan(m.cohort.Schema(), merged, i)
		if err != nil {
			return nil, err
		}
		m.plan = plan
		if len(m.cohort.shards) > 0 {
			for _, sh := range m.cohort.shards {
				name := unionShardName(i, sh.Filename)
				view.refs[name] = unionShardRef{member: i, entry: sh.Filename}
				shards = append(shards, ShardEntry{Filename: name, RecordCount: sh.RecordCount})
			}
			continue
		}
		n, err := s.CountRecords(ctx, m.cohort.path)
		if err != nil {
			return nil, descx.LocateUnionMember(err, i, m.label)
		}
		name := unionShardName(i, "")
		view.refs[name] = unionShardRef{member: i}
		shards = append(shards, ShardEntry{Filename: name, RecordCount: int64(n)})
	}
	return &Cohort{schema: merged, fs: s.fs.Fs(), shards: shards, union: view}, nil
}

// unionWarnings lifts the union merge's divergences onto resp, as
// predict lifts them onto its envelope.
func unionWarnings(resp *types.Response, cohort *Cohort) {
	if resp == nil || cohort == nil || cohort.union == nil {
		return
	}
	for _, w := range cohort.union.warnings {
		resp.Warnings = append(resp.Warnings, &types.ResponseWarning{Code: string(w.Code), Message: w.Message, Details: w.Details})
	}
}

// openCohortShards opens an archive-shaped cohort for random access:
// its archive, or — for a union — every member (unionView.open).
func openCohortShards(cohort *Cohort, fsys afero.Fs) (*archiveFile, error) {
	if cohort.union != nil {
		return cohort.union.open()
	}
	return openArchiveFile(fsys, cohort.path)
}

// unionScan is a union opened for one scan: each archive member's
// archive (central directory only) and each other member's file
// handle, all concurrency-safe ReaderAts, so the per-shard reducer's
// workers read member shards at once. Close releases them.
type unionScan struct {
	v        *unionView
	archives []*archiveFile // per member; nil for a member read as one file
	files    []afero.File
	ras      []io.ReaderAt
	sizes    []int64
}

// open opens every member for random access and returns the union's
// scan handle. No record is read.
func (v *unionView) open() (*archiveFile, error) {
	u := &unionScan{
		v:        v,
		archives: make([]*archiveFile, len(v.members)),
		files:    make([]afero.File, len(v.members)),
		ras:      make([]io.ReaderAt, len(v.members)),
		sizes:    make([]int64, len(v.members)),
	}
	for i := range v.members {
		c := v.members[i].cohort
		fsys := c.fs
		if len(c.shards) > 0 {
			a, err := openArchiveFile(fsys, c.path)
			if err != nil {
				_ = u.Close()
				return nil, descx.LocateUnionMember(err, i, v.members[i].label)
			}
			u.archives[i] = a
			continue
		}
		f, err := fsys.Open(c.path)
		if err != nil {
			_ = u.Close()
			return nil, descx.LocateUnionMember(errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
				"opening union member cohort: "+c.path), i, v.members[i].label)
		}
		u.files[i] = f
		st, err := f.Stat()
		if err != nil {
			_ = u.Close()
			return nil, descx.LocateUnionMember(errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
				"sizing union member cohort: "+c.path), i, v.members[i].label)
		}
		u.ras[i], u.sizes[i] = concurrentReaderAt(f), st.Size()
	}
	return &archiveFile{union: u}, nil
}

// rows returns the named flattened shard's records in the merged
// layout: the member shard (or file) past its header and schema, read
// logically (a grouped member expands to its ungrouped twin) and
// rewritten by the member's UnionRowPlan.
func (u *unionScan) rows(name string) (io.Reader, error) {
	ref, ok := u.v.refs[name]
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_SHARD_MISSING,
			"union shard "+strconv.Quote(name)+" is not a member shard", map[string]any{"entry": name})
	}
	m := &u.v.members[ref.member]
	var r io.Reader
	if a := u.archives[ref.member]; a != nil {
		sr, err := a.shardReader(ref.entry)
		if err != nil {
			return nil, err
		}
		r = sr
	} else {
		r = newSectionScanner(io.NewSectionReader(u.ras[ref.member], 0, u.sizes[ref.member]))
	}
	if err := skipShardPrelude(r, name); err != nil {
		return nil, err
	}
	lr, _, err := encx.NewLogicalStream(r, m.cohort.Schema())
	if err != nil {
		return nil, err
	}
	return encx.NewUnionRowStream(lr, m.plan), nil
}

// Close releases every member handle.
func (u *unionScan) Close() error {
	var first error
	for _, a := range u.archives {
		if a != nil {
			if err := a.Close(); err != nil && first == nil {
				first = err
			}
		}
	}
	for _, f := range u.files {
		if f != nil {
			if err := f.Close(); err != nil && first == nil {
				first = err
			}
		}
	}
	return first
}

// newUnionShardIter is newShardIter over a union cohort: the members'
// flattened shards, read through the union's scan handle.
func newUnionShardIter(cohort *Cohort) *shardIter {
	it := newShardIter(cohort.fs, "", cohort.schema, cohort.shards)
	it.union = cohort.union
	return it
}
