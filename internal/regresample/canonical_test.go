package regresample

import "testing"

// TestCanonicalJSON: key order (at any depth) and whitespace vanish,
// numbers and strings keep their text, array order is kept, and a value
// change still shows. Malformed or empty input comes back as given.
func TestCanonicalJSON(t *testing.T) {
	a := CanonicalJSON([]byte(`{"b":{"y":[3, {"q":1,"p":"{{x}}"}],"x":0.10},"a":{"$var":"k"}, "n":12345678901234567890}`))
	b := CanonicalJSON([]byte(` { "n" : 12345678901234567890, "a":{"$var":"k"},"b":{"x":0.10,"y":[3,{"p":"{{x}}","q":1}]}}`))
	want := `{"a":{"$var":"k"},"b":{"x":0.10,"y":[3,{"p":"{{x}}","q":1}]},"n":12345678901234567890}`
	if string(a) != want || string(b) != want {
		t.Fatalf("canonical forms\n got %s\n     %s\nwant %s", a, b, want)
	}
	if got := string(CanonicalJSON([]byte(`[2, 1]`))); got != `[2,1]` {
		t.Fatalf("array order moved: %s", got)
	}
	if string(CanonicalJSON([]byte(`{"x":0.1}`))) == string(CanonicalJSON([]byte(`{"x":0.10}`))) {
		t.Fatal("number text must survive (no float round-trip)")
	}
	for _, in := range []string{``, `{"a":`, `nope`} {
		if got := string(CanonicalJSON([]byte(in))); got != in {
			t.Fatalf("%q came back as %q", in, got)
		}
	}
	if CanonicalJSON(nil) != nil {
		t.Fatal("nil must stay nil (an omitempty raw slot stays absent)")
	}
}
