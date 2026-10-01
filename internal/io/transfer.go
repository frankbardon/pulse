package io

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/klauspost/compress/zstd"
	"github.com/spf13/afero"
)

// Transport-only compression.
//
// A transfer artifact (`.pulse.zst`) is ONE standard zstd stream (RFC
// 8878) whose decompressed content is the exact byte image of a `.pulse`
// cohort — single-file (0x01 or 0x02) or a whole shard archive. It is not
// a cohort layout: nothing in the read path accepts it, and every read
// surface refuses it with PULSE_COHORT_COMPRESSED. Compression at rest
// would cost fixed-stride random access, mmap, point lookup, parallel
// decode and the header-fast record count, so Pulse compresses only for
// the trip and decompresses back to a byte-identical cohort on arrival.
//
// Both directions stream: memory is bounded by the codec's window and
// the fixed I/O buffers, never by the cohort size. Because the artifact
// is a plain zstd frame with a content checksum, the stock `zstd -d`
// produces the same bytes Pulse does.

// TransferCodec is the one codec the transfer artifact uses.
const TransferCodec = "zstd"

// TransferExtension is the conventional suffix appended to a cohort's
// file name for its transfer artifact.
const TransferExtension = ".zst"

// DefaultTransferLevel is the zstd level used when a job leaves Level at
// zero. Level 3 is zstd's own default and was the level measured during
// planning.
const DefaultTransferLevel = 3

// MinTransferLevel and MaxTransferLevel bound the accepted zstd levels.
// The encoder maps them onto its four speed classes
// (zstd.EncoderLevelFromZstd): 1-2 fastest, 3-5 default, 6-9 better,
// 10-22 best.
const (
	MinTransferLevel = 1
	MaxTransferLevel = 22
)

// transferIOBuffer is the buffered-I/O size on both sides of the codec.
const transferIOBuffer = 256 << 10

// transferTempPattern is the afero.TempFile pattern for the decompress
// side's staging file, written beside the output then renamed over it.
const transferTempPattern = ".transfer-*"

// Transfer layout names reported on TransferReport.Layout.
const (
	TransferLayoutSingleFile   = "single_file"
	TransferLayoutShardArchive = "shard_archive"
)

// TransferExportJob compresses a cohort's exact bytes into a transfer
// artifact. Source must be an uncompressed `.pulse` (single-file or
// shard archive); Output is overwritten.
type TransferExportJob struct {
	// FS is the filesystem both paths resolve against. Required.
	FS afero.Fs
	// Source is the cohort path.
	Source string
	// Output is the artifact path, conventionally Source + ".zst".
	Output string
	// Level is the zstd level, MinTransferLevel..MaxTransferLevel. Zero
	// selects DefaultTransferLevel.
	Level int
}

// TransferImportJob decompresses a transfer artifact back into a
// byte-identical `.pulse` at rest. The output is staged beside Output and
// renamed over it only once the whole stream decoded, its checksum held
// and the decoded bytes began with a Pulse magic — a refusal or a
// damaged artifact leaves nothing behind.
type TransferImportJob struct {
	// FS is the filesystem both paths resolve against. Required.
	FS afero.Fs
	// Source is the `.pulse.zst` artifact path.
	Source string
	// Output is the cohort path to write.
	Output string
	// Overwrite permits replacing an existing Output. Default false:
	// silently replacing a working cohort is refused.
	Overwrite bool
}

// TransferReport describes one transfer compress or decompress.
// SHA256 is always the digest of the UNCOMPRESSED cohort bytes, so the
// sender's and the receiver's reports compare directly.
type TransferReport struct {
	Source string `json:"source"`
	Output string `json:"output"`
	Codec  string `json:"codec"`
	// Level is the zstd level used; set on compress only.
	Level int `json:"level,omitempty"`
	// Layout is single_file or shard_archive.
	Layout string `json:"layout"`
	// CohortBytes is the uncompressed cohort size.
	CohortBytes int64 `json:"cohort_bytes"`
	// CompressedBytes is the artifact size.
	CompressedBytes int64 `json:"compressed_bytes"`
	// Ratio is CohortBytes / CompressedBytes (0 when the artifact is
	// empty, which cannot happen for a real stream).
	Ratio float64 `json:"ratio"`
	// SHA256 is the lowercase hex digest of the uncompressed cohort.
	SHA256 string `json:"sha256"`
}

func transferInvalid(reason, msg string, details map[string]any) *errors.CodedError {
	d := map[string]any{"reason": reason}
	for k, v := range details {
		d[k] = v
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_TRANSFER_INVALID, msg, d)
}

// cohortLayout classifies the first bytes of an uncompressed cohort, or
// returns "" when they are not a Pulse cohort.
func cohortLayout(prefix []byte) string {
	if len(prefix) >= len(encoding.MagicBytes) && [8]byte(prefix[:8]) == encoding.MagicBytes {
		return TransferLayoutSingleFile
	}
	if len(prefix) >= len(encx.ZipMagic) && [4]byte(prefix[:4]) == encx.ZipMagic {
		return TransferLayoutShardArchive
	}
	return ""
}

// ResolveTransferLevel validates level and applies the default.
func ResolveTransferLevel(level int) (int, error) {
	if level == 0 {
		return DefaultTransferLevel, nil
	}
	if level < MinTransferLevel || level > MaxTransferLevel {
		return 0, transferInvalid("level",
			fmt.Sprintf("zstd level %d is out of range %d..%d", level, MinTransferLevel, MaxTransferLevel),
			map[string]any{"level": level, "min": MinTransferLevel, "max": MaxTransferLevel})
	}
	return level, nil
}

// countingWriter counts bytes written through it.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// Run compresses Source into Output.
func (j *TransferExportJob) Run(ctx context.Context) (*TransferReport, error) {
	if j.FS == nil {
		return nil, fmt.Errorf("TransferExportJob.FS is required")
	}
	level, err := ResolveTransferLevel(j.Level)
	if err != nil {
		return nil, err
	}

	src, err := j.FS.Open(j.Source)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE, fmt.Sprintf("opening cohort for transfer: %s", j.Source))
	}
	defer src.Close()
	br := bufio.NewReaderSize(src, transferIOBuffer)

	// Sniff the layout before touching Output: a CSV, a truncated file or
	// an artifact that is already compressed is refused up front.
	prefix, _ := br.Peek(len(encoding.MagicBytes))
	if encoding.IsZstdMagic(prefix) {
		return nil, transferInvalid("already_compressed",
			fmt.Sprintf("%s is already a zstd transfer artifact", j.Source), map[string]any{"path": j.Source})
	}
	layout := cohortLayout(prefix)
	if layout == "" {
		return nil, transferInvalid("not_a_cohort",
			fmt.Sprintf("%s is not a Pulse cohort (neither the single-file magic nor a shard archive)", j.Source),
			map[string]any{"path": j.Source})
	}

	out, err := j.FS.OpenFile(j.Output, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO, fmt.Sprintf("creating transfer artifact: %s", j.Output))
	}
	fail := func(e error) (*TransferReport, error) {
		_ = out.Close()
		_ = j.FS.Remove(j.Output)
		return nil, e
	}

	counted := &countingWriter{w: out}
	bw := bufio.NewWriterSize(counted, transferIOBuffer)
	enc, err := zstd.NewWriter(bw,
		zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(level)),
		zstd.WithEncoderCRC(true))
	if err != nil {
		return fail(errors.WrapCodedError(err, errors.ENCODING_IO, "creating zstd encoder"))
	}
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(enc, sum), ctxReader{ctx: ctx, r: br})
	if err != nil {
		_ = enc.Close()
		return fail(errors.WrapCodedError(err, errors.ENCODING_IO, fmt.Sprintf("compressing %s", j.Source)))
	}
	if err := enc.Close(); err != nil {
		return fail(errors.WrapCodedError(err, errors.ENCODING_IO, "finishing zstd stream"))
	}
	if err := bw.Flush(); err != nil {
		return fail(errors.WrapCodedError(err, errors.ENCODING_IO, fmt.Sprintf("writing %s", j.Output)))
	}
	if err := out.Close(); err != nil {
		_ = j.FS.Remove(j.Output)
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO, fmt.Sprintf("closing %s", j.Output))
	}
	return newTransferReport(j.Source, j.Output, level, layout, n, counted.n, sum), nil
}

// Run decompresses Source into Output.
func (j *TransferImportJob) Run(ctx context.Context) (*TransferReport, error) {
	if j.FS == nil {
		return nil, fmt.Errorf("TransferImportJob.FS is required")
	}
	if !j.Overwrite {
		if _, err := j.FS.Stat(j.Output); err == nil {
			return nil, transferInvalid("output_exists",
				fmt.Sprintf("%s already exists; pass overwrite to replace it", j.Output), map[string]any{"path": j.Output})
		}
	}

	src, err := j.FS.Open(j.Source)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE, fmt.Sprintf("opening transfer artifact: %s", j.Source))
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.SERVICE_RESOURCE, fmt.Sprintf("stat transfer artifact: %s", j.Source))
	}
	br := bufio.NewReaderSize(src, transferIOBuffer)

	prefix, _ := br.Peek(len(encoding.MagicBytes))
	if !encoding.IsZstdMagic(prefix) {
		msg := fmt.Sprintf("%s is not a zstd transfer artifact", j.Source)
		if cohortLayout(prefix) != "" {
			msg += " (it is already an uncompressed cohort: open it directly)"
		}
		return nil, transferInvalid("not_zstd", msg, map[string]any{"path": j.Source})
	}

	dec, err := zstd.NewReader(br)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO, "creating zstd decoder")
	}
	defer dec.Close()

	dir, base := filepath.Dir(j.Output), filepath.Base(j.Output)
	tmp, err := afero.TempFile(j.FS, dir, base+transferTempPattern)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO, fmt.Sprintf("creating staging file beside %s", j.Output))
	}
	tmpName := tmp.Name()
	abort := func(e error) (*TransferReport, error) {
		_ = tmp.Close()
		_ = j.FS.Remove(tmpName)
		return nil, e
	}

	dr := bufio.NewReaderSize(dec, transferIOBuffer)
	head, perr := dr.Peek(len(encoding.MagicBytes))
	if perr != nil && perr != io.EOF && perr != bufio.ErrBufferFull {
		return abort(corruptStream(j.Source, perr))
	}
	layout := cohortLayout(head)
	if layout == "" {
		return abort(transferInvalid("not_a_cohort",
			fmt.Sprintf("%s decompressed to bytes that are not a Pulse cohort", j.Source), map[string]any{"path": j.Source}))
	}

	sum := sha256.New()
	tw := bufio.NewWriterSize(tmp, transferIOBuffer)
	n, err := io.Copy(io.MultiWriter(tw, sum), ctxReader{ctx: ctx, r: dr})
	if err != nil {
		if ctx.Err() != nil {
			return abort(ctx.Err())
		}
		return abort(corruptStream(j.Source, err))
	}
	if err := tw.Flush(); err != nil {
		return abort(errors.WrapCodedError(err, errors.ENCODING_IO, fmt.Sprintf("writing %s", j.Output)))
	}
	// fsync BEFORE the rename, as every atomic cohort write does.
	if err := tmp.Sync(); err != nil {
		return abort(errors.WrapCodedError(err, errors.ENCODING_IO, fmt.Sprintf("syncing %s", j.Output)))
	}
	if err := tmp.Close(); err != nil {
		_ = j.FS.Remove(tmpName)
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO, fmt.Sprintf("closing %s", j.Output))
	}
	if err := j.FS.Rename(tmpName, j.Output); err != nil {
		_ = j.FS.Remove(tmpName)
		return nil, errors.WrapCodedError(err, errors.ENCODING_IO, fmt.Sprintf("renaming %s onto %s", tmpName, j.Output))
	}
	return newTransferReport(j.Source, j.Output, 0, layout, n, info.Size(), sum), nil
}

func corruptStream(path string, cause error) *errors.CodedError {
	e := transferInvalid("corrupt_stream",
		fmt.Sprintf("%s is truncated or damaged: %v", path, cause), map[string]any{"path": path})
	e.Cause = cause
	return e
}

func newTransferReport(src, dst string, level int, layout string, cohortBytes, compressedBytes int64, sum hash.Hash) *TransferReport {
	r := &TransferReport{
		Source:          src,
		Output:          dst,
		Codec:           TransferCodec,
		Level:           level,
		Layout:          layout,
		CohortBytes:     cohortBytes,
		CompressedBytes: compressedBytes,
		SHA256:          hex.EncodeToString(sum.Sum(nil)),
	}
	if compressedBytes > 0 {
		r.Ratio = float64(cohortBytes) / float64(compressedBytes)
	}
	return r
}
