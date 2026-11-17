package sweep

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// target_test.go — a sweep axis `target: "params.<name>"` (#521): the
// expander writes each slot's axis value into the slot request's
// params, overriding a declared value and declaring an absent one; the
// axis needs no placeholder; the refusals are target_invalid,
// target_duplicate, axis_target_conflict and (item for_each)
// target_unsupported.

// targetBody is a sweep request body whose filter reads params.cut.
const targetBody = `{"cohort":{"filename":"c.pulse"},%s"filterers":[{"type":"FILTER_EXPRESSION","expression":"x >= params.cut"}]}`

func targetSpec(t *testing.T, axes, extra string) *types.ComposedRequest {
	t.Helper()
	return decodeComposed(t, `{"sweep":{"axes":[`+axes+`],"request":`+fmt.Sprintf(targetBody, extra)+`}}`)
}

// TestExpand_TargetWritesParams: every slot carries its axis value as
// params.cut — the wire value's own json.Number — declared when the
// body has no params, overriding the body's own params.cut while its
// other keys stay; an untargeted axis beside it still splices.
func TestExpand_TargetWritesParams(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name, extra string
		keep        map[string]any
	}{
		{"declares", ``, nil},
		{"overrides", `"params":{"cut":0,"w":"a"},`, map[string]any{"w": "a"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			exp := mustExpand(t, targetSpec(t, `{"name":"cut","values":[5,"7.50"],"target":"params.cut"}`, c.extra))
			reqs := exp.Composed.Requests
			if len(reqs) != 2 {
				t.Fatalf("slots = %d, want 2", len(reqs))
			}
			for i, want := range []any{num("5"), "7.50"} {
				if got := reqs[i].Params["cut"]; got != want {
					t.Fatalf("slot %d params.cut = %#v, want %#v", i, got, want)
				}
				for k, v := range c.keep {
					if reqs[i].Params[k] != v {
						t.Fatalf("slot %d params.%s = %#v, want %#v", i, k, reqs[i].Params[k], v)
					}
				}
				if len(reqs[i].Params) != 1+len(c.keep) {
					t.Fatalf("slot %d params = %v", i, reqs[i].Params)
				}
			}
			if reqs[0].Params == nil || fmt.Sprintf("%p", reqs[0].Params) == fmt.Sprintf("%p", reqs[1].Params) {
				t.Fatal("slots share one params map")
			}
			if fmt.Sprint(exp.Labels) != "[cut=5 cut=7.50]" {
				t.Fatalf("labels = %v", exp.Labels)
			}
		})
	}

	// An untargeted axis beside a targeted one keeps the $var route.
	exp := mustExpand(t, decodeComposed(t, `{"sweep":{"axes":[
		{"name":"cut","values":[1,2],"target":"params.cut"},
		{"name":"zone","values":["UTC"]}],
		"request":`+fmt.Sprintf(targetBody, `"time_zone":{"$var":"zone"},`)+`}}`))
	for i, r := range exp.Composed.Requests {
		if r.TimeZone != "UTC" || r.Params["cut"] != num(fmt.Sprint(i+1)) {
			t.Fatalf("slot %d: tz %q params %v", i, r.TimeZone, r.Params)
		}
	}
}

// TestExpand_TargetGoValues: a Go caller's axis scalars land in params
// as the values themselves (params normalizes them as their JSON text).
func TestExpand_TargetGoValues(t *testing.T) {
	t.Parallel()

	c := &types.ComposedRequest{Sweep: &types.SweepSpec{
		Axes:    []types.SweepAxis{{Name: "cut", Values: []any{3, 0.25, true}, Target: "params.cut"}},
		Request: json.RawMessage(fmt.Sprintf(targetBody, "")),
	}}
	exp := mustExpand(t, c)
	for i, want := range []any{3, 0.25, true} {
		if got := exp.Composed.Requests[i].Params["cut"]; got != want {
			t.Fatalf("slot %d params.cut = %#v, want %#v", i, got, want)
		}
	}
}

// TestExpand_TargetRefusals: each malformed or conflicting target is
// refused before any slot renders, located at the faulting key.
func TestExpand_TargetRefusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, axes, extra, reason, field string
	}{
		{"no prefix", `{"name":"cut","values":[1],"target":"cut"}`, ``, ReasonTargetInvalid, "sweep.axes[0].target"},
		{"empty name", `{"name":"cut","values":[1],"target":"params."}`, ``, ReasonTargetInvalid, "sweep.axes[0].target"},
		{"nested name", `{"name":"cut","values":[1],"target":"params.a.b"}`, ``, ReasonTargetInvalid, "sweep.axes[0].target"},
		{"duplicate", `{"name":"cut","values":[1],"target":"params.cut"},{"name":"c2","values":[2],"target":"params.cut"}`, ``,
			ReasonTargetDuplicate, "sweep.axes[1].target"},
		{"marker on target", `{"name":"cut","values":[1],"target":"params.cut"}`, `"params":{"cut":{"$var":"cut"}},`,
			ReasonAxisTargetConflict, "sweep.request.params.cut"},
		{"marker elsewhere", `{"name":"cut","values":["UTC"],"target":"params.cut"}`, `"time_zone":{"$var":"cut"},`,
			ReasonAxisTargetConflict, "sweep.request.time_zone"},
		{"declared marker of another axis", `{"name":"cut","values":[1],"target":"params.cut"},{"name":"k","values":[2]}`,
			`"params":{"cut":{"$var":"k"}},`, ReasonAxisTargetConflict, "sweep.request.params.cut"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Expand(targetSpec(t, c.axes, c.extra), Options{}, func(int) error {
				t.Fatal("preflight ran before the target check")
				return nil
			})
			ce := requireSweepInvalid(t, err, c.reason)
			if ce.Details["field"] != c.field {
				t.Fatalf("field = %v, want %s (%s)", ce.Details["field"], c.field, ce.Message)
			}
		})
	}

	// A marker in the overlays naming a targeted axis conflicts too.
	c := targetSpec(t, `{"name":"cut","values":[1],"target":"params.cut"}`, ``)
	c.Sweep.Overlays = json.RawMessage(`[{"kind":"OVERLAY_FORMULA","params":{"formula":{"$var":"cut"}}}]`)
	_, err := Expand(c, Options{}, nil)
	if ce := requireSweepInvalid(t, err, ReasonAxisTargetConflict); ce.Details["field"] != "sweep.overlays[0].params.formula" {
		t.Fatalf("field = %v", ce.Details["field"])
	}
}

// TestExpandItems_TargetUnsupported: an item for_each axis carrying a
// target is refused — params are request-wide.
func TestExpandItems_TargetUnsupported(t *testing.T) {
	t.Parallel()

	r := decodeRequest(t, `{"windows":[{"type":"WIN_LAG","field":"x","label":"l_{{k}}","params":{"offset":{"$var":"k"}},
		"for_each":{"axes":[{"name":"k","values":[1,2],"target":"params.k"}]}}]}`)
	_, _, err := ExpandItems(r, Options{}, nil)
	requireForEachInvalid(t, err, ReasonTargetUnsupported, "windows[0].for_each.axes[0].target")
}
