package descriptor

import (
	"sort"
	"sync"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/buildinfo"
	"github.com/frankbardon/pulse/internal/skills"
	"github.com/frankbardon/pulse/internal/temporal"
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
		{Name: "features init", Description: "Print a feature profile listing every feature this build offers, or seeded from an example feature profile", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: false}},
		{Name: "features check", Description: "Validate a feature profile file against this build, exactly as pulse.New would", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: false}},
		{Name: "features diff", Description: "List the features this build offers that a feature profile does not list, and the names it does not resolve", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: false}},
		{Name: "features show", Description: "Describe every feature a feature profile lists: kind, category, source, since and dependencies", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: false}},
		{Name: "version", Description: "Print the Pulse build version (--json adds Go version, commit and envelope format_version)", Annotations: descriptor.CommandAnnotations{Streamable: false, Deterministic: true, Expensive: false}},
	}
}

// rawCohortFieldTypes returns the bare (name, categorical) tuples for
// every defined field type. Compatible* cross-refs are computed by
// cohortFieldTypesFrom from the operator capability tables.
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

// cohortFieldTypesFrom returns CohortFieldType descriptors enriched with
// Compatible* cross-references derived deterministically from the
// per-operator AcceptsTypes declarations of the given operator
// tables. The instance manifest passes its filtered tables so no
// compatible_* list names an operator the instance hides.
func cohortFieldTypesFrom(aggs, attrs, filts, grps, wins, feats []descriptor.Operator) []descriptor.CohortFieldType {
	base := rawCohortFieldTypes()

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
// the (instance-filtered) per-category capability tables into the top-level
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
func componentsSchemasBlock(aggs, grps, filts []descriptor.Operator, snap *ExtensionsSnapshot, mats []descriptor.MatrixMeta) descriptor.ComponentsSchemasBlock {
	out := descriptor.ComponentsSchemasBlock{Matrices: matrixComponentSchemas(mats)}
	if len(aggs) > 0 {
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
	if len(grps) > 0 {
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
	if len(filts) > 0 {
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

// BuildManifest constructs the deterministic FULL-registry Manifest: the
// view of a profile-free instance with no extensions, carrying that
// instance's feature_set_digest. The unprofiled CLI serves it. The
// result is safe to cache and share across goroutines; callers do not
// mutate the returned slices.
func BuildManifest() *descriptor.Manifest {
	return BuildManifestForInstance(nil)
}

// BuildManifestWithExtensions constructs a full-registry Manifest that
// includes the embedder-registered extension surface (an UNSCOPED
// instance over snap). A nil snapshot is equivalent to BuildManifest —
// the Extensions block becomes the empty manifest (every category is
// `[]`, not `null`).
//
// descriptor stays free of service / processing imports; the snapshot
// is the only way the live ExtensionRegistry reaches this layer.
func BuildManifestWithExtensions(snap *ExtensionsSnapshot) *descriptor.Manifest {
	return BuildManifestForInstance(UnscopedInstanceSnapshot(snap))
}

// assembleManifest builds every manifest section, keeping only what
// on(name) offers. on is total for an unscoped instance, so the full
// registry assembles byte-identically to the pre-feature-profile
// manifest.
func assembleManifest(inst *InstanceSnapshot, on func(string) bool) *descriptor.Manifest {
	snap := inst.Extensions()
	allTests := append([]descriptor.TestMeta{}, filterTests(testCapabilities(), on)...)
	allTests = append(allTests, filterTests(postTestCapabilities(), on)...)
	tier1, tier2 := partitionTier(allTests)

	// capability:weighting hidden: no aggregator is weight-aware on this
	// instance (no weight can reach one), so the table is read without
	// the weight stamp — no weight_aware flag, no weighted floor keys.
	aggTable := aggregatorCapabilities()
	if !on(featWeighting) {
		aggTable = aggregatorCapabilityTable()
	}
	aggs := filterOps(aggTable, on)
	attrs := filterOps(attributeCapabilities(), on)
	filts := filterOps(filtererCapabilities(), on)
	grps := filterOps(grouperCapabilities(), on)
	if on(featWeighting) {
		attrs, grps = withWeightKinds(attrs), withWeightKinds(grps)
	}
	mats := withMatrixIntents(sortMatrices(filterMatrices(matrixCapabilities(), on)))
	if on(featWeighting) {
		mats = withMatrixWeightKeys(mats)
	}
	wins := filterOps(windowCapabilities(), on)
	feats := filterOps(featureCapabilities(), on)
	errCodes := errorCodeNamesFor(on)
	disc := inst.Discovery()
	exCount, exCats, exTags := disc.ExampleStats()

	m := &descriptor.Manifest{
		FormatVersion:    "1.0",
		PulseVersion:     buildinfo.Version(),
		TZDataVersion:    temporal.TZDataVersion,
		FeatureSetDigest: manifestDigest(inst),
		Commands:         filterCommands(commands(), on),
		Operations:       filterCommands(operations(), on),
		Components: descriptor.Components{
			Aggregators: withOpIntents(withZone(sortByName(aggs))),
			Attributes:  withOpIntents(withZone(sortByName(attrs))),
			Filterers:   withOpIntents(withZone(sortByName(filts))),
			Groupers:    withOpIntents(withZone(sortByName(grps))),
			Windows:     withOpIntents(withZone(sortByName(wins))),
			Features:    withOpIntents(withZone(sortByName(feats))),
		},
		Tests:              withTestIntents(tier1),
		PostTests:          withTestIntents(tier2),
		Regressions:        withRegressionIntents(sortRegressions(filterRegressions(regressionCapabilities(), on))),
		Matrices:           mats,
		SynthDistributions: withDistributionIntents(sortDistributions(distributionCapabilities())),
		ErrorCodesCount:    len(errCodes),
		ErrorDomains:       errorDomainsFor(errCodes),
		ErrorCodes:         errCodes,
		MCPTools:           filterMCPTools(mcpToolCapabilities(), on),
		CohortTypes:        cohortFieldTypesFrom(aggs, attrs, filts, grps, wins, feats),
		Skills:             visibleSkills(disc),
		ExamplesCount:      exCount,
		ExampleCategories:  exCats,
		ExampleTags:        exTags,
		Extensions:         extensionsManifestFromSnapshot(snap),
		Overlays:           withOverlayIntents(filterOverlays(OverlayCapabilities(), on)),
		ComponentsSchemas:  componentsSchemasBlock(aggs, grps, filts, snap, mats),
		Intents:            instanceIntentIDs(inst),
	}
	if on(featWeighting) {
		stampWeightKinds(m)
	} else {
		// An extension's WeightAware declaration is moot too.
		for _, metas := range [][]descriptor.OperatorMeta{
			m.Extensions.Aggregators, m.Extensions.Attributes, m.Extensions.Tests,
		} {
			for i := range metas {
				metas[i].WeightAware = false
			}
		}
	}
	if !on(featSynth) {
		m.SynthDistributions = []descriptor.DistributionMeta{}
		m.Extensions.SynthDistributions = []descriptor.OperatorMeta{}
	}
	// A named table rides its capability: with capability:labels /
	// capability:range_tables hidden the instance lists no such table,
	// exactly as one that registered none.
	if !on(featLabels) {
		m.Extensions.LabelTables = []descriptor.LabelTableMeta{}
	}
	if !on(featRangeTables) {
		m.Extensions.RangeTables = []descriptor.RangeTableMeta{}
	}
	if on(featMatrices) {
		c := matrixCapability()
		m.Matrix = &c
	}
	if on(featFacet) {
		c := facetCapability()
		c.SupportedOverlayKinds = filterNames(c.SupportedOverlayKinds, on)
		m.Facet = &c
	}
	if on(featProcessChain) {
		c := processChainCapability()
		c.MergeableAggregators = filterNames(c.MergeableAggregators, on)
		c.MergeableGroupers = filterNames(c.MergeableGroupers, on)
		c.RowLocalAttributes = filterNames(c.RowLocalAttributes, on)
		c.OverlayKinds = filterNames(c.OverlayKinds, on)
		c.Overlays = filterOverlays(c.Overlays, on)
		m.ProcessChain = &c
	}
	if on(featJoins) {
		c := joinCapability()
		m.Join = &c
	}
	if on(featCrosstab) {
		c := crosstabCapability()
		c.SummableAggregators = filterNames(c.SummableAggregators, on)
		c.MeanReducibleAggregators = filterNames(c.MeanReducibleAggregators, on)
		c.IndependentAggregators = filterNames(c.IndependentAggregators, on)
		c.RecomputeAggregators = filterNames(c.RecomputeAggregators, on)
		c.MapValuedCellAggregators = filterNames(c.MapValuedCellAggregators, on)
		m.Crosstab = &c
	}
	if on(featExport) {
		c := exportCapability()
		c.Formats = filterSlice(c.Formats, func(f descriptor.ExportFormatCapability) bool {
			return on(FeatureName(FeatureKindIOFormat, f.Name))
		})
		m.Export = &c
	}
	if on(featImport) {
		c := importCapability()
		c.Formats = filterSlice(c.Formats, func(f descriptor.ImportFormatCapability) bool {
			return on(FeatureName(FeatureKindIOFormat, f.Name))
		})
		if !on(featExport) {
			for i := range c.Formats {
				c.Formats[i].Export = false
			}
		}
		m.Import = &c
	}
	return m
}

// sortedSkills returns the embedded skill metadata as descriptor SkillMeta
// records, sorted by Name for deterministic output. The skill index is
// immutable for the process lifetime, so we cache the result.
var (
	sortedSkillsOnce sync.Once
	sortedSkillsVal  []descriptor.SkillMeta
)

// visibleSkills is sortedSkills minus the instance's pruned skills
// (Discovery), each survivor's description rendered through the same
// renderMetadata pass pulse_skills_list uses, so a listed description
// names no hidden operator or tool. Survivors are copies — the cached
// sortedSkills slice is never written. With nothing pruned and nothing
// to scrub it is sortedSkills itself.
// Visible embedder skills (Extensions.Skills) are listed like
// built-ins, in name order (Discovery.Skills).
func visibleSkills(d *Discovery) []descriptor.SkillMeta {
	all := sortedSkills()
	if len(d.hiddenSkills) == 0 && !d.scrub.Active() && len(d.ext) == 0 {
		return all
	}
	listed := d.Skills()
	out := make([]descriptor.SkillMeta, len(listed))
	for i, md := range listed {
		out[i] = descriptor.SkillMeta{Name: md.Name, Description: md.Description}
	}
	return out
}

// instanceIntentIDs is IntentIDs minus the intents the instance
// ontology pruned (an intent every serving operator of which is hidden;
// an intent no operator serves always stays). Unscoped, it is the full
// taxonomy.
func instanceIntentIDs(inst *InstanceSnapshot) []string {
	all := IntentIDs()
	g := inst.Ontology()
	out := all[:0]
	for _, id := range all {
		if g.Has(OntologyID(descriptor.OntologyNodeIntent, id)) {
			out = append(out, id)
		}
	}
	return out
}

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
