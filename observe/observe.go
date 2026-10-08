// Package observe holds the vocabulary a host uses to watch a Pulse
// instance: the hook set (Hooks), what each hook is told
// (OperationInfo, OperationResult, PhaseTiming), the bounded enums those
// carry (OperationKind, Phase, Arm, Scope) and the metric instrument
// interfaces an adapter implements (Metrics, Counter, Histogram,
// UpDownCounter, Label).
//
// It is a leaf: it imports only the standard library and nothing from
// Pulse, so an adapter module (OpenTelemetry, Prometheus) can implement
// it without pulling in the engine. A host wires it through
// pulse.Options.Hooks and pulse.Options.Metrics; every field is
// optional and a nil value costs nothing.
//
// Hooks and instruments run synchronously on the operation's goroutine.
// They must be fast and must not block; a panic inside one is recovered
// and the operation continues unchanged.
package observe

import (
	"context"
	"time"
)

// OperationKind names one kind of Pulse operation — one instrumented
// *pulse.Pulse method family. The set is closed and bounded so it is
// safe as a metric label.
type OperationKind string

// The operation kinds. Several facade methods share a kind: Inspect,
// InspectEnvelope and InspectBytes are all OpInspect; Import,
// ImportTransfer and ImportFile are all OpImport; and so on.
const (
	OpOpen            OperationKind = "open"
	OpProcess         OperationKind = "process"
	OpProcessStream   OperationKind = "process_stream"
	OpCompose         OperationKind = "compose"
	OpComposeParallel OperationKind = "compose_parallel"
	OpProcessChain    OperationKind = "process_chain"
	OpFacet           OperationKind = "facet"
	OpFacetSchema     OperationKind = "facet_schema"
	OpLookup          OperationKind = "lookup"
	OpCountRecords    OperationKind = "count_records"
	OpInspect         OperationKind = "inspect"
	OpPredict         OperationKind = "predict"
	OpManifest        OperationKind = "manifest"
	OpPayloadSchema   OperationKind = "payload_schema"
	OpImport          OperationKind = "import"
	OpExport          OperationKind = "export"
	OpConvert         OperationKind = "convert"
	OpImportsSweep    OperationKind = "imports_sweep"
	OpDrop            OperationKind = "drop"
	OpSample          OperationKind = "sample"
	OpFilterToFile    OperationKind = "filter_to_file"
	OpSynth           OperationKind = "synth"
	OpSynthStream     OperationKind = "synth_stream"
	OpProfile         OperationKind = "profile"
	OpDedup           OperationKind = "dedup"
	OpWiden           OperationKind = "widen"
	OpIndexBuild      OperationKind = "index_build"
	OpIndexVerify     OperationKind = "index_verify"
	OpIndexList       OperationKind = "index_list"
	OpIndexDrop       OperationKind = "index_drop"
	OpShardCreate     OperationKind = "shard_create"
	OpShardAdd        OperationKind = "shard_add"
	OpShardRemove     OperationKind = "shard_remove"
	OpShardList       OperationKind = "shard_list"
	OpShardExtract    OperationKind = "shard_extract"
	OpShardCompact    OperationKind = "shard_compact"
	OpShardVerify     OperationKind = "shard_verify"
	OpTemplateRender  OperationKind = "template_render"
	OpTemplateReload  OperationKind = "template_reload"
)

// AllOperationKinds returns every OperationKind in declaration order. An
// adapter pre-resolving per-kind instruments enumerates it.
func AllOperationKinds() []OperationKind {
	return []OperationKind{
		OpOpen, OpProcess, OpProcessStream, OpCompose, OpComposeParallel,
		OpProcessChain, OpFacet, OpFacetSchema, OpLookup, OpCountRecords,
		OpInspect, OpPredict, OpManifest, OpPayloadSchema, OpImport,
		OpExport, OpConvert, OpImportsSweep, OpDrop, OpSample,
		OpFilterToFile, OpSynth, OpSynthStream, OpProfile, OpDedup,
		OpWiden, OpIndexBuild, OpIndexVerify, OpIndexList, OpIndexDrop,
		OpShardCreate, OpShardAdd, OpShardRemove, OpShardList,
		OpShardExtract, OpShardCompact, OpShardVerify, OpTemplateRender,
		OpTemplateReload,
	}
}

// Phase names one coarse stage inside an operation. A phase is reported
// only when it actually runs.
type Phase string

// The phases, in execution order.
const (
	// PhasePlan covers refusals, return resolution and ComputePlan,
	// defaults and the limits pre-flight.
	PhasePlan Phase = "plan"
	// PhaseOpen covers opening the cohort (header, schema, archive).
	PhaseOpen Phase = "open"
	// PhaseDecode covers materializing records; buffered arm only.
	PhaseDecode Phase = "decode"
	// PhaseScan covers filtering and aggregation (buffered), or decode,
	// filter and aggregate fused per row (every other arm).
	PhaseScan Phase = "scan"
	// PhaseReduce covers the shard or parallel-decode merge.
	PhaseReduce Phase = "reduce"
	// PhasePost covers components, matrices, tests and regressions.
	PhasePost Phase = "post"
	// PhaseOverlay covers the series and Compose overlay folds.
	PhaseOverlay Phase = "overlay"
	// PhaseShape covers response shaping (the `return` block).
	PhaseShape Phase = "shape"
)

// AllPhases returns every Phase in execution order.
func AllPhases() []Phase {
	return []Phase{PhasePlan, PhaseOpen, PhaseDecode, PhaseScan, PhaseReduce, PhasePost, PhaseOverlay, PhaseShape}
}

// Arm names the execution strategy the engine chose for a request.
type Arm string

// The execution arms. The zero value ("") means no arm applies — the
// operation does not run the request engine.
const (
	ArmBuffered       Arm = "buffered"
	ArmStreaming      Arm = "streaming"
	ArmFusedCrosstab  Arm = "fused_crosstab"
	ArmParallelDecode Arm = "parallel_decode"
	ArmShardParallel  Arm = "shard_parallel"
	ArmJoin           Arm = "join"
)

// AllArms returns every Arm in declaration order.
func AllArms() []Arm {
	return []Arm{ArmBuffered, ArmStreaming, ArmFusedCrosstab, ArmParallelDecode, ArmShardParallel, ArmJoin}
}

// Scope says whether an operation was called by the host (top) or fired
// by Pulse inside another operation (child) — a Compose slot or a
// ProcessChain stage.
type Scope string

// The scopes.
const (
	ScopeTop   Scope = "top"
	ScopeChild Scope = "child"
)

// AllScopes returns every Scope.
func AllScopes() []Scope { return []Scope{ScopeTop, ScopeChild} }

// The two OperationResult.Code values that are not error codes.
const (
	// CodeOK is the code of an operation that returned no error.
	CodeOK = "ok"
	// CodeUncoded is the code of an operation that failed with an error
	// carrying no Pulse error code (a plain Go error).
	CodeUncoded = "uncoded"
)

// OperationInfo identifies one operation. It is built once at start and
// handed unchanged to every hook call for that operation.
type OperationInfo struct {
	// Kind is the operation kind.
	Kind OperationKind
	// Scope is ScopeTop for a host call, ScopeChild for a slot or stage
	// Pulse fired inside another operation.
	Scope Scope
	// ID identifies the operation within its Pulse instance; never 0.
	ID uint64
	// Parent is the ID of the enclosing operation for a child, 0 for a
	// top-level operation.
	Parent uint64
	// Index is the slot (Compose) or stage (ProcessChain) index of a
	// child operation; 0 for a top-level operation.
	Index int
	// Cohort is the cohort path the operation addresses, empty when it
	// addresses none.
	Cohort string
	// RequestHash is the canonical request hash (the request type's
	// Hash method), empty when the operation takes no request.
	RequestHash string
}

// OperationResult describes how an operation ended. Counters an
// operation does not produce stay zero.
type OperationResult struct {
	// Duration is the wall time from start to end.
	Duration time.Duration
	// Code is CodeOK, the failing error's Pulse error code, or
	// CodeUncoded. It never carries an error message.
	Code string
	// RowsScanned is the number of records read.
	RowsScanned int64
	// RowsMatched is the number of records that passed the filters.
	RowsMatched int64
	// RowsOut is the number of result rows produced.
	RowsOut int64
	// BytesRead is the number of cohort bytes read.
	BytesRead int64
	// Shards is the number of shards read.
	Shards int
	// Workers is the number of workers the run used.
	Workers int
	// Arm is the execution arm the engine chose.
	Arm Arm
	// Projected reports whether a projected (field-subset) decode ran.
	Projected bool
}

// PhaseTiming reports one finished phase.
type PhaseTiming struct {
	// Phase is the phase that finished.
	Phase Phase
	// Duration is its wall time.
	Duration time.Duration
}

// Hooks are optional callbacks fired around every operation. Each field
// is independently optional; a nil field is skipped.
//
// Hooks run synchronously on the operation's goroutine and must be fast.
// A panic in a hook is recovered and the operation continues; the
// result the host receives is unchanged.
type Hooks struct {
	// OnOperationStart fires before the operation does any work. The
	// context it returns (when non-nil) replaces the operation's context
	// for the work and for the matching OnPhase and OnOperationEnd
	// calls — the place to start a span.
	OnOperationStart func(ctx context.Context, info OperationInfo) context.Context
	// OnOperationEnd fires exactly once when the operation ends.
	OnOperationEnd func(ctx context.Context, info OperationInfo, result OperationResult)
	// OnPhase fires when a phase of the operation finishes.
	OnPhase func(ctx context.Context, info OperationInfo, phase PhaseTiming)
}

// Label is one metric label pair.
type Label struct {
	Key   string
	Value string
}

// Metrics creates metric instruments. Pulse calls it only while building
// an instance, once per name and label combination, and caches what it
// returns; the hot path never calls the factory. Bucket boundaries for
// a Histogram are the adapter's choice.
type Metrics interface {
	Counter(name string, labels ...Label) Counter
	Histogram(name string, labels ...Label) Histogram
	UpDownCounter(name string, labels ...Label) UpDownCounter
}

// Counter is a monotonically increasing instrument.
type Counter interface {
	// Add increases the counter by delta (never negative).
	Add(delta float64)
}

// Histogram records a distribution of observed values.
type Histogram interface {
	// Observe records one value.
	Observe(value float64)
}

// UpDownCounter is an instrument that can go up and down.
type UpDownCounter interface {
	// Add changes the value by delta, positive or negative.
	Add(delta float64)
}
