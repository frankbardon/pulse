package service

import (
	"archive/zip"
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/spf13/afero"
)

// shardAdminTempPattern is the infix of every staged archive rewrite:
// a mutation of `<dir>/<base>` stages into `<dir>/<base>.pulse.tmp-<suffix>`
// beside it and renames that over the destination.
const shardAdminTempPattern = ".pulse.tmp-"

// shardStagedSuffix is the DETERMINISTIC suffix of the staged temp file.
// One name per destination (not a random one per attempt) bounds the
// garbage a crash can leave to one file per archive, and makes the
// next mutation's sweep (sweepStagedArchives) a name match rather than
// a guess. Concurrency is caller-owned by contract (no lock), so two
// writers never legitimately share the name.
const shardStagedSuffix = "staged"

// shardCopyBuffer is the copy window of a raw entry copy and of a
// streamed entry: the whole of a stored shard's resident cost.
const shardCopyBuffer = 256 << 10

// storedShards is an archive's stored shards as a merge sees them: names
// in archive order, one payload loaded at a time, and a record count
// that never loads the payload. mergeShard reads a payload only when the
// archive itself must move (an archive-level set widen or a constant
// group promotion), so the ordinary add never reads one.
type storedShards interface {
	shardCount() int
	shardName(i int) string
	loadShard(i int) ([]byte, error)
	shardRecords(i int) (int64, error)
}

// archiveShards is storedShards over an open archive: payloads are read
// one at a time (decoded through the zip reader, so CRC-verified), and
// counts come from the header-only PeekShardRecordCount.
type archiveShards struct {
	a     *archiveFile
	names []string
}

func (s *archiveShards) shardCount() int        { return len(s.names) }
func (s *archiveShards) shardName(i int) string { return s.names[i] }
func (s *archiveShards) loadShard(i int) ([]byte, error) {
	return readEntryBytes(s.a.arch, s.names[i])
}
func (s *archiveShards) shardRecords(i int) (int64, error) {
	return s.a.arch.PeekShardRecordCount(s.names[i])
}

// storedRewrite turns one stored shard's payload into the bytes the
// rewritten archive stores for it. mergeShard returns one when the
// archive itself moves; nil means every stored shard is copied as-is.
type storedRewrite func(name string, payload []byte) ([]byte, error)

// sumStoredRecords is the header-only aggregate of every stored shard.
func sumStoredRecords(stored storedShards) (uint64, error) {
	var total uint64
	for i := range stored.shardCount() {
		n, err := stored.shardRecords(i)
		if err != nil {
			return 0, err
		}
		total += uint64(n)
	}
	return total, nil
}

// archiveWriter streams a shard archive into a staged temp file beside
// its destination and publishes it with one rename. It is the ONE
// writer every archive mutation uses (create, add, remove, compact,
// filter-to-file), so the crash story is the same everywhere:
//
//   - the destination is never opened for writing — until the rename it
//     is byte-identical to what it was, and a crash at any point before
//     the rename leaves it openable at its old layout (the set-widen and
//     group-promotion rewrites lean on exactly this);
//   - the stage has one deterministic name, `<base>.pulse.tmp-staged`,
//     and opening a writer first sweeps every `<base>.pulse.tmp-*` a
//     crashed earlier mutation left behind (sweepStagedArchives);
//   - commit is zip Close → flush → fsync → close → rename → fsync of
//     the directory, so the published name never points at bytes, or a
//     directory entry, that a crash can still lose.
//
// Entries are written in call order; the caller writes the reserved
// `_schema.pulse` entry first (writeSchemaDoc), so its aggregate and
// shard count must be known up front — every caller derives them
// header-only. Memory is O(one entry): writeEntry holds the caller's
// slice, copyEntry / createEntry stream through one shardCopyBuffer.
//
// Usage: w := newArchiveWriter; defer w.abort(); write…; w.commit().
// abort after a successful commit is a no-op.
type archiveWriter struct {
	fsys    afero.Fs
	dst     string
	tmpName string
	f       afero.File
	bw      *bufio.Writer
	zw      *zip.Writer
	buf     []byte
	done    bool
}

// stagedArchivePath is the deterministic stage of dst.
func stagedArchivePath(dst string) string {
	return filepath.Join(filepath.Dir(dst), filepath.Base(dst)+shardAdminTempPattern+shardStagedSuffix)
}

// newArchiveWriter sweeps dst's orphan stages and opens a fresh one.
//
// The stage sits BESIDE dst — never in "": afero.TempFile maps an empty
// dir to os.TempDir(), which under Options{DataDir} (a BasePathFs)
// resolves outside the base path; filepath.Dir yields "." for a
// root-level archive, which every afero backend resolves against its
// own root. The rename is therefore same-directory, hence atomic.
func newArchiveWriter(fsys afero.Fs, dst string) (*archiveWriter, error) {
	sweepStagedArchives(fsys, dst)
	tmpName := stagedArchivePath(dst)
	f, err := fsys.OpenFile(tmpName, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("creating staged archive for %s", dst))
	}
	bw := bufio.NewWriterSize(f, shardCopyBuffer)
	return &archiveWriter{
		fsys:    fsys,
		dst:     dst,
		tmpName: tmpName,
		f:       f,
		bw:      bw,
		zw:      zip.NewWriter(bw),
		buf:     make([]byte, shardCopyBuffer),
	}, nil
}

// sweepStagedArchives removes every `<base>.pulse.tmp-*` beside dst: the
// deterministic stage and the random-suffix stages earlier releases
// wrote. Only dst's own stages are touched — another archive in the
// same directory may be mid-mutation. Best effort: a stage that will not
// go is truncated by the next open of the deterministic name anyway.
func sweepStagedArchives(fsys afero.Fs, dst string) {
	dir := filepath.Dir(dst)
	prefix := filepath.Base(dst) + shardAdminTempPattern
	d, err := fsys.Open(dir)
	if err != nil {
		return
	}
	names, err := d.Readdirnames(-1)
	_ = d.Close()
	if err != nil {
		return
	}
	for _, n := range names {
		if strings.HasPrefix(n, prefix) {
			_ = fsys.Remove(filepath.Join(dir, n))
		}
	}
}

func (w *archiveWriter) wrap(err error, what string) error {
	return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
		fmt.Sprintf("%s for %s", what, w.dst))
}

// writeSchemaDoc writes the reserved `_schema.pulse` entry.
func (w *archiveWriter) writeSchemaDoc(schema *encoding.Schema, aggregate uint64, shardCount int) error {
	payload, err := buildSchemaDocPayload(schema, aggregate, shardCount, w.dst)
	if err != nil {
		return err
	}
	return w.writeEntry(encx.ReservedSchemaName, payload)
}

// writeEntry adds one store-only entry holding payload.
func (w *archiveWriter) writeEntry(name string, payload []byte) error {
	return writeArchiveEntry(w.zw, name, payload)
}

// createEntry opens one store-only entry for streaming: write the whole
// payload to the returned writer before the next entry call. The zip
// writer hashes and counts it on the fly, so the payload is never held.
func (w *archiveWriter) createEntry(name string) (io.Writer, error) {
	ew, err := w.zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("creating archive entry %q", name))
	}
	return ew, nil
}

// copyEntry copies src's named entry into the stage without decoding
// it: a store-only entry RAW (encx.Archive.CopyStoredEntry —
// zip.Writer.CreateRaw from a SectionReader, the source CRC and sizes
// verbatim), a deflated one (a third-party zip tool's) inflated through
// the zip reader — CRC-checked at EOF — and re-stored, as every rewrite
// before the streaming writer did.
func (w *archiveWriter) copyEntry(src *archiveFile, name string) error {
	if src.arch.IsStored(name) {
		return src.arch.CopyStoredEntry(w.zw, name, w.buf)
	}
	rc, err := src.arch.Open(name)
	if err != nil {
		return err
	}
	defer rc.Close()
	ew, err := w.createEntry(name)
	if err != nil {
		return err
	}
	if _, err := io.CopyBuffer(ew, rc, w.buf); err != nil {
		return errors.WrapCodedError(err, errors.ENCODING_IO,
			fmt.Sprintf("re-storing archive entry %q", name))
	}
	return nil
}

// commit finalises the zip, makes the stage durable and renames it over
// the destination, then fsyncs the directory so the rename itself
// survives a crash. Any failure before the rename removes the stage
// (abort) and leaves the destination untouched.
func (w *archiveWriter) commit() error {
	if err := w.finish(); err != nil {
		return err
	}
	return w.publish()
}

// finish finalises the zip and makes the stage durable WITHOUT renaming
// it: the stage at stagedPath is then a complete archive a caller may
// open and read (index upkeep builds the new shard's sidecars from it)
// before publish renames it over the destination. abort still removes
// it until publish succeeds.
func (w *archiveWriter) finish() error {
	if err := w.zw.Close(); err != nil {
		return w.wrap(err, "finalising archive zip writer")
	}
	if err := w.bw.Flush(); err != nil {
		return w.wrap(err, "writing staged archive")
	}
	// fsync BEFORE the rename: a rename that lands while the stage's
	// contents are still only in the page cache publishes a name
	// pointing at bytes a crash can still lose. That matters most for
	// the set-widen rewrite, where the alternative to "old archive" is
	// not "missing archive" but "archive whose schema block promises a
	// stride its records do not have".
	if err := w.f.Sync(); err != nil {
		return w.wrap(err, "syncing staged archive")
	}
	f := w.f
	w.f = nil
	if err := f.Close(); err != nil {
		return w.wrap(err, "closing staged archive")
	}
	return nil
}

// stagedPath is the stage the writer streams into: after finish, a
// complete archive not yet published.
func (w *archiveWriter) stagedPath() string { return w.tmpName }

// publish renames the finished stage over the destination and fsyncs
// the directory. Call it only after finish.
func (w *archiveWriter) publish() error {
	if err := w.fsys.Rename(w.tmpName, w.dst); err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("renaming staged archive %s to %s", w.tmpName, w.dst))
	}
	w.done = true
	syncDir(w.fsys, filepath.Dir(w.dst))
	return nil
}

// abort releases and removes the stage unless commit published it.
// Idempotent; the destination is never touched.
func (w *archiveWriter) abort() {
	if w == nil || w.done {
		return
	}
	w.done = true
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
	_ = w.fsys.Remove(w.tmpName)
}

// syncDir fsyncs a directory so a rename into it is durable. Best
// effort, deliberately: it runs AFTER the rename has published the new
// archive, so failing the mutation here would report an error for a
// change that already happened, and some backends (an in-memory fs, a
// platform that refuses to fsync a directory handle) cannot sync one at
// all.
func syncDir(fsys afero.Fs, dir string) {
	d, err := fsys.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}

// atomicWrite publishes data at dst through the archive writer's stage:
// the same deterministic temp name, orphan sweep, fsync, rename and
// directory fsync, for a caller that already holds the whole payload
// (filter-to-file's archive arm).
func atomicWrite(fsys afero.Fs, dst string, data []byte) error {
	sweepStagedArchives(fsys, dst)
	tmpName := stagedArchivePath(dst)
	f, err := fsys.OpenFile(tmpName, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("creating temp file for atomic rename of %s", dst))
	}
	fail := func(err error, what string) error {
		_ = f.Close()
		_ = fsys.Remove(tmpName)
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("%s for %s", what, dst))
	}
	if _, err := f.Write(data); err != nil {
		return fail(err, "writing temp file")
	}
	if err := f.Sync(); err != nil {
		return fail(err, "syncing temp file")
	}
	if err := f.Close(); err != nil {
		_ = fsys.Remove(tmpName)
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("closing temp file for %s", dst))
	}
	if err := fsys.Rename(tmpName, dst); err != nil {
		_ = fsys.Remove(tmpName)
		return errors.WrapCodedError(err, errors.SERVICE_RESOURCE,
			fmt.Sprintf("renaming temp file %s to %s", tmpName, dst))
	}
	syncDir(fsys, filepath.Dir(dst))
	return nil
}
