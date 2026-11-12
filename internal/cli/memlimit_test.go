package cli

import (
	stderrors "errors"
	"math"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
)

const testRAM = 16 << 30 // 16 GiB

func fixedRAM() (uint64, error) { return testRAM, nil }

// TestParseMemoryLimit: the --memory-limit grammar is GOMEMLIMIT's
// (a byte count with an optional B / KiB / MiB / GiB / TiB suffix) plus
// N% of physical RAM, read through the injected reader.
func TestParseMemoryLimit(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		in   string
		want int64
	}{
		{"14GiB", 14 << 30},
		{"14000MiB", 14000 << 20},
		{"512KiB", 512 << 10},
		{"2TiB", 2 << 40},
		{"4096B", 4096},
		{"4096", 4096},
		{" 1GiB ", 1 << 30},
		{"85%", 14602888806}, // floor(16 GiB × 0.85)
		{"100%", testRAM},
		{"12.5%", 2 << 30},
	} {
		got, err := parseMemoryLimit(c.in, fixedRAM)
		if err != nil || got != c.want {
			t.Errorf("parseMemoryLimit(%q) = %d, %v; want %d", c.in, got, err, c.want)
		}
	}
}

// TestParseMemoryLimit_Refusals: zero, negative, over 100%, a bad unit
// or an overflow is CLI_INPUT carrying {flag, value}; so is a % form
// when physical RAM cannot be read.
func TestParseMemoryLimit_Refusals(t *testing.T) {
	t.Parallel()

	for _, in := range []string{
		"", "0", "0GiB", "-1", "-5GiB", "0%", "-3%", "101%", "100.5%", "NaN%", "Inf%",
		"14GB", "14gib", "14 GiB", "GiB", "lots", "1.5GiB", "%", "9999999TiB",
		"9223372036854775808",
	} {
		_, err := parseMemoryLimit(in, fixedRAM)
		var ce *perrors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perrors.CLI_INPUT ||
			ce.Details["flag"] != flagMemoryLimit || ce.Details["value"] != in {
			t.Errorf("parseMemoryLimit(%q): err = %#v, want CLI_INPUT {flag: %s, value: %q}", in, err, flagMemoryLimit, in)
		}
	}
	noRAM := func() (uint64, error) { return 0, stderrors.New("unsupported") }
	if _, err := parseMemoryLimit("50%", noRAM); err == nil {
		t.Error("50% with an unreadable physical RAM: no error")
	}
	if n, err := parseMemoryLimit("1GiB", noRAM); err != nil || n != 1<<30 {
		t.Errorf("a byte size must not read physical RAM: %d, %v", n, err)
	}
}

// TestPhysicalMemory_ReadsTheHost: the % form's RAM reader works on the
// shipped unix hosts (darwin, linux) and refuses elsewhere.
func TestPhysicalMemory_ReadsTheHost(t *testing.T) {
	t.Parallel()

	n, err := physicalMemory()
	switch runtime.GOOS {
	case "darwin", "linux":
		if err != nil || n < 64<<20 {
			t.Fatalf("physicalMemory() = %d, %v; want the host RAM", n, err)
		}
	default:
		if err == nil {
			t.Fatalf("physicalMemory() on %s = %d, want an unsupported error", runtime.GOOS, n)
		}
	}
}

// restoreMemoryLimit snapshots the process soft memory limit and puts it
// back when the test ends; SetMemoryLimit(-1) reads without changing.
func restoreMemoryLimit(t *testing.T) {
	t.Helper()
	orig := debug.SetMemoryLimit(-1)
	t.Cleanup(func() { debug.SetMemoryLimit(orig) })
}

// TestMemoryLimitFlag_SetsTheRuntimeLimit: --memory-limit sets the Go
// soft memory limit in GlobalBefore, after every other global flag
// validated; it overrides a limit GOMEMLIMIT set at start-up (the same
// runtime knob, simulated here by a prior SetMemoryLimit).
//
// Falsified by deleting the setMemoryLimit call in memoryLimitBefore.
//
// serial: changes the process-global soft memory limit.
func TestMemoryLimitFlag_SetsTheRuntimeLimit(t *testing.T) {
	restoreMemoryLimit(t)

	debug.SetMemoryLimit(3 << 30) // as GOMEMLIMIT=3GiB would
	if _, err := runWorkersBefore(t, "--memory-limit", "1GiB"); err != nil {
		t.Fatal(err)
	}
	if got := debug.SetMemoryLimit(-1); got != 1<<30 {
		t.Fatalf("after --memory-limit 1GiB the soft limit is %d, want %d", got, int64(1<<30))
	}
}

// TestMemoryLimitFlag_UnsetLeavesTheRuntimeAlone: without the flag the
// runtime limit is untouched — no default, no RAM-derived value — and a
// bad value (or another global flag's refusal) never sets it.
//
// Falsified by applying a default when the flag is unset.
//
// serial: reads and changes the process-global soft memory limit.
func TestMemoryLimitFlag_UnsetLeavesTheRuntimeAlone(t *testing.T) {
	restoreMemoryLimit(t)

	const sentinel = 5<<30 + 7
	debug.SetMemoryLimit(sentinel)
	if _, err := runWorkersBefore(t); err != nil {
		t.Fatal(err)
	}
	if got := debug.SetMemoryLimit(-1); got != sentinel {
		t.Fatalf("unset flag: soft limit %d, want the untouched %d", got, int64(sentinel))
	}
	debug.SetMemoryLimit(math.MaxInt64)
	if got := debug.SetMemoryLimit(-1); got != math.MaxInt64 {
		t.Fatalf("reset: %d", got)
	}
	for _, args := range [][]string{
		{"--memory-limit", "101%"},
		{"--memory-limit", "1GiB", "--shard-workers", "-1"},
	} {
		_, err := runWorkersBefore(t, args...)
		if status, _ := ExitStatus(err); status != ExitUsage {
			t.Fatalf("%v: exit status %d (err %v), want %d", args, status, err, ExitUsage)
		}
		if got := debug.SetMemoryLimit(-1); got != math.MaxInt64 {
			t.Errorf("%v: a refused run set the soft limit to %d", args, got)
		}
	}
}

// TestMemoryCeiling (memory-bounds E2-S2): the N% base is physical RAM,
// lowered to a numeric cgroup v2 memory.max below it; "max", a missing
// or unreadable file, junk, or a cgroup limit at or above the RAM leave
// the RAM; an unreadable RAM size is an error even with a cgroup limit.
func TestMemoryCeiling(t *testing.T) {
	t.Parallel()

	const host = 23<<30 + 440<<20 // a 23.4 GiB VM
	ram := func() (uint64, error) { return host, nil }
	file := func(s string) func() ([]byte, error) {
		return func() ([]byte, error) { return []byte(s), nil }
	}
	missing := func() ([]byte, error) { return nil, os.ErrNotExist }
	for _, c := range []struct {
		name   string
		cgroup func() ([]byte, error)
		want   uint64
	}{
		{"16 GiB container", file("17179869184\n"), 16 << 30},
		{"no limit", file("max\n"), host},
		{"no cgroup v2 file", missing, host},
		{"junk", file("lots\n"), host},
		{"empty", file(""), host},
		{"limit above RAM", file("99999999999999\n"), host},
		{"limit equal to RAM", file(strconv.FormatUint(host, 10)), host},
	} {
		got, err := memoryCeiling(ram, c.cgroup)
		if err != nil || got != c.want {
			t.Errorf("%s: memoryCeiling = %d, %v; want %d", c.name, got, err, uint64(c.want))
		}
	}
	noRAM := func() (uint64, error) { return 0, stderrors.New("unsupported") }
	if _, err := memoryCeiling(noRAM, file("17179869184\n")); err == nil {
		t.Error("an unreadable RAM size with a cgroup limit: no error")
	}
}

// TestMemoryLimitPercent_ReadsTheCgroupLimit (memory-bounds E2-S2): the
// N% form the flag parses is a share of the cgroup v2 memory.max when it
// is below the host RAM — 50% inside a 1 GiB cgroup is 512 MiB, not half
// the host. Darwin and linux only (elsewhere the RAM is unreadable and
// the % form refused).
//
// Falsified by wiring physicalMemory back to hostPhysicalMemory.
//
// serial: swaps the package-level cgroupMemoryMax seam.
func TestMemoryLimitPercent_ReadsTheCgroupLimit(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return
	}
	orig := cgroupMemoryMax
	t.Cleanup(func() { cgroupMemoryMax = orig })
	cgroupMemoryMax = func() ([]byte, error) { return []byte("1073741824\n"), nil }
	got, err := parseMemoryLimit("50%", physicalMemory)
	if err != nil || got != 512<<20 {
		t.Fatalf(`parseMemoryLimit("50%%") in a 1 GiB cgroup = %d, %v; want %d`, got, err, int64(512<<20))
	}
}
