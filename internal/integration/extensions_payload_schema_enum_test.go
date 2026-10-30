package integration_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/spf13/afero"
)

// payloadEnumWith returns the enum of the payload-schema def whose enum
// lists anchor (a built-in operator name of the category), nil when no
// def lists it.
func payloadEnumWith(t *testing.T, p *pulse.Pulse, anchor string) []string {
	t.Helper()
	raw, err := p.PayloadSchema()
	if err != nil {
		t.Fatalf("PayloadSchema: %v", err)
	}
	var schema struct {
		Defs map[string]struct {
			Enum []string `json:"enum"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	for _, d := range schema.Defs {
		if slices.Contains(d.Enum, anchor) {
			return d.Enum
		}
	}
	return nil
}

// TestExtensions_PayloadSchemaEnumsCarryExtensions: the instance
// payload schema (Pulse.PayloadSchema, `pulse schema`) lists every
// registered extension operator in its category's operator-type enum
// beside the built-ins — aggregators, attributes, filterers, groupers,
// windows, features, tests and regressions as well as matrix operators — so a
// request naming one validates against the instance's own schema. A
// feature profile that leaves an extension out drops it from the enum.
func TestExtensions_PayloadSchemaEnumsCarryExtensions(t *testing.T) {
	t.Parallel()

	categories := []struct {
		anchor string
		names  func(*descriptor.ExtensionsManifest) []descriptor.OperatorMeta
	}{
		{"AGG_COUNT", func(m *descriptor.ExtensionsManifest) []descriptor.OperatorMeta { return m.Aggregators }},
		{"ATTR_ZSCORE", func(m *descriptor.ExtensionsManifest) []descriptor.OperatorMeta { return m.Attributes }},
		{"FILTER_INCLUDE", func(m *descriptor.ExtensionsManifest) []descriptor.OperatorMeta { return m.Filterers }},
		{"GROUP_CATEGORY", func(m *descriptor.ExtensionsManifest) []descriptor.OperatorMeta { return m.Groupers }},
		{"WIN_LAG", func(m *descriptor.ExtensionsManifest) []descriptor.OperatorMeta { return m.Windows }},
		{"FEAT_LOG", func(m *descriptor.ExtensionsManifest) []descriptor.OperatorMeta { return m.Features }},
		{"TEST_T", func(m *descriptor.ExtensionsManifest) []descriptor.OperatorMeta { return m.Tests }},
	}
	ctx := context.Background()
	covered := map[string]bool{}
	for _, s := range paritySuites() {
		t.Run(s.name, func(t *testing.T) {
			p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: s.register(&parityProbe{})})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			m := p.Manifest(ctx)
			for _, c := range categories {
				ops := c.names(&m.Extensions)
				if len(ops) == 0 {
					continue
				}
				enum := payloadEnumWith(t, p, c.anchor)
				if enum == nil {
					t.Fatalf("no payload-schema enum lists %s", c.anchor)
				}
				for _, op := range ops {
					if !slices.Contains(enum, op.Name) {
						t.Errorf("payload-schema enum beside %s lacks extension %s", c.anchor, op.Name)
					}
					covered[c.anchor] = true
				}
				if !slices.IsSorted(enum) {
					t.Errorf("enum beside %s is not sorted: %v", c.anchor, enum)
				}
			}
			// Extension REG_* models ride the manifest in the built-in
			// RegressionMeta shape.
			if regs := m.Extensions.Regressions; len(regs) > 0 {
				enum := payloadEnumWith(t, p, "REG_OLS")
				for _, r := range regs {
					if !slices.Contains(enum, r.Name) {
						t.Errorf("payload-schema enum beside REG_OLS lacks extension %s", r.Name)
					}
					covered["REG_OLS"] = true
				}
				if !slices.IsSorted(enum) {
					t.Errorf("enum beside REG_OLS is not sorted: %v", enum)
				}
			}
		})
	}
	if !covered["REG_OLS"] {
		t.Error("no parity suite registers an extension regression — the check is vacuous for it")
	}
	for _, c := range categories {
		if !covered[c.anchor] {
			t.Errorf("no parity suite registers an extension beside %s — the check is vacuous for it", c.anchor)
		}
	}

	// A profile that leaves the aggregator extension out hides it from
	// the enum, like the manifest.
	s := paritySuites()[0]
	scoped, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: s.register(&parityProbe{}),
		FeatureProfile: &pulse.FeatureProfile{Features: []string{"capability:process", "AGG_COUNT"}}})
	if err != nil {
		t.Fatalf("New (scoped): %v", err)
	}
	enum := payloadEnumWith(t, scoped, "AGG_COUNT")
	for _, op := range s.register(&parityProbe{}).Aggregators {
		if slices.Contains(enum, string(op.Name)) {
			t.Errorf("profile-hidden extension %s still in the AggregationType enum %v", op.Name, enum)
		}
	}
}
