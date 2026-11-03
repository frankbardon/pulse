package encoding

import (
	"bytes"
	"io"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// TestLogicalStream_SeekMatchesRead: a forward relative Seek over the
// grouped logical stream lands exactly where reading that many bytes
// would — within a row, across a row boundary, and across whole rows
// skipped without expansion — so a decode plan's SkipBytes over a 0x02
// cohort reads the same bytes as over its 0x01 twin. Anything but a
// forward relative seek is refused, and a seek past the end surfaces
// the end.
func TestLogicalStream_SeekMatchesRead(t *testing.T) {
	t.Parallel()

	fx := groupFixtures(t)[0]
	flat := flatCohort(t, fx.schema, fx.rows)
	grouped, gs := groupedTwin(t, flat, fx.specs)
	_, flatRegion := recordRegion(t, flat)
	_, phys := recordRegion(t, grouped)
	ls := int64(fx.schema.RecordByteSize())

	for _, hops := range [][]int64{{0}, {1}, {ls - 1}, {ls}, {ls + 3}, {3*ls + 5}, {2, ls, 7}, {ls - 2, 2*ls + 1}} {
		lr, _, err := NewLogicalStream(bytes.NewReader(phys), gs)
		if err != nil {
			t.Fatal(err)
		}
		sk := lr.(io.Seeker)
		var at int64
		for i, h := range hops {
			off, err := sk.Seek(h, io.SeekCurrent)
			at += h
			if err != nil || off != at {
				t.Fatalf("hops %v: seek %d = (%d, %v), want offset %d", hops, i, off, err, at)
			}
			// Read one byte between hops, as a decoder would.
			if i < len(hops)-1 {
				var b [1]byte
				if _, err := io.ReadFull(lr, b[:]); err != nil || b[0] != flatRegion[at] {
					t.Fatalf("hops %v: byte at %d = (%#x, %v), want %#x", hops, at, b[0], err, flatRegion[at])
				}
				at++
			}
		}
		rest, err := io.ReadAll(lr)
		if err != nil || !bytes.Equal(rest, flatRegion[at:]) {
			t.Fatalf("hops %v: tail after offset %d differs from the flat region (%v)", hops, at, err)
		}
	}

	lr, _, _ := NewLogicalStream(bytes.NewReader(phys), gs)
	sk := lr.(io.Seeker)
	for _, bad := range []struct {
		off    int64
		whence int
	}{{0, io.SeekStart}, {-1, io.SeekCurrent}, {0, io.SeekEnd}} {
		if _, err := sk.Seek(bad.off, bad.whence); !errors.HasCode(err, errors.ENCODING_INTERNAL) {
			t.Errorf("Seek(%d, %d): err = %v, want ENCODING_INTERNAL", bad.off, bad.whence, err)
		}
	}
	if n, err := lr.Read(nil); n != 0 || err != nil {
		t.Errorf("Read(nil) = (%d, %v), want (0, nil)", n, err)
	}
	// Past the end: over a seekable source the seek lands (io.Seeker
	// semantics) and the next read is a clean io.EOF; over a plain
	// reader the skipped rows cannot be consumed, so the seek itself
	// surfaces the end.
	if _, err := sk.Seek(int64(len(flatRegion))+ls, io.SeekCurrent); err != nil {
		t.Errorf("seek past the end over a seekable source: %v", err)
	}
	if _, err := lr.Read(make([]byte, 1)); err != io.EOF {
		t.Errorf("read after seeking past the end: err = %v, want io.EOF", err)
	}
	plain, _, _ := NewLogicalStream(io.MultiReader(bytes.NewReader(phys)), gs)
	if _, err := plain.(io.Seeker).Seek(int64(len(flatRegion))+ls, io.SeekCurrent); err == nil {
		t.Error("seek past the end over a plain reader succeeded")
	}
	plain, _, _ = NewLogicalStream(io.MultiReader(bytes.NewReader(phys)), gs)
	if _, err := plain.(io.Seeker).Seek(int64(len(flatRegion))+1, io.SeekCurrent); err == nil {
		t.Error("seek into a row past the end over a plain reader succeeded")
	}
}

// TestRowExpander_Refusals: a physical row shorter than the stride is
// refused, and an ungrouped schema passes through NewLogicalStream
// untouched.
func TestRowExpander_Refusals(t *testing.T) {
	t.Parallel()

	fx := groupFixtures(t)[0]
	_, gs := groupedTwin(t, flatCohort(t, fx.schema, fx.rows), fx.specs)
	x, err := NewRowExpander(gs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.Expand(nil, make([]byte, x.PhysicalStride()-1)); !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Errorf("short row: err = %v, want ENCODING_INVALID", err)
	}
	flat := &encoding.Schema{Fields: []encoding.Field{{Name: "a", Type: encoding.FieldTypeU8}}}
	src := bytes.NewReader([]byte{1})
	r, s, err := NewLogicalStream(src, flat)
	if err != nil || r != io.Reader(src) || s != flat {
		t.Errorf("ungrouped schema was wrapped: (%T, %v, %v)", r, s == flat, err)
	}
	bad := &encoding.Schema{Fields: flat.Fields, Groups: []encoding.Group{{Members: []encoding.GroupMember{{Field: 5}}}}}
	if _, err := NewRowExpander(bad); err == nil {
		t.Error("expander compiled a group naming no field")
	}
	if _, _, err := NewLogicalStream(src, bad); err == nil {
		t.Error("logical stream opened over a group naming no field")
	}
}
