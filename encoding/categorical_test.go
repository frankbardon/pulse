package encoding

import (
	"bytes"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

func TestCategoricalDictionary_AddAndResolve(t *testing.T) {
	d := NewDictionary()

	id0, err := d.Add("apple")
	if err != nil {
		t.Fatalf("Add(apple): %v", err)
	}
	if id0 != 0 {
		t.Errorf("first Add returned %d, want 0", id0)
	}

	id1, err := d.Add("banana")
	if err != nil {
		t.Fatalf("Add(banana): %v", err)
	}
	if id1 != 1 {
		t.Errorf("second Add returned %d, want 1", id1)
	}

	// Duplicate returns same ID.
	id0dup, err := d.Add("apple")
	if err != nil {
		t.Fatalf("Add(apple) dup: %v", err)
	}
	if id0dup != 0 {
		t.Errorf("dup Add returned %d, want 0", id0dup)
	}

	if d.Count() != 2 {
		t.Errorf("Count() = %d, want 2", d.Count())
	}

	if got := d.Resolve(0); got != "apple" {
		t.Errorf("Resolve(0) = %q, want apple", got)
	}
	if got := d.Resolve(1); got != "banana" {
		t.Errorf("Resolve(1) = %q, want banana", got)
	}
}

func TestCategoricalDictionary_IDFor(t *testing.T) {
	d := NewDictionary()
	d.Add("x")
	d.Add("y")

	id, ok := d.IDFor("x")
	if !ok || id != 0 {
		t.Errorf("IDFor(x) = %d, %v; want 0, true", id, ok)
	}

	_, ok = d.IDFor("z")
	if ok {
		t.Error("IDFor(z) should return false")
	}
}

func TestCategoricalDictionary_EmptyDict(t *testing.T) {
	d := NewDictionary()
	if d.Count() != 0 {
		t.Errorf("empty dict Count() = %d", d.Count())
	}

	// Resolve out of range returns empty string.
	if got := d.Resolve(0); got != "" {
		t.Errorf("Resolve(0) on empty = %q", got)
	}
}

func TestCategoricalDictionary_WriteRead(t *testing.T) {
	d := NewDictionary()
	d.Add("hello")
	d.Add("world")

	var buf bytes.Buffer
	if _, err := d.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}

	d2 := NewDictionary()
	if _, err := d2.ReadFrom(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}

	if d2.Count() != 2 {
		t.Fatalf("read back Count = %d, want 2", d2.Count())
	}
	if got := d2.Resolve(0); got != "hello" {
		t.Errorf("Resolve(0) = %q, want hello", got)
	}
	if got := d2.Resolve(1); got != "world" {
		t.Errorf("Resolve(1) = %q, want world", got)
	}
}

func TestCategoricalDictionary_MaxCapacity_U8(t *testing.T) {
	d := NewDictionary()
	for i := 0; i < 256; i++ {
		if _, err := d.Add(string(rune('A' + i))); err != nil {
			// We might get duplicates with rune trick, use a different approach.
			t.Fatalf("Add at %d: %v", i, err)
		}
	}
	if d.Count() != 256 {
		t.Fatalf("Count = %d, want 256", d.Count())
	}
}

func TestCategoricalDictionary_Overflow_U8(t *testing.T) {
	d := NewDictionary()
	for i := 0; i < 256; i++ {
		if _, err := d.AddWithLimit(uniqueStr(i), 256); err != nil {
			t.Fatalf("AddWithLimit at %d: %v", i, err)
		}
	}
	_, err := d.AddWithLimit(uniqueStr(256), 256)
	if err == nil {
		t.Fatal("expected overflow error")
	}
	if !errors.HasCode(err, errors.PULSE_IMPORT_CATEGORICAL_OVERFLOW) {
		t.Errorf("expected PULSE_IMPORT_CATEGORICAL_OVERFLOW, got: %v", err)
	}
}

func TestCategoricalDictionary_Overflow_U16(t *testing.T) {
	// Just test the limit check logic, don't fill 65536 entries.
	d := NewDictionary()
	// Manually set count to limit.
	for i := 0; i < 10; i++ {
		d.AddWithLimit(uniqueStr(i), 10)
	}
	_, err := d.AddWithLimit(uniqueStr(999), 10)
	if err == nil {
		t.Fatal("expected overflow error with limit 10")
	}
	if !errors.HasCode(err, errors.PULSE_IMPORT_CATEGORICAL_OVERFLOW) {
		t.Errorf("expected PULSE_IMPORT_CATEGORICAL_OVERFLOW, got: %v", err)
	}
}

func TestCategoricalDictionaryRoundTrip(t *testing.T) {
	d := NewDictionary()
	entries := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	for _, s := range entries {
		d.Add(s)
	}

	var buf bytes.Buffer
	if _, err := d.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}

	d2 := NewDictionary()
	if _, err := d2.ReadFrom(bytes.NewReader(buf.Bytes())); err != nil {
		t.Fatalf("ReadFrom: %v", err)
	}

	if d2.Count() != len(entries) {
		t.Fatalf("Count = %d, want %d", d2.Count(), len(entries))
	}
	for i, s := range entries {
		if got := d2.Resolve(uint32(i)); got != s {
			t.Errorf("Resolve(%d) = %q, want %q", i, got, s)
		}
		id, ok := d2.IDFor(s)
		if !ok {
			t.Errorf("IDFor(%q) not found", s)
		}
		if id != uint32(i) {
			t.Errorf("IDFor(%q) = %d, want %d", s, id, i)
		}
	}
}

func TestCategoricalDictionary_Values(t *testing.T) {
	d := NewDictionary()
	d.Add("a")
	d.Add("b")
	d.Add("c")

	vals := d.Values()
	if len(vals) != 3 {
		t.Fatalf("Values() len = %d, want 3", len(vals))
	}
	// Mutating returned slice should not affect dictionary.
	vals[0] = "z"
	if d.Resolve(0) != "a" {
		t.Error("Values() returned internal slice reference")
	}
}

// uniqueStr generates a unique string for dictionary testing.
func uniqueStr(i int) string {
	b := make([]byte, 4)
	b[0] = byte(i >> 24)
	b[1] = byte(i >> 16)
	b[2] = byte(i >> 8)
	b[3] = byte(i)
	return string(b)
}
