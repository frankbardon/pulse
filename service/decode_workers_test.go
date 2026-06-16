package service

import (
	"testing"

	"github.com/frankbardon/pulse/fs"
)

func TestService_DecodeWorkers_DefaultZero(t *testing.T) {
	cfg := fs.NewMemMap()
	svc := New(cfg)
	if got := svc.DecodeWorkers(); got != 0 {
		t.Fatalf("DecodeWorkers default: got %d, want 0", got)
	}
}

// TestService_DecodeWorkers_RoundTrip verifies SetDecodeWorkers
// installs the configured cap byte-for-byte and that subsequent
// reads return the same value. Mirrors the SetShardWorkers /
// ShardWorkers round-trip pattern.
func TestService_DecodeWorkers_RoundTrip(t *testing.T) {
	cfg := fs.NewMemMap()
	svc := New(cfg)

	cases := []int{0, 1, 2, 4, 8, 32}
	for _, n := range cases {
		svc.SetDecodeWorkers(n)
		if got := svc.DecodeWorkers(); got != n {
			t.Fatalf("DecodeWorkers after SetDecodeWorkers(%d): got %d, want %d", n, got, n)
		}
	}
}

// TestService_DecodeWorkers_IndependentOfShardWorkers verifies the
// two knobs are orthogonal — setting one does not affect the other.
// Guards against accidental aliasing if a future refactor consolidates
// the worker-pool plumbing.
func TestService_DecodeWorkers_IndependentOfShardWorkers(t *testing.T) {
	cfg := fs.NewMemMap()
	svc := New(cfg)

	svc.SetShardWorkers(4)
	svc.SetDecodeWorkers(2)
	if got := svc.ShardWorkers(); got != 4 {
		t.Fatalf("ShardWorkers after independent set: got %d, want 4", got)
	}
	if got := svc.DecodeWorkers(); got != 2 {
		t.Fatalf("DecodeWorkers after independent set: got %d, want 2", got)
	}

	svc.SetDecodeWorkers(0)
	if got := svc.ShardWorkers(); got != 4 {
		t.Fatalf("ShardWorkers unchanged by DecodeWorkers reset: got %d, want 4", got)
	}
	if got := svc.DecodeWorkers(); got != 0 {
		t.Fatalf("DecodeWorkers reset: got %d, want 0", got)
	}
}

// TestParallelDecodeRecordThreshold verifies the threshold constant
// is the documented 100K-record floor. Below this floor the buffered
// Process path stays serial regardless of DecodeWorkers. The
// constant lives at the dispatch site; this test pins the value so
// changes require touching both the constant and the test together.
func TestParallelDecodeRecordThreshold(t *testing.T) {
	const want = 100_000
	if parallelDecodeRecordThreshold != want {
		t.Fatalf("parallelDecodeRecordThreshold: got %d, want %d", parallelDecodeRecordThreshold, want)
	}
}
