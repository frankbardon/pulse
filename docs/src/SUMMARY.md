# Summary

[Introduction](README.md)

# Getting Started

- [Installation](getting-started/installation.md)
- [Your First Cohort](getting-started/first-cohort.md)
- [CLI Tour](getting-started/cli-tour.md)

# Command Line Reference

- [api process](cli/api-process.md)
- [api compose](cli/api-compose.md)
- [cohort inspect](cli/cohort-inspect.md)
- [api predict](cli/api-predict.md)
- [api sample](cli/api-sample.md)
- [api facet](cli/api-facet.md)
- [api lookup](cli/api-lookup.md)
- [index](cli/index.md)
- [manifest](cli/manifest.md)
- [schema](cli/schema.md)
- [synth from-schema](cli/synth-from-schema.md)
- [synth from-profile](cli/synth-from-profile.md)
- [profile create](cli/profile-create.md)
- [mcp](cli/mcp.md)
- [Flag Reference](cli/flags.md)

# Library Embedding

- [Go API Overview](library/overview.md)
- [pulse.New & Options](library/options.md)
- [Custom Filesystems](library/custom-fs.md)
- [Streaming & ProcessStream](library/streaming.md)
- [StreamResult[T]](library/stream-result.md)
- [Parallel Compose](library/parallel-compose.md)
- [Request Hashing](library/request-hashing.md)
- [Watch & WatchDir](library/watch.md)
- [Deterministic FilterToFile](library/filter-to-file.md)

# .pulse File Format

- [Header Layout](format/header.md)
- [Field Types](format/field-types.md)
- [Schema Block](format/schema-block.md)
- [Dictionary Blocks](format/dictionaries.md)
- [Record Layout](format/records.md)

# MCP Integration

- [Server, Tools, Resources](mcp/index.md)

# Payload Contract

- [JSON Schema](contract/payload-schema.md)

# Examples Library

- [Searchable Request Examples](examples/library.md)

# Operator Families

- [Regression Modeling](regression.md)

# Internals

- [Architecture Overview](internals/architecture.md)
- [Package Layout](internals/packages.md)
- [Extension Points](internals/extension-points.md)
- [Adding an Aggregator](internals/adding-aggregator.md)
- [Adding an Attribute](internals/adding-attribute.md)
- [Adding a Filterer](internals/adding-filterer.md)
- [Adding a Grouper](internals/adding-grouper.md)
- [Adding a Window Operator](internals/adding-window.md)
- [Adding a Feature Operator](internals/adding-feature.md)
- [Adding a Statistical Test](internals/adding-test.md)
- [Adding a Synth Distribution](internals/adding-synth-distribution.md)
- [Adding an I/O Format](internals/adding-io-format.md)
- [Adding a Field Type](internals/adding-field-type.md)
- [Adding an Error Code](internals/adding-error-code.md)
- [Adding an MCP Tool](internals/adding-mcp-tool.md)
- [Adding a Facet Capability Variant](internals/adding-facet-capability.md)
- [Adding a Chain-Stage Predicate](internals/adding-chain-predicate.md)
- [Adding an `afero.Fs` Implementation](internals/adding-afero-fs.md)
- [Managing a Shard Archive](internals/managing-shard-archives.md)
- [Wiring Pulse into an MCP Client](internals/wiring-mcp-client.md)
- [Debugging a Predict Mismatch](internals/debugging-predict.md)
- [Regenerating Goldens](internals/regenerating-goldens.md)
- [The Update Demand](internals/update-demand.md)

# Operations

- [Deployment](ops/deployment.md)
- [Performance Notes](ops/performance.md)
- [Troubleshooting](ops/troubleshooting.md)

# Contributing

- [Development Setup](contributing/setup.md)
- [Style Guide](contributing/style.md)
- [Testing Conventions](contributing/testing.md)
- [Running CI Gates Locally](contributing/local-ci.md)
- [Porting Workflow](contributing/porting-workflow.md)
- [Pull Request Process](contributing/pr-process.md)
