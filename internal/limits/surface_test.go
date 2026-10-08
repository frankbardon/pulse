package limits

import (
	stderrors "errors"
	"strings"
	"testing"
	"time"
)

func TestUnitAndDefault(t *testing.T) {
	want := map[Name]string{
		RequestTimeout:     UnitNanoseconds,
		MaxEstimatedMemory: UnitBytes,
		MaxGroups:          UnitCount,
		MaxCrosstabCells:   UnitCount,
		MaxMatrixDim:       UnitCount,
		MaxComposeSlots:    UnitCount,
		MaxChainStages:     UnitCount,
		MaxJoinBuildRows:   UnitCount,
	}
	defs := Defaults()
	for _, n := range Names() {
		if Unit(n) != want[n] {
			t.Errorf("Unit(%s) = %q, want %q", n, Unit(n), want[n])
		}
		if Default(n) != Value(defs, n) {
			t.Errorf("Default(%s) = %d, want %d", n, Default(n), Value(defs, n))
		}
	}
	if Unit("nope") != "" || Default("nope") != 0 {
		t.Error("an unknown name has a unit or default")
	}
}

func TestSet(t *testing.T) {
	var l Limits
	for i, n := range Names() {
		if !Set(&l, n, int64(i+7)) {
			t.Fatalf("Set(%s) refused", n)
		}
		if Value(l, n) != int64(i+7) {
			t.Errorf("Set(%s) did not store the value", n)
		}
	}
	before := l
	if Set(&l, "nope", 1) || l != before {
		t.Error("Set of an unknown name changed the limits")
	}
}

func TestParseName(t *testing.T) {
	for _, n := range Names() {
		if got, err := ParseName(string(n)); err != nil || got != n {
			t.Errorf("ParseName(%s) = %q, %v", n, got, err)
		}
	}
	if _, err := ParseName("MaxGroups"); !stderrors.Is(err, ErrUnknownName) {
		t.Errorf("ParseName(Go spelling) err = %v, want ErrUnknownName", err)
	}
}

func TestParseValue(t *testing.T) {
	good := []struct {
		n    Name
		in   string
		want int64
	}{
		{RequestTimeout, "30s", int64(30 * time.Second)},
		{RequestTimeout, "2m", int64(2 * time.Minute)},
		{RequestTimeout, "unlimited", Unlimited},
		{RequestTimeout, "-1", Unlimited},
		{RequestTimeout, "0", 0},
		{MaxGroups, "1000000", 1_000_000},
		{MaxGroups, "10_000_000", 10_000_000},
		{MaxGroups, "unlimited", Unlimited},
		{MaxGroups, "-1", Unlimited},
		{MaxGroups, "0", 0},
		{MaxEstimatedMemory, " 4096 ", 4096},
	}
	for _, c := range good {
		got, err := ParseValue(c.n, c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseValue(%s, %q) = %d, %v; want %d", c.n, c.in, got, err, c.want)
		}
	}
	bad := []struct {
		n  Name
		in string
	}{
		{RequestTimeout, "30"},
		{RequestTimeout, "soon"},
		{RequestTimeout, "-5s"},
		{RequestTimeout, ""},
		{MaxGroups, "1e6"},
		{MaxGroups, "30s"},
		{MaxGroups, "-2"},
		{MaxGroups, "lots"},
		{MaxGroups, ""},
		{"nope", "1"},
	}
	for _, c := range bad {
		if v, err := ParseValue(c.n, c.in); err == nil {
			t.Errorf("ParseValue(%s, %q) = %d, want a refusal", c.n, c.in, v)
		}
	}
}

func TestSpell_RoundTrips(t *testing.T) {
	l := Defaults()
	l.RequestTimeout = 90 * time.Second
	for _, n := range Names() {
		v := Value(l, n)
		s := Spell(n, v)
		back, err := ParseValue(n, s)
		if err != nil || back != v {
			t.Errorf("Spell(%s, %d) = %q does not round-trip: %d, %v", n, v, s, back, err)
		}
	}
	if Spell(MaxGroups, Unlimited) != "unlimited" || Spell(RequestTimeout, Unlimited) != "unlimited" {
		t.Error("Unlimited is not spelled \"unlimited\"")
	}
	if Spell(RequestTimeout, int64(30*time.Second)) != "30s" {
		t.Errorf("Spell(request_timeout, 30s) = %q", Spell(RequestTimeout, int64(30*time.Second)))
	}
}

// TestDigest: the digest is a function of every effective value — any
// single-field change moves it, equal limits share it.
func TestDigest(t *testing.T) {
	base := Digest(Defaults())
	if !strings.HasPrefix(base, DigestPrefix) || len(base) != len(DigestPrefix)+64 {
		t.Fatalf("Digest = %q", base)
	}
	if Digest(Defaults()) != base {
		t.Fatal("Digest is not deterministic")
	}
	seen := map[string]Name{base: ""}
	for _, n := range Names() {
		l := Defaults()
		Set(&l, n, Value(l, n)+12345)
		d := Digest(l)
		if prev, dup := seen[d]; dup {
			t.Errorf("changing %s gives the digest of %q", n, prev)
		}
		seen[d] = n
	}
}
