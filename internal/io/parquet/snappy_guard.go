package parquet

import (
	"fmt"

	"github.com/apache/arrow-go/v18/parquet/compress"
	"github.com/klauspost/compress/snappy"
)

// boundedSnappy is the Parquet SNAPPY codec with one guard on decode: a
// block whose stream header declares more bytes than a non-empty dst
// holds is refused before it is decoded. The page reader sizes dst from the page
// header's uncompressed size (itself capped by the reader properties)
// and refuses any output of another length after decoding, so the
// guard refuses nothing the reader would accept. Without it the
// decoder allocates whatever the stream header declares: FuzzParquetImport
// found a 2 KiB file whose snappy page claimed 3 GB.
type boundedSnappy struct{ compress.Codec }

func (b boundedSnappy) Decode(dst, src []byte) []byte {
	out, err := b.DecodeWithError(dst, src)
	if err != nil {
		panic(err)
	}
	return out
}

func (b boundedSnappy) DecodeWithError(dst, src []byte) ([]byte, error) {
	n, err := snappy.DecodedLen(src)
	if err != nil {
		return nil, err
	}
	if len(dst) > 0 && n > len(dst) {
		return nil, fmt.Errorf("parquet: snappy block declares %d decoded bytes, its page holds %d", n, len(dst))
	}
	return snappy.Decode(dst, src)
}

// The codec registry is the decoder library's, process-wide, and it
// has no per-reader override: the guarded codec replaces the stock one
// for every Parquet read in the process. A caller handing an empty dst
// (the codec sizes the output itself) gets the stock decode, so the
// guard fires only on the page reader's shape, where dst is the page's
// declared size and a longer output is refused after decoding anyway.
func init() {
	stock, err := compress.GetCodec(compress.Codecs.Snappy)
	if err != nil {
		panic(err)
	}
	compress.RegisterCodec(compress.Codecs.Snappy, boundedSnappy{stock})
}
