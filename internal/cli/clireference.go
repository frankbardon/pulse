package cli

import (
	cli "github.com/urfave/cli/v3"

	"github.com/frankbardon/pulse/internal/docgen"
)

// CLIReference returns the command tree as the generated flags reference
// (docs/src/cli/reference.md) shows it: root's global flags and every
// runnable leaf with its own flags and in-depth pages. root is set up
// first (SetUpTree). `make docs` writes the page; TestCLIReferenceCurrent
// keeps it current.
func CLIReference(root *cli.Command) docgen.CLIReference {
	SetUpTree(root)
	leaves := Leaves(root)
	for i := range leaves {
		leaves[i].Links = leafDocLinks[leaves[i].Path]
	}
	return docgen.CLIReference{Globals: GlobalFlags(root), Leaves: leaves}
}

// leafDocLinks names the book pages that document a leaf beyond its flags,
// targets relative to docs/src/cli/. A leaf with no entry is covered by
// its flags and --help. Every key must be a mounted leaf
// (TestLeafDocLinksMounted) and every target must resolve
// (TestDocsLinksResolve over the generated page).
var leafDocLinks = map[string][]docgen.CLILink{
	"pulse api compose":            {{Text: "api compose", Target: "api-compose.md"}},
	"pulse api facet":              {{Text: "api facet", Target: "api-facet.md"}},
	"pulse api lookup":             {{Text: "api lookup", Target: "api-lookup.md"}, {Text: "point lookup", Target: "../library/point-lookup.md"}},
	"pulse api materialize":        {{Text: "Materialize", Target: "../library/materialize.md"}},
	"pulse api predict":            {{Text: "api predict", Target: "api-predict.md"}},
	"pulse api predict-chain":      {{Text: "api predict", Target: "api-predict.md#compose-facet-and-chain-requests"}},
	"pulse api predict-compose":    {{Text: "api predict", Target: "api-predict.md#compose-facet-and-chain-requests"}},
	"pulse api predict-facet":      {{Text: "api predict", Target: "api-predict.md#compose-facet-and-chain-requests"}},
	"pulse api process":            {{Text: "api process", Target: "api-process.md"}},
	"pulse api process-chain":      {{Text: "api process", Target: "api-process.md"}},
	"pulse api sample":             {{Text: "api sample", Target: "api-sample.md"}},
	"pulse cohort filter":          {{Text: "filter to a file", Target: "../library/filter-to-file.md"}, {Text: "--time-zone", Target: "flags.md#--time-zone-cohort-filter"}},
	"pulse cohort inspect":         {{Text: "cohort inspect", Target: "cohort-inspect.md"}},
	"pulse cohort zone clear":      {{Text: "default zone field", Target: "flags.md#--zone-field-cohort-default-zone"}},
	"pulse cohort zone set":        {{Text: "default zone field", Target: "flags.md#--zone-field-cohort-default-zone"}},
	"pulse cohorts":                {{Text: "listing the cohorts", Target: "cohort-inspect.md#listing-the-cohorts-pulse-cohorts"}},
	"pulse completion bash":        {{Text: "completion", Target: "completion.md"}},
	"pulse completion fish":        {{Text: "completion", Target: "completion.md"}},
	"pulse completion zsh":         {{Text: "completion", Target: "completion.md"}},
	"pulse convert":                {{Text: "import spss", Target: "import-spss.md"}, {Text: "export spss", Target: "export-spss.md"}},
	"pulse convert predict":        {{Text: "export spss", Target: "export-spss.md"}},
	"pulse dedup":                  {{Text: "parent groups", Target: "../format/parent-groups.md"}},
	"pulse docs export":            {{Text: "exporting the reference", Target: "../library/guided-analysis.md#exporting-the-reference"}},
	"pulse examples data":          {{Text: "get the data", Target: "../examples/library.md#get-the-data"}},
	"pulse examples search":        {{Text: "example library", Target: "../examples/library.md"}},
	"pulse examples show":          {{Text: "example library", Target: "../examples/library.md"}},
	"pulse explain":                {{Text: "explain", Target: "../library/guided-analysis.md#explain-from-a-request-or-a-result-to-plain-language"}},
	"pulse export predict":         {{Text: "export spss", Target: "export-spss.md"}},
	"pulse export spss":            {{Text: "export spss", Target: "export-spss.md"}},
	"pulse export transfer":        {{Text: "transfer compression", Target: "../format/transfer.md"}},
	"pulse features check":         {{Text: "feature profiles", Target: "../library/feature-profiles.md"}},
	"pulse features diff":          {{Text: "feature profiles", Target: "../library/feature-profiles.md"}},
	"pulse features init":          {{Text: "feature profiles", Target: "../library/feature-profiles.md"}},
	"pulse features show":          {{Text: "feature profiles", Target: "../library/feature-profiles.md"}},
	"pulse import auto":            {{Text: "import spss", Target: "import-spss.md"}},
	"pulse import predict":         {{Text: "import spss", Target: "import-spss.md"}},
	"pulse import schema-template": {{Text: "import spss", Target: "import-spss.md"}},
	"pulse import spss":            {{Text: "import spss", Target: "import-spss.md"}},
	"pulse import transfer":        {{Text: "transfer compression", Target: "../format/transfer.md"}},
	"pulse index build":            {{Text: "index", Target: "index.md"}},
	"pulse index drop":             {{Text: "index", Target: "index.md"}},
	"pulse index list":             {{Text: "index", Target: "index.md"}},
	"pulse index verify":           {{Text: "index", Target: "index.md"}},
	"pulse mcp":                    {{Text: "mcp", Target: "mcp.md"}},
	"pulse profile create":         {{Text: "profile create", Target: "profile-create.md"}},
	"pulse rake":                   {{Text: "Rake", Target: "../library/rake.md"}},
	"pulse recommend":              {{Text: "recommend", Target: "../library/guided-analysis.md#recommend-from-a-question-to-a-request"}},
	"pulse schema":                 {{Text: "schema", Target: "schema.md"}},
	"pulse shard add":              {{Text: "shard archives", Target: "../internals/managing-shard-archives.md"}},
	"pulse shard compact":          {{Text: "shard archives", Target: "../internals/managing-shard-archives.md"}},
	"pulse shard create":           {{Text: "shard archives", Target: "../internals/managing-shard-archives.md"}},
	"pulse shard extract":          {{Text: "shard archives", Target: "../internals/managing-shard-archives.md"}},
	"pulse shard list":             {{Text: "shard archives", Target: "../internals/managing-shard-archives.md"}},
	"pulse shard remove":           {{Text: "shard archives", Target: "../internals/managing-shard-archives.md"}},
	"pulse shard verify":           {{Text: "shard archives", Target: "../internals/managing-shard-archives.md"}},
	"pulse synth from-profile":     {{Text: "synth from-profile", Target: "synth-from-profile.md"}},
	"pulse synth from-schema":      {{Text: "synth from-schema", Target: "synth-from-schema.md"}},
	"pulse widen":                  {{Text: "parent groups", Target: "../format/parent-groups.md"}},
}
