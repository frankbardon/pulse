package service

import (
	"context"
	"fmt"
	"io"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/spf13/afero"
)

// SetShardDistinct configures the per-shard distinct counts every
// archive writer records in `_schema.pulse` (U73 #500): limit is the
// per-field key cap (0 = encx.DefaultDistinctLimit; pulse.New refuses a
// negative one) and disabled opts out — newly written shards are then
// recorded DistinctOptedOut and no record is read to count them.
func (s *Service) SetShardDistinct(limit int, disabled bool) {
	s.distinctLimit = limit
	s.distinctDisabled = disabled
}

// distinctPass counts the shards ONE archive write stores, against that
// write's canonical schema, through one reused DistinctCounter (memory
// for the largest shard, never the sum). Opted out, it reads nothing and
// records every shard DistinctOptedOut.
type distinctPass struct {
	disabled  bool
	canonical *encoding.Schema
	fields    []encx.DistinctField
	counter   *encx.DistinctCounter
}

func (s *Service) newDistinctPass(canonical *encoding.Schema) *distinctPass {
	return &distinctPass{
		disabled:  s.distinctDisabled,
		canonical: canonical,
		fields:    encx.EligibleDistinctFields(canonical),
		counter:   encx.NewDistinctCounter(s.distinctLimit),
	}
}

// count counts one stored shard payload read from r.
func (p *distinctPass) count(r io.Reader, name string) ([]encx.DistinctCount, error) {
	if p.disabled {
		return encx.OptedOutCounts(len(p.fields)), nil
	}
	counts, err := p.counter.Count(r, p.canonical, p.fields)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.PULSE_SHARD_HEADER_INVALID,
			fmt.Sprintf("counting distinct values of shard %q", name))
	}
	return counts, nil
}

// countIdentified counts one stored shard payload read from r and
// returns the identity of every byte r held (encx.IdentityHasher, fed
// through the same read; the tail past the counted records is drained
// into it). Opted out, it reads nothing and the identity is unknown.
func (p *distinctPass) countIdentified(r io.Reader, name string) ([]encx.DistinctCount, encx.ShardIdentity, error) {
	if p.disabled {
		counts, err := p.count(nil, name)
		return counts, encx.ShardIdentity{}, err
	}
	h := encx.NewIdentityHasher()
	tee := io.TeeReader(r, h)
	counts, err := p.count(tee, name)
	if err != nil {
		return nil, encx.ShardIdentity{}, err
	}
	if _, err := io.Copy(io.Discard, tee); err != nil {
		return nil, encx.ShardIdentity{}, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("reading shard %q", name))
	}
	return counts, h.Identity(), nil
}

// keptShardRefs is the NextArchiveDistinct ref of every named shard
// whose bytes the write keeps, its identity read off a's central
// directory.
func keptShardRefs(a *archiveFile, names []string) []encx.DistinctShardRef {
	ident := archiveShardIdentities(a)
	refs := make([]encx.DistinctShardRef, len(names))
	for i, name := range names {
		refs[i] = encx.KeptShard(name, encx.EntryIdentity(ident[name]))
	}
	return refs
}

// recountKept counts, off its stored bytes, every kept shard prior
// cannot carry: one whose entry is Stale at the shard's central-directory
// identity (replaced outside Pulse under the same name, or recorded by a
// trailer without identities — U73 #720) and, when backfill is set
// (compact), every shard prior does not fully know. A shard prior can
// carry is never read.
func (p *distinctPass) recountKept(ctx context.Context, a *archiveFile, refs []encx.DistinctShardRef, prior *encx.ArchiveDistinct, backfill bool) (map[string][]encx.DistinctCount, error) {
	fresh := map[string][]encx.DistinctCount{}
	for _, ref := range refs {
		recount := prior.Stale(ref.Name, ref.Current)
		if backfill && !prior.Known(ref.Name, ref.Current, p.fields) {
			recount = true
		}
		if !recount {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		counts, err := p.entry(a, ref.Name)
		if err != nil {
			return nil, err
		}
		fresh[ref.Name] = counts
	}
	return fresh, nil
}

// entry counts the named entry of an open archive: a stored entry off
// its raw bytes (a SectionReader — no CRC re-check, the bytes a raw copy
// re-emits), a deflated one through the zip reader.
func (p *distinctPass) entry(a *archiveFile, name string) ([]encx.DistinctCount, error) {
	if p.disabled {
		return p.count(nil, name)
	}
	if a.arch.IsStored(name) {
		sr, err := a.arch.OpenAt(name)
		if err != nil {
			return nil, err
		}
		return p.count(&sr, name)
	}
	rc, err := a.arch.Open(name)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return p.count(rc, name)
}

// file counts a source file streamed off fsys, and returns its identity
// (countIdentified): the bytes a create stores for it verbatim.
func (p *distinctPass) file(fsys afero.Fs, path, name string) ([]encx.DistinctCount, encx.ShardIdentity, error) {
	if p.disabled {
		return p.countIdentified(nil, name)
	}
	f, err := fsys.Open(path)
	if err != nil {
		return nil, encx.ShardIdentity{}, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("reading shard source %s", path))
	}
	defer f.Close()
	return p.countIdentified(f, name)
}

// readPriorDistinct reads the DIST trailer of an archive's current
// `_schema.pulse`, the counts a rewrite CARRIES for shards whose bytes it
// keeps. A trailer that is absent, of a newer version, or malformed
// carries nothing (nil): those shards are recorded DistinctAbsent, never
// given a fabricated count, and the write itself is never refused over
// an advisory stat.
func readPriorDistinct(arch *encx.Archive) *encx.ArchiveDistinct {
	rc, err := arch.Open(encx.ReservedSchemaName)
	if err != nil {
		return nil
	}
	defer rc.Close()
	_, dist, err := encx.ReadSchemaDocDistinct(rc)
	if err != nil {
		return nil
	}
	return dist
}
