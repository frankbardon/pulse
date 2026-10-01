package descriptor

import (
	"sort"
	"sync"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/buildinfo"
	"github.com/frankbardon/pulse/internal/examples"
	"github.com/frankbardon/pulse/internal/skills"
)

// operations returns the library-only entry points that do not back a
// CLI leaf. They carry the same CommandAnnotations as commands so the
// manifest exposes a uniform shape across CLI and library surfaces.
func operations() []descriptor.Command {
	return []descriptor.Command{
		{Name: "filter_to_file", Description: "Filter a cohort and write the result to a deterministic .pulse output", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: true}},
		{Name: "process_stream", Description: "Execute a processing request and return a structured StreamResult of incremental rows", Annotations: descriptor.CommandAnnotations{Streamable: true, Deterministic: true, Expensive: true}},
		{Name: "synth_stream", Description: "Generate synthetic respondents incrementally via a structured StreamResult", Annotations: descriptor.CommandAnnotations{Streamable: true, Deterministic: false, Expensive: true}},
		{Name: "watch", Description: "Observe .pulse file changes and emit ChangeEvent records", Annotations: descriptor.CommandAnnotations{Streamable: true, Deterministic: false, Expensive: false}},
	}
}

// commands returns the default set of CLI leaf commands. Each entry
// carries its capability annotations (streamable / deterministic /
// expensive) so manifest consumers can decide whether to cache.
func commands() []descriptor.Command {
	return []descriptor.Command{
		{Name: "process", Description: "Execute a processing request against a cohort", Annotations: descriptor.CommandAnnotations{Streamable: true, Deterministic: true, Expensive: true}},
		{Name: "compose", Description: "Execute multiple processing requests in batch", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: true}},
		{Name: "process-chain", Description: "Execute a source-rooted linear chain of mergeable processing stages", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: true}},
		{Name: "sample", Description: "Return sample rows from a cohort", Annotations: descriptor.CommandAnnotations{Streamable: true, Deterministic: false, Expensive: false}},
		{Name: "facet", Description: "Return distinct values for a field", Annotations: descriptor.CommandAnnotations{Streamable: true, Deterministic: true, Expensive: false}},
		{Name: "lookup", Description: "Resolve a point lookup (single-key or composite) against a cohort's prebuilt sidecar index", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: false}},
		{Name: "inspect", Description: "Inspect a .pulse file header and schema", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: false}},
		{Name: "predict", Description: "Validate a request without executing", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: false}},
		{Name: "manifest", Description: "Output the root manifest", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: false}},
		{Name: "schema", Description: "Output the JSON Schema for request/response payloads", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: false}},
		{Name: "mcp", Description: "Serve the Model Context Protocol over stdio", Annotations: descriptor.CommandAnnotations{Streamable: true, Deterministic: false, Expensive: false}},
		{Name: "synth", Description: "Generate synthetic .pulse cohorts from a schema or profile", Annotations: descriptor.CommandAnnotations{Streamable: true, Deterministic: false, Expensive: true}},
		{Name: "profile", Description: "Capture statistical summaries of cohorts for synthesis", Annotations: descriptor.CommandAnnotations{Streamable: true, Deterministic: true, Expensive: true}},
		{Name: "shard create", Description: "Create a new shard archive from one or more single-file .pulse shards", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: true}},
		{Name: "shard add", Description: "Append a shard to an existing archive", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: false}},
		{Name: "shard remove", Description: "Remove a shard from an archive by basename", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: false}},
		{Name: "shard list", Description: "List shards inside an archive", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: false}},
		{Name: "shard compact", Description: "Rewrite a shard archive to reclaim orphan bytes and refresh canonical metadata", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: true}},
		{Name: "shard verify", Description: "Re-validate every shard's header + cohesion against the canonical schema", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: true}},
		{Name: "shard extract", Description: "Extract a shard's standalone .pulse bytes to stdout", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: false}},
		{Name: "index build", Description: "Build a point-lookup sidecar index for one or more key columns", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: true}},
		{Name: "index list", Description: "List every sidecar point-lookup index built for a cohort", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: false}},
		{Name: "index verify", Description: "Report whether a cohort's sidecar point-lookup index is still fresh", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: false}},
		{Name: "index drop", Description: "Remove a cohort's sidecar point-lookup index", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: false}},
		{Name: "widen", Description: "Widen a set column of a single-file cohort to a wider set rung, rewriting the cohort in place (destructive, non-interactive, atomic)", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: true}},
		{Name: "dedup", Description: "Deduplicate an existing single-file cohort's repeated parent blocks into parent groups (format 0x02), in place or to a new path (destructive in place, non-interactive, atomic)", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: true}},
		{Name: "version", Description: "Print the Pulse build version (--json adds Go version, commit and envelope format_version)", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: false}},
	}
}

// rawCohortFieldTypes returns the bare (name, categorical) tuples for
// every defined field type. Compatible* cross-refs are computed by
// cohortFieldTypes() from the operator capability tables.
//
// The walk is bounded by FieldType.IsKnown() (which compares against
// encoding's own fieldTypeCount sentinel) rather than a literal count.
// A hardcoded bound silently drops any newly registered field type from
// the manifest's cohort_types block — exactly what happened to
// `datetime` when it was appended at type byte 17 — so the loop derives
// its end from the registry instead. FieldType is a byte, hence the 256
// ceiling.
func rawCohortFieldTypes() []descriptor.CohortFieldType {
	var out []descriptor.CohortFieldType
	for i := range 256 {
		ft := encoding.FieldType(i)
		if !ft.IsKnown() {
			break
		}
		name := ft.String()
		if len(name) > 7 && name[:7] == "unknown" {
			continue
		}
		out = append(out, descriptor.CohortFieldType{
			Name:           name,
			Categorical:    ft.IsCategorical(),
			ShardedCapable: true,
		})
	}
	return out
}

// cohortFieldTypes returns CohortFieldType descriptors enriched with
// Compatible* cross-references derived deterministically from the
// per-operator AcceptsTypes declarations.
func cohortFieldTypes() []descriptor.CohortFieldType {
	base := rawCohortFieldTypes()
	aggs := aggregatorCapabilities()
	attrs := attributeCapabilities()
	filts := filtererCapabilities()
	grps := grouperCapabilities()
	wins := windowCapabilities()
	feats := featureCapabilities()

	indexByType := func(ops []descriptor.Operator) map[string][]string {
		m := make(map[string][]string)
		for _, op := range ops {
			for _, t := range op.AcceptsTypes {
				m[t] = append(m[t], op.Name)
			}
		}
		for k := range m {
			sort.Strings(m[k])
		}
		return m
	}

	aggIdx := indexByType(aggs)
	attrIdx := indexByType(attrs)
	filtIdx := indexByType(filts)
	grpIdx := indexByType(grps)
	winIdx := indexByType(wins)
	featIdx := indexByType(feats)

	for i := range base {
		t := base[i].Name
		base[i].CompatibleAggregators = aggIdx[t]
		base[i].CompatibleAttributes = attrIdx[t]
		base[i].CompatibleFilterers = filtIdx[t]
		base[i].CompatibleGroupers = grpIdx[t]
		base[i].CompatibleWindows = winIdx[t]
		base[i].CompatibleFeatures = featIdx[t]
	}
	return base
}

// sortByName sorts an Operator slice lexically by Name. Used to keep the
// manifest payload deterministic.
func sortByName(ops []descriptor.Operator) []descriptor.Operator {
	out := make([]descriptor.Operator, len(ops))
	copy(out, ops)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortTestsByName(ts []descriptor.TestMeta) []descriptor.TestMeta {
	out := make([]descriptor.TestMeta, len(ts))
	copy(out, ts)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// partitionTier separates a TestMeta slice into tier-1 and tier-2 entries.
// Tier-1 entries become Manifest.Tests; tier-2 become Manifest.PostTests.
// Both slices are sorted by Name for determinism.
func partitionTier(all []descriptor.TestMeta) (tier1, tier2 []descriptor.TestMeta) {
	for _, m := range all {
		if m.Tier == 2 {
			tier2 = append(tier2, m)
		} else {
			tier1 = append(tier1, m)
		}
	}
	return sortTestsByName(tier1), sortTestsByName(tier2)
}

func sortDistributions(ds []descriptor.DistributionMeta) []descriptor.DistributionMeta {
	out := make([]descriptor.DistributionMeta, len(ds))
	copy(out, ds)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortRegressions(rs []descriptor.RegressionMeta) []descriptor.RegressionMeta {
	out := make([]descriptor.RegressionMeta, len(rs))
	copy(out, rs)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// componentsSchemasBlock collects every populated ComponentSchema from
// the per-category capability tables into the top-level
// ComponentsSchemasBlock projection. Map serialization is sorted by
// encoding/json since Go 1.12, so the golden manifest stays
// deterministic without a wrapping sort step. Every registered category
// populates its sub-map today.
//
// When snap is non-nil, every entry in snap.ComponentSchemas is merged
// into the appropriate sub-map after the built-in capability table is
// projected. Extension operators are partitioned by snapshot category
// (Aggregators / Groupers / Filterers slices); a name appearing in
// snap.Aggregators routes onto out.Aggregators, etc. Names not
// matching any snapshot category are dropped (defensive — the
// registration surface only populates ComponentSchemas alongside one
// of the three category slices).
func componentsSchemasBlock(snap *ExtensionsSnapshot) descriptor.ComponentsSchemasBlock {
	out := descriptor.ComponentsSchemasBlock{}
	if aggs := aggregatorCapabilities(); len(aggs) > 0 {
		m := make(map[string]descriptor.ComponentSchema, len(aggs))
		for _, op := range aggs {
			if len(op.ComponentSchema.Keys) == 0 && op.ComponentSchema.Mergeability == "" {
				continue
			}
			m[op.Name] = op.ComponentSchema
		}
		if len(m) > 0 {
			out.Aggregators = m
		}
	}
	if grps := grouperCapabilities(); len(grps) > 0 {
		m := make(map[string]descriptor.ComponentSchema, len(grps))
		for _, op := range grps {
			if len(op.ComponentSchema.Keys) == 0 && op.ComponentSchema.Mergeability == "" {
				continue
			}
			m[op.Name] = op.ComponentSchema
		}
		if len(m) > 0 {
			out.Groupers = m
		}
	}
	if filts := filtererCapabilities(); len(filts) > 0 {
		m := make(map[string]descriptor.ComponentSchema, len(filts))
		for _, op := range filts {
			if len(op.ComponentSchema.Keys) == 0 && op.ComponentSchema.Mergeability == "" {
				continue
			}
			m[op.Name] = op.ComponentSchema
		}
		if len(m) > 0 {
			out.Filterers = m
		}
	}
	if snap != nil && len(snap.ComponentSchemas) > 0 {
		mergeExtensionSchemas := func(target *map[string]descriptor.ComponentSchema, metas []descriptor.OperatorMeta) {
			for _, m := range metas {
				schema, ok := snap.ComponentSchemas[m.Name]
				if !ok {
					continue
				}
				if len(schema.Keys) == 0 && schema.Mergeability == "" {
					continue
				}
				if *target == nil {
					*target = make(map[string]descriptor.ComponentSchema)
				}
				(*target)[m.Name] = schema
			}
		}
		mergeExtensionSchemas(&out.Aggregators, snap.Aggregators)
		mergeExtensionSchemas(&out.Groupers, snap.Groupers)
		mergeExtensionSchemas(&out.Filterers, snap.Filterers)
	}
	return out
}

// BuildManifest constructs a deterministic Manifest from the current
// registries and capability tables. The result is safe to cache and
// share across goroutines; callers do not mutate the returned slices.
func BuildManifest() *descriptor.Manifest {
	return BuildManifestWithExtensions(nil)
}

// BuildManifestWithExtensions constructs a Manifest that includes the
// embedder-registered extension surface. A nil snapshot is equivalent
// to BuildManifest — the Extensions block becomes the empty manifest
// (every category is `[]`, not `null`).
//
// descriptor stays free of service / processing imports; the snapshot
// is the only way the live ExtensionRegistry reaches this layer.
func BuildManifestWithExtensions(snap *ExtensionsSnapshot) *descriptor.Manifest {
	allTests := append([]descriptor.TestMeta{}, testCapabilities()...)
	allTests = append(allTests, postTestCapabilities()...)
	tier1, tier2 := partitionTier(allTests)

	return &descriptor.Manifest{
		FormatVersion: "1.0",
		PulseVersion:  buildinfo.Version(),
		Commands:      commands(),
		Operations:    operations(),
		Components: descriptor.Components{
			Aggregators: sortByName(aggregatorCapabilities()),
			Attributes:  sortByName(attributeCapabilities()),
			Filterers:   sortByName(filtererCapabilities()),
			Groupers:    sortByName(grouperCapabilities()),
			Windows:     sortByName(windowCapabilities()),
			Features:    sortByName(featureCapabilities()),
		},
		Tests:              tier1,
		PostTests:          tier2,
		Regressions:        sortRegressions(regressionCapabilities()),
		SynthDistributions: sortDistributions(distributionCapabilities()),
		ErrorCodesCount:    errorCodesCount(),
		ErrorDomains:       errorDomains(),
		ErrorCodes:         errorCodeNames(),
		MCPTools:           mcpToolCapabilities(),
		CohortTypes:        cohortFieldTypes(),
		Skills:             sortedSkills(),
		ExamplesCount:      examples.Count(),
		ExampleCategories:  examples.AllCategories(),
		ExampleTags:        examples.AllTags(),
		Extensions:         extensionsManifestFromSnapshot(snap),
		Facet:              facetCapability(),
		ProcessChain:       processChainCapability(),
		Join:               joinCapability(),
		Crosstab:           crosstabCapability(),
		Export:             exportCapability(),
		Import:             importCapability(),
		Overlays:           OverlayCapabilities(),
		ComponentsSchemas:  componentsSchemasBlock(snap),
	}
}

// sortedSkills returns the embedded skill metadata as descriptor SkillMeta
// records, sorted by Name for deterministic output. The skill index is
// immutable for the process lifetime, so we cache the result.
var (
	sortedSkillsOnce sync.Once
	sortedSkillsVal  []descriptor.SkillMeta
)

func sortedSkills() []descriptor.SkillMeta {
	sortedSkillsOnce.Do(func() {
		raw := skills.List()
		out := make([]descriptor.SkillMeta, len(raw))
		for i, s := range raw {
			out[i] = descriptor.SkillMeta{Name: s.Name, Description: s.Description}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		sortedSkillsVal = out
	})
	return sortedSkillsVal
}
