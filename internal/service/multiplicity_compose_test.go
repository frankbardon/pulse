package service

import (
	"bytes"
	"context"
	"testing"

	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing/multiplicity"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// multiplicity_compose_test.go covers the Compose barrier fold (U13,
// E3-S1): one fold after every slot and every Compose-host layer, a
// `compose` family pooled across slots and Compose-host layers, each
// slot's own families corrected inside the slot, and Compose and
// ComposeParallel answering byte-identically.

// composeMultService writes three cohorts with distinct record offsets
// and associations so every slot's p-values differ.
func composeMultService(t *testing.T) *Service {
	t.Helper()
	cfg := fs.NewMemMap()
	for i, name := range []string{"m0.pulse", "m1.pulse", "m2.pulse"} {
		rows := multRows(80+10*i, 7*i)
		if i == 1 {
			// Slot 1 ties h to g on every third record more, so its cell
			// shares move away from slot 0's and the Compose-host
			// proportion test has small p-values to correct.
			for k, rec := range rows {
				if k%3 == 0 {
					rec[2] = rec[1]
				}
			}
		}
		data := writeNullablePulse(t, multSchema(), rows, nil)
		if err := afero.WriteFile(cfg.Fs(), name, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return New(cfg)
}

// composeMultRequest is a three-slot batch: every slot a g×h crosstab
// carrying tests, post-tests-free request-host overlays (SCALAR, SERIES
// and MATRIX p-sites plus a descriptive layer), and a Compose-host
// cell overlay comparing slot 0 against slot 1.
func composeMultRequest() *types.ComposedRequest {
	var slots []*types.Request
	for i, name := range []string{"m0.pulse", "m1.pulse", "m2.pulse"} {
		r := overlayCrosstabRequest(true)
		r.Label = "s" + string(rune('0'+i))
		r.Cohort = &types.Cohort{Filename: name}
		slots = append(slots, r)
	}
	return &types.ComposedRequest{
		Requests: slots,
		Overlays: []types.ComposeOverlaySpec{{
			Name: "pz", Kind: types.OverlayKindPropZCell, Scope: types.OverlayScopeCell,
			Reference: "s0", Targets: []string{"s1"},
		}},
	}
}

// composePSite is one p-value a composed response carries, located by
// owner (slot index, -1 for a Compose-host layer) and layer (-1 for a
// test or post-test).
type composePSite struct {
	slot, layer int
	raw         float64
	adj         *float64
	m           int // the echoed family size; 0 when uncorrected
}

// composePSites walks every p-site of the fixture's shapes in fold
// collection order: per slot its tests, post-tests and inferential
// layers, then the Compose-host layers.
func composePSites(t *testing.T, out *types.ComposedResponse) []composePSite {
	t.Helper()
	var sites []composePSite
	layerSites := func(slot int, layers []types.OverlayLayer) {
		for li := range layers {
			l := &layers[li]
			m := 0
			if l.Multiplicity != nil {
				m = l.Multiplicity.M
			}
			add := func(raw float64, adj *float64) {
				sites = append(sites, composePSite{slot: slot, layer: li, raw: raw, adj: adj, m: m})
			}
			switch {
			case l.Summary != nil && l.Summary.PValue != nil:
				add(*l.Summary.PValue, l.Summary.PAdjusted)
			case l.Payload.Series != nil && l.Kind == types.OverlayKindChiSqRow:
				for _, e := range l.Payload.Series.Entries {
					add(*e.Summary.PValue, e.Summary.PAdjusted)
				}
			case l.Payload.Matrix != nil && (l.Kind == types.OverlayKindFisherExactCell || l.Kind == types.OverlayKindPropZCell):
				for r, row := range l.Payload.Matrix.Cells {
					for c, cl := range row {
						if !cl.Present {
							continue
						}
						var adj *float64
						if l.Payload.PAdjusted != nil {
							if v, ok := l.Payload.PAdjusted.Cells[r][c].Value.(float64); ok {
								adj = &v
							}
						}
						add(cl.Value.(float64), adj)
					}
				}
			}
		}
	}
	for i, resp := range out.Responses {
		for _, r := range append(append([]*types.TestResult{}, resp.Tests...), resp.PostTests...) {
			m := 0
			if r.Multiplicity != nil {
				m = r.Multiplicity.M
			}
			sites = append(sites, composePSite{slot: i, layer: -1, raw: r.PValue, adj: r.PAdjusted, m: m})
		}
		layerSites(i, resp.Overlays)
	}
	layerSites(-1, out.Overlays)
	return sites
}

// TestMultiplicityFold_Compose runs the barrier fold over a real
// three-slot batch with a Compose-host layer: each case's expected
// families (keyed per site) are corrected by the core over the
// baseline's raw p-values, raw figures stay untouched, and Compose and
// ComposeParallel answer byte-identically.
func TestMultiplicityFold_Compose(t *testing.T) {
	svc := composeMultService(t)
	ctx := context.Background()
	holm, bh := types.MultiplicityMethodHolm, types.MultiplicityMethodBH
	base, err := svc.Compose(ctx, composeMultRequest())
	if err != nil {
		t.Fatal(err)
	}
	baseSites := composePSites(t, base)
	if len(base.Overlays) != 1 {
		t.Fatalf("fixture has %d Compose-host layers", len(base.Overlays))
	}
	hostSites := 0
	for _, s := range baseSites {
		if s.adj != nil || s.m != 0 {
			t.Fatal("baseline corrected without a block")
		}
		if s.slot < 0 {
			hostSites++
		}
	}
	if hostSites < 2 {
		t.Fatalf("Compose-host layer yields %d p-values; the fixture proves nothing", hostSites)
	}

	slotKey := func(s composePSite) string {
		return string(rune('0' + s.slot))
	}
	layerKey := func(s composePSite) string {
		if s.layer < 0 {
			return slotKey(s) + "/request"
		}
		return slotKey(s) + "/layer" + string(rune('0'+s.layer))
	}
	for _, c := range []struct {
		name   string
		mutate func(*types.ComposedRequest)
		// key names each site's expected family; "" = uncorrected.
		key    func(composePSite) string
		method func(composePSite) types.MultiplicityMethod
	}{
		{
			name: "compose pools every slot and the compose-host layer",
			mutate: func(r *types.ComposedRequest) {
				r.Multiplicity = &types.Multiplicity{Method: holm, Family: types.MultiplicityFamilyCompose}
			},
			key:    func(composePSite) string { return "compose" },
			method: func(composePSite) types.MultiplicityMethod { return holm },
		},
		{
			name: "request stays per slot, compose-host layer falls back to layer",
			mutate: func(r *types.ComposedRequest) {
				r.Multiplicity = &types.Multiplicity{Method: holm, Family: types.MultiplicityFamilyRequest}
			},
			key: func(s composePSite) string {
				if s.slot < 0 {
					return "host/layer"
				}
				return slotKey(s) + "/request"
			},
			method: func(composePSite) types.MultiplicityMethod { return holm },
		},
		{
			name:   "layer stays per slot layer",
			mutate: func(r *types.ComposedRequest) { r.Multiplicity = &types.Multiplicity{Method: bh} },
			key: func(s composePSite) string {
				if s.slot < 0 {
					return "host/layer"
				}
				return layerKey(s)
			},
			method: func(composePSite) types.MultiplicityMethod { return bh },
		},
		{
			name: "a slot's own request family sits beside the compose family",
			mutate: func(r *types.ComposedRequest) {
				r.Multiplicity = &types.Multiplicity{Method: bh, Family: types.MultiplicityFamilyCompose}
				r.Requests[1].Multiplicity = &types.Multiplicity{Method: holm, Family: types.MultiplicityFamilyRequest}
			},
			key: func(s composePSite) string {
				if s.slot == 1 {
					return "1/request"
				}
				return "compose"
			},
			method: func(s composePSite) types.MultiplicityMethod {
				if s.slot == 1 {
					return holm
				}
				return bh
			},
		},
		{
			name: "only the compose-host layer corrected",
			mutate: func(r *types.ComposedRequest) {
				r.Overlays[0].Multiplicity = &types.Multiplicity{Method: holm, Family: types.MultiplicityFamilyCompose}
			},
			key: func(s composePSite) string {
				if s.slot < 0 {
					return "compose"
				}
				return ""
			},
			method: func(composePSite) types.MultiplicityMethod { return holm },
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := composeMultRequest()
			c.mutate(req)
			got, err := svc.Compose(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			sites := composePSites(t, got)
			if len(sites) != len(baseSites) {
				t.Fatalf("%d p-sites, baseline %d", len(sites), len(baseSites))
			}
			// Expected: the core over each family's baseline raw p-values,
			// in collection order.
			order := []string{}
			pools := map[string][]int{}
			for i, s := range baseSites {
				k := c.key(s)
				if k == "" {
					continue
				}
				if _, ok := pools[k]; !ok {
					order = append(order, k)
				}
				pools[k] = append(pools[k], i)
			}
			want := make([]*float64, len(sites))
			wantM := make([]int, len(sites))
			moved := false
			for _, k := range order {
				idx := pools[k]
				ps := make([]float64, len(idx))
				for j, i := range idx {
					ps[j] = baseSites[i].raw
				}
				adj, err := multiplicity.Adjust(multiplicity.Method(c.method(baseSites[idx[0]])), ps)
				if err != nil {
					t.Fatal(err)
				}
				m := multiplicity.FamilySize(ps)
				for j, i := range idx {
					want[i] = &adj[j]
					wantM[i] = m
				}
			}
			for i, s := range sites {
				if !sameFloat(s.raw, baseSites[i].raw) {
					t.Errorf("site %d raw p %v changed (was %v)", i, s.raw, baseSites[i].raw)
				}
				if want[i] == nil {
					if s.adj != nil {
						t.Errorf("site %d (slot %d layer %d) corrected outside its family", i, s.slot, s.layer)
					}
					continue
				}
				if s.adj == nil {
					t.Fatalf("site %d (slot %d layer %d) uncorrected", i, s.slot, s.layer)
				}
				if !sameFloat(*s.adj, *want[i]) {
					t.Errorf("site %d (slot %d layer %d) p_adjusted %v, want %v", i, s.slot, s.layer, *s.adj, *want[i])
				}
				if s.m != wantM[i] {
					t.Errorf("site %d (slot %d layer %d) m=%d, want %d", i, s.slot, s.layer, s.m, wantM[i])
				}
				moved = moved || !sameFloat(*s.adj, s.raw)
			}
			if !moved {
				t.Fatal("no adjusted p differs from its raw one; the case proves nothing")
			}

			// Serial and parallel answer byte-identically.
			preq := composeMultRequest()
			c.mutate(preq)
			pgot, err := svc.ComposeParallel(ctx, preq, ComposeOptions{MaxWorkers: 3, FailFast: true})
			if err != nil {
				t.Fatal(err)
			}
			if a, b := mustMarshal(t, got), mustMarshal(t, pgot); !bytes.Equal(a, b) {
				t.Errorf("Compose and ComposeParallel differ:\n serial   %s\n parallel %s", a, b)
			}
		})
	}
}

// TestMultiplicityFold_ComposeNoneIsIdentity: a batch with no block,
// and one whose every block resolves to `none`, answer byte-identically
// — serial and parallel.
func TestMultiplicityFold_ComposeNoneIsIdentity(t *testing.T) {
	svc := composeMultService(t)
	ctx := context.Background()
	base, err := svc.Compose(ctx, composeMultRequest())
	if err != nil {
		t.Fatal(err)
	}
	want := mustMarshal(t, base)
	none := &types.Multiplicity{Method: types.MultiplicityMethodNone, Family: types.MultiplicityFamilyCompose}
	for name, mk := range map[string]func() *types.ComposedRequest{
		"absent": composeMultRequest,
		"none": func() *types.ComposedRequest {
			r := composeMultRequest()
			r.Multiplicity = none
			return r
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := svc.Compose(ctx, mk())
			if err != nil {
				t.Fatal(err)
			}
			if b := mustMarshal(t, got); !bytes.Equal(want, b) {
				t.Errorf("serial output changed:\n%s\n%s", want, b)
			}
			pgot, err := svc.ComposeParallel(ctx, mk(), ComposeOptions{MaxWorkers: 3})
			if err != nil {
				t.Fatal(err)
			}
			if b := mustMarshal(t, pgot); !bytes.Equal(want, b) {
				t.Errorf("parallel output changed:\n%s\n%s", want, b)
			}
		})
	}
}
