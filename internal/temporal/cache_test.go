package temporal

import (
	stderrors "errors"
	"sync"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
)

func TestCache_LoadReturnsSamePointer(t *testing.T) {
	var c Cache
	a, err := c.Load("Europe/Berlin")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	b, err := c.Load("Europe/Berlin")
	if err != nil {
		t.Fatalf("Load (second): %v", err)
	}
	if a != b {
		t.Fatalf("second Load returned a different *Zone; cache did not memoise")
	}
	if a.Name() != "Europe/Berlin" {
		t.Fatalf("Name = %q, want Europe/Berlin", a.Name())
	}
}

func TestCache_UTCIsSentinel(t *testing.T) {
	var c Cache
	z, err := c.Load("UTC")
	if err != nil {
		t.Fatalf("Load(UTC): %v", err)
	}
	if z != UTC {
		t.Fatalf("Load(UTC) did not return the UTC sentinel")
	}
}

func TestCache_UnknownIsCodedAndNotCached(t *testing.T) {
	var c Cache
	for i := 0; i < 2; i++ {
		_, err := c.Load("EST")
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_TIMEZONE_UNKNOWN {
			t.Fatalf("call %d: err = %v, want PULSE_TIMEZONE_UNKNOWN", i, err)
		}
	}
	if _, ok := c.zones.Load("EST"); ok {
		t.Fatalf("a refused name was cached")
	}
}

func TestCache_ConcurrentLoadsAgree(t *testing.T) {
	var c Cache
	const n = 16
	got := make([]*Zone, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			z, err := c.Load("America/New_York")
			if err != nil {
				t.Errorf("Load: %v", err)
				return
			}
			got[i] = z
		}(i)
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if got[i] != got[0] {
			t.Fatalf("goroutine %d observed a different *Zone", i)
		}
	}
}
