package embeddersmoke

import (
	"bytes"
	"context"
	"io"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/mcp/gosdk"
	"github.com/frankbardon/pulse/mcpserve"
	"github.com/frankbardon/pulse/synth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/afero"
)

// Compile-only checks. Each assignment pins a spelling or a signature
// from the embedder migration guide; a rename, a removal or a move under
// internal/ breaks the build of this module rather than an embedder's.

// Root facade: construction and the instance methods that replaced the
// descriptor free functions and the index-manifest helpers.
var (
	_ func(pulse.Options) (*pulse.Pulse, error)                                                                          = pulse.New
	_ func() string                                                                                                      = pulse.Version
	_ func(*pulse.Pulse, context.Context, *pulse.Request) (*pulse.Response, error)                                       = (*pulse.Pulse).Process
	_ func(*pulse.Pulse, context.Context, *pulse.ComposedRequest) (*pulse.ComposedResponse, error)                       = (*pulse.Pulse).Compose
	_ func(*pulse.Pulse, context.Context, *pulse.ComposedRequest, pulse.ComposeOptions) (*pulse.ComposedResponse, error) = (*pulse.Pulse).ComposeParallel
	_ func(*pulse.Pulse, context.Context, []byte, *descriptor.InspectOptions) (*descriptor.Envelope, error)              = (*pulse.Pulse).InspectBytes
	_ func(*pulse.Pulse, context.Context, []byte, *pulse.Request) (*descriptor.Envelope, error)                          = (*pulse.Pulse).PredictBytes
	_ func(*pulse.Pulse, context.Context, *pulse.ComposedRequest) (*descriptor.Envelope, error)                          = (*pulse.Pulse).PredictCompose
	_ func(*pulse.Pulse, context.Context, *pulse.FacetRequest) (*descriptor.Envelope, error)                             = (*pulse.Pulse).PredictFacet
	_ func(*pulse.Pulse, context.Context, *pulse.ChainRequest) (*descriptor.Envelope, error)                             = (*pulse.Pulse).PredictChain
	// The predict-root envelopes' Data types.
	_                                                                                        = pulse.ComposePredictResult{}
	_                                                                                        = pulse.FacetPredictResult{}
	_                                                                                        = pulse.ChainPredictResult{}
	_                                                                                        = pulse.ChainOverlaySchemaDivergence{}
	_ func(*pulse.Pulse, context.Context, string) ([]string, error)                          = (*pulse.Pulse).CohortArtifacts
	_ func(*pulse.Pulse, context.Context, string, []string) (*pulse.BuildIndexResult, error) = (*pulse.Pulse).BuildIndex
	_ func(*pulse.Pulse, context.Context, *pio.ImportJob) (*pio.ImportReport, error)         = (*pulse.Pulse).Import
	_ func(*pulse.Pulse, context.Context, *pio.ExportJob) (*pio.ExportReport, error)         = (*pulse.Pulse).Export
	_ func(*pulse.Pulse, context.Context, pulse.ImportSpec) (*pulse.ImportResult, error)     = (*pulse.Pulse).ImportFile
	_ func(io.Reader, *encoding.Schema, string) (pulse.LoadMemberSetResult, error)           = pulse.LoadMemberSetFromReader
)

// Record-by-record reader: Cohort.Reader and the exact-typed CohortRow.
var (
	_ func(*pulse.Cohort) (*pulse.CohortReader, error)          = (*pulse.Cohort).Reader
	_ func(*pulse.CohortReader) *encoding.Schema                = (*pulse.CohortReader).Schema
	_ func(*pulse.CohortReader) int64                           = (*pulse.CohortReader).Len
	_ func(*pulse.CohortReader, int64) (pulse.CohortRow, error) = (*pulse.CohortReader).RecordAt
	_ func(*pulse.CohortReader) error                           = (*pulse.CohortReader).Close
	_ []any                                                     = pulse.CohortRow(nil)
)

// Row-at-a-time builder: Pulse.NewCohortBuilder, Append / Close / Abort
// and the result + options it names.
var (
	_ func(*pulse.Pulse, context.Context, string, encoding.Schema, pulse.CohortBuilderOptions) (*pulse.CohortBuilder, error) = (*pulse.Pulse).NewCohortBuilder
	_ func(*pulse.CohortBuilder, pulse.CohortRow) error                                                                      = (*pulse.CohortBuilder).Append
	_ func(*pulse.CohortBuilder) (*pulse.CohortBuildResult, error)                                                           = (*pulse.CohortBuilder).Close
	_ func(*pulse.CohortBuilder) error                                                                                       = (*pulse.CohortBuilder).Abort
	_                                                                                                                        = pulse.CohortBuilderOptions{Strict: true, Overwrite: true, Groups: []pio.GroupDecl{{Key: []string{"k"}, Members: []string{"m"}}}, ElideConstants: true, RatioFloor: 2}
	_                                                                                                                        = pulse.CohortBuildResult{Groups: []pio.GroupReport(nil), ElidedConstants: []string(nil), Shards: []string(nil)}
	_                                                                                                                        = pulse.CohortBuilderOptions{Shards: &pulse.ShardSplit{MaxRecords: 1000}}
	_                                                                                                                        = pulse.CohortBuildResult{Target: "", Records: 0, FormatVersion: encoding.FormatVersionV1, Schema: (*encoding.Schema)(nil), Warnings: []*perrors.CodedError(nil), InvalidatedSidecars: []pulse.StaleSidecar(nil)}
)

// Options carries the crosstab-fusion switch that replaced mutating the
// service, alongside the filesystem an embedder supplies.
var _ = pulse.Options{
	FS:                    afero.NewMemMapFs(),
	DisableCrosstabFusion: true,
}

// ComposeOptions is a root alias with the parallel-compose knobs.
var _ = pulse.ComposeOptions{MaxWorkers: 2, FailFast: true}

// Root aliases and root-native types an embedder spells by name.
var (
	_ pulse.BuildIndexResult
	_ pulse.SetWidening
	_ pulse.CreateShardArchiveResult
	_ pulse.AddShardResult
	_ pulse.GroupReconciliation
	_ pulse.VerifyResult
	_ pulse.ShardEntry
	_ pulse.VerifyIndexResult
	_ pulse.IndexFreshnessReason
	_ pulse.IndexInfo
	_ pulse.Row
	_ pulse.RowIter
	_ pulse.ImportSpec
	_ pulse.ImportResult
	_ pulse.ImportEntry
	_ pulse.Example
	_ pulse.CohesionWarning
	_ pulse.GroupIndexHeadroom
	_ pulse.SidecarIndex
	_ pulse.SidecarIndexKeySpec
	_ pulse.SidecarIndexBucket
	_ pulse.SidecarIndexEntry
	_ pulse.CohortFingerprint
	_ pulse.Template
	_ pulse.TemplateSummary
	_ pulse.TemplateTarget
	_ pulse.TemplateVarType
	_ pulse.TemplateVariable
	_ pulse.LoadMemberSetResult
	_ pulse.MemberSet
)

// walkSidecarIndex spells every type in SidecarIndex's closure by its
// root alias and walks the structure: an external module can read a
// BuildIndexResult.Index end to end without an internal import.
func walkSidecarIndex(idx *pulse.SidecarIndex) (fp pulse.CohortFingerprint, keys []string, rowIDs []uint64) {
	fp = idx.Fingerprint
	for _, k := range idx.Keys {
		var spec pulse.SidecarIndexKeySpec = k
		keys = append(keys, spec.Name)
	}
	for _, b := range idx.Buckets {
		var bucket pulse.SidecarIndexBucket = b
		for _, e := range bucket.Entries {
			var entry pulse.SidecarIndexEntry = e
			rowIDs = append(rowIDs, entry.RowIDs...)
		}
	}
	return fp, keys, rowIDs
}

// Template targets and variable types have root spellings: an embedder
// compares Template.Target / TemplateVariable.Type against these, never
// against String().
var (
	_ = []pulse.TemplateTarget{
		pulse.TemplateTargetRequest, pulse.TemplateTargetComposed, pulse.TemplateTargetChain,
		pulse.TemplateTargetFacet, pulse.TemplateTargetSample,
	}
	_ = []pulse.TemplateVarType{
		pulse.TemplateVarString, pulse.TemplateVarNumber, pulse.TemplateVarInteger,
		pulse.TemplateVarBoolean, pulse.TemplateVarField, pulse.TemplateVarEnum,
		pulse.TemplateVarList, pulse.TemplateVarDate, pulse.TemplateVarPeriod,
	}
)

// DateRangeSpec is root-native and feeds a RangeTable extension.
var _ = pulse.RangeTable{Ranges: []pulse.DateRangeSpec{{Label: "all"}}}

// io: the format factory, its option sub-structs and the jobs.
var (
	_ func(pio.Format, afero.Fs, string, pio.ReaderOptions) (pio.Reader, error) = pio.NewReader
	_ func(pio.Format, afero.Fs, string, pio.WriterOptions) (pio.Writer, error) = pio.NewWriter
	_ func(pio.Format, []byte, pio.ReaderOptions) (pio.Reader, error)           = pio.NewReaderFromBytes
	_ func(pio.Format, pio.WriterOptions) (pio.BufferWriter, error)             = pio.NewWriterToBuffer
	_ func(pio.Reader, string) *pio.ImportJob                                   = pio.NewImportJob
	_ func(string, pio.Writer) *pio.ExportJob                                   = pio.NewExportJob
	_ func(string) pio.Format                                                   = pio.FormatFromPath
	_ func() []pio.Format                                                       = pio.Formats
	_ []pio.Format                                                              = []pio.Format{
		pio.FormatCSV, pio.FormatTSV, pio.FormatNDJSON, pio.FormatJSONArray,
		pio.FormatArrow, pio.FormatParquet, pio.FormatExcel, pio.FormatSPSS, pio.FormatPulse,
	}
	_ = pio.ReaderOptions{
		Excel: pio.ExcelReaderOptions{Sheet: "Sheet1"},
		SPSS:  pio.SPSSReaderOptions{Charset: "UTF-8", MissingMode: pio.SPSSMissingNull},
	}
	_ = pio.WriterOptions{SPSS: pio.SPSSWriterOptions{SanitizeNames: true}}
	_ pio.RowError
)

// encoding: schema nouns, the raw ungrouped write primitives and the
// record-locator geometry.
var (
	_ func(io.Writer) error                                                  = encoding.WriteHeader
	_ func(io.Writer, *encoding.Schema) error                                = encoding.WriteSchema
	_ func(io.Writer, encoding.FieldType, uint64) error                      = encoding.WriteFieldValue
	_ func(io.Reader, encoding.FieldType) (uint64, error)                    = encoding.ReadFieldValue
	_ func(io.Reader) (byte, error)                                          = encoding.ReadHeader
	_ func(io.Reader, byte) (*encoding.Schema, error)                        = encoding.ReadSchema
	_ func(*bytes.Reader, *encoding.Schema) (*encoding.RecordLocator, error) = encoding.NewRecordLocator
	_ func(string) (encoding.FieldType, bool)                                = encoding.ParseFieldType
	_ func(string) (uint32, error)                                           = encoding.ParseDate
	_ func(uint64) int32                                                     = encoding.DateDays
	_ func(uint64) int64                                                     = encoding.DateTimeSeconds
	_ func([]byte, int) bool                                                 = encoding.BitmapIsNull
	_ func([]byte, int)                                                      = encoding.BitmapSetNull
	_ func(*encoding.RecordLocator, uint64) int64                            = (*encoding.RecordLocator).Offset
)

// descriptor + errors.
var (
	_ func(any) *descriptor.Envelope            = descriptor.NewEnvelope
	_ func(string) (perrors.LookupResult, bool) = perrors.Lookup
	_ func() []perrors.Code                     = perrors.AllCodes
	_ error                                     = (*perrors.CodedError)(nil)
)

// synth: the fixture-building entry points stay public.
var (
	_ func(afero.Fs, *synth.Spec, string, synth.Options) (*synth.Result, error) = synth.Synth
	_ func(*synth.Spec, synth.Options) ([]byte, *synth.Result, error)           = synth.SynthBytes
)

// MCP: mount onto a caller-owned go-sdk server, or serve directly.
var (
	_ func(*mcpsdk.Server, *pulse.Pulse, gosdk.Config) error                            = gosdk.Register
	_                                                                                   = gosdk.Config{Version: "embedder", DisableCohortScan: true}
	_                                                                                   = mcpserve.Options{Version: "embedder", DisableCohortScan: true}
	_ func(context.Context, *pulse.Pulse, mcpserve.Options, io.Reader, io.Writer) error = mcpserve.Serve
	_                                                                                   = mcpserve.Options{FeatureProfileFile: "profile.json"}
	_ func(pulse.Options, mcpserve.Options) (*pulse.Pulse, error)                       = mcpserve.NewPulse
	_ func([]byte) (*pulse.FeatureProfile, error)                                       = pulse.ParseFeatureProfile
	_ func(*pulse.Pulse, mcpserve.Options) mcpserve.ServeInfo                           = mcpserve.Describe
	_ func(*pulse.Pulse) string                                                         = (*pulse.Pulse).FeatureSetDigest
	_ func(*pulse.Pulse) (*pulse.FeatureProfile, bool)                                  = (*pulse.Pulse).FeatureProfile
	_ func(*pulse.Pulse) ([]byte, error)                                                = (*pulse.Pulse).PayloadSchema
	_ func() []string                                                                   = pulse.ExampleFeatureProfiles
	_ func(string) (*pulse.FeatureProfile, error)                                       = pulse.ExampleFeatureProfile
)

// Feature-profile tooling: init / check / diff / describe without a
// *pulse.Pulse, for CI.
var (
	_ func(string, ...pulse.Extensions) (*pulse.FeatureProfile, error)                                  = pulse.InitFeatureProfile
	_ func(*pulse.FeatureProfile, pulse.FeatureProfileCheckOptions) (*pulse.FeatureProfileCheck, error) = pulse.CheckFeatureProfile
	_ func(*pulse.FeatureProfile, ...pulse.Extensions) (*pulse.FeatureProfileDiff, error)               = pulse.DiffFeatureProfile
	_ func(*pulse.FeatureProfile, ...pulse.Extensions) (*pulse.FeatureProfileDescription, error)        = pulse.DescribeFeatureProfile
	_                                                                                                   = pulse.FeatureProfileCheckOptions{Extensions: pulse.Extensions{}, Offline: true}
	_                                                                                                   = pulse.FeatureProfileCheck{Unverified: []string{}, Warnings: []*descriptor.EnvelopeEntry{}}
	_                                                                                                   = pulse.FeatureProfileDiff{Missing: []pulse.FeatureProfileMissing{{New: true}}, Unknown: []pulse.FeatureProfileUnknownName{{DidYouMean: "capability:process"}}}
	_                                                                                                   = pulse.FeatureProfileDescription{Features: []pulse.FeatureDescription{{DependsOn: [][]string{}, Unknown: &pulse.FeatureProfileUnknownName{}}}}
)

// Guided-analysis vocabulary: the glossary and intent taxonomy, as
// copies an embedder may mutate.
var (
	_ func() []descriptor.Term   = pulse.Glossary
	_ func() []descriptor.Intent = pulse.Intents
)

// Instance discovery: the pruned skill ontology and the skill pack as
// this instance serves them (what an embedder's discovery UI reads).
var (
	_ func(*pulse.Pulse) descriptor.Ontology    = (*pulse.Pulse).Ontology
	_ func(*pulse.Pulse) []pulse.SkillMetadata  = (*pulse.Pulse).Skills
	_ func(*pulse.Pulse, string) (string, bool) = (*pulse.Pulse).Skill
	_                                           = pulse.SkillMetadata{Name: "op-agg-count", Requires: []string{}}
	_                                           = descriptor.OntologyEdge{Kind: descriptor.OntologyEdgeServesIntent}
	_                                           = descriptor.OntologyNode{Kind: descriptor.OntologyNodeSkill}
)

// Linear-algebra core: Pulse-owned matrix types, the FMA-free
// reference kernels and the gonum-backed routines. No gonum type may appear in any of these spellings.
var (
	_ func(int, int, []float64) (*linalg.Matrix, error)                        = linalg.NewMatrix
	_ func([][]float64) (*linalg.Matrix, error)                                = linalg.NewMatrixFromRows
	_ func(int, []float64) (*linalg.Sym, error)                                = linalg.NewSym
	_ func([][]float64) (*linalg.Sym, error)                                   = linalg.NewSymFromRows
	_ func([]float64) *linalg.Vec                                              = linalg.NewVec
	_ func(*linalg.Sym) (*linalg.Matrix, error)                                = linalg.Cholesky
	_ func(*linalg.Sym, linalg.RidgeSchedule) (*linalg.Matrix, float64, error) = linalg.CholeskyRidge
	_ func() linalg.RidgeSchedule                                              = linalg.DefaultRidgeSchedule
	_ func(*linalg.Sym, *linalg.Vec) (*linalg.Vec, error)                      = linalg.SolveSPD
	_ func(*linalg.Sym) (*linalg.Sym, error)                                   = linalg.InverseSPD
	_ func(int, int, float64) float64                                          = linalg.RankTolerance
	_ float64                                                                  = linalg.Epsilon
	_ float64                                                                  = linalg.DominanceTolerance
	_ func(*linalg.Sym) (*linalg.SymEigenResult, error)                        = linalg.SymEigen
	_ func(*linalg.Matrix) (*linalg.SVDResult, error)                          = linalg.SVD
	_ func(*linalg.Matrix) (*linalg.QRResult, error)                           = linalg.QR
	_ func(*linalg.Matrix, float64) (int, error)                               = linalg.Rank
	_ func(*linalg.Matrix) (float64, error)                                    = linalg.ConditionNumber
	_                                                                          = linalg.SymEigenResult{Values: (*linalg.Vec)(nil), Vectors: (*linalg.Matrix)(nil)}
	_                                                                          = linalg.SVDResult{U: (*linalg.Matrix)(nil), Values: (*linalg.Vec)(nil), V: (*linalg.Matrix)(nil)}
	_                                                                          = linalg.QRResult{Q: (*linalg.Matrix)(nil), R: (*linalg.Matrix)(nil)}
	_ func(*linalg.Sym) (*linalg.SPDFactor, error)                             = linalg.FactorSPD
	_ func(*linalg.SPDFactor) int                                              = (*linalg.SPDFactor).N
	_ func(*linalg.SPDFactor) float64                                          = (*linalg.SPDFactor).ConditionNumber
	_ func(*linalg.SPDFactor, *linalg.Vec) (*linalg.Vec, error)                = (*linalg.SPDFactor).Solve
	_ func(*linalg.SPDFactor) (*linalg.Sym, error)                             = (*linalg.SPDFactor).Inverse
	_ func(*linalg.Matrix, *linalg.Matrix) (*linalg.Matrix, error)             = linalg.Mul
	_ float64                                                                  = linalg.ConditionTolerance
	_ func(int, linalg.CoMomentMode) (*linalg.CoMoment, error)                 = linalg.NewCoMoment
	_ linalg.CoMomentMode                                                      = linalg.Listwise
	_ linalg.CoMomentMode                                                      = linalg.Pairwise
	_ func(linalg.CoMomentMode) string                                         = linalg.CoMomentMode.String
	_ func(*linalg.CoMoment, []float64, float64)                               = (*linalg.CoMoment).Add
	_ func(*linalg.CoMoment, *linalg.CoMoment) error                           = (*linalg.CoMoment).Merge
	_ func(*linalg.CoMoment) *linalg.CoMoment                                  = (*linalg.CoMoment).Clone
	_ func(*linalg.CoMoment) int                                               = (*linalg.CoMoment).P
	_ func(*linalg.CoMoment) linalg.CoMomentMode                               = (*linalg.CoMoment).Mode
	_ func(*linalg.CoMoment) int64                                             = (*linalg.CoMoment).N
	_ func(*linalg.CoMoment) int64                                             = (*linalg.CoMoment).NWeightInvalid
	_ func(*linalg.CoMoment) float64                                           = (*linalg.CoMoment).W
	_ func(*linalg.CoMoment) float64                                           = (*linalg.CoMoment).NEff
	_ func(*linalg.CoMoment, int, int) int64                                   = (*linalg.CoMoment).PairN
	_ func(*linalg.CoMoment, int, int) float64                                 = (*linalg.CoMoment).PairW
	_ func(*linalg.CoMoment) *linalg.Vec                                       = (*linalg.CoMoment).Mean
	_ func(*linalg.CoMoment, int) *linalg.Sym                                  = (*linalg.CoMoment).Cov
	_ func(*linalg.CoMoment) *linalg.Sym                                       = (*linalg.CoMoment).Corr
	_ func([]*linalg.CoMoment) (*linalg.CoMoment, error)                       = linalg.MergeTree
	_ int                                                                      = linalg.MergeBlockSize
	_                                                                          = perrors.PULSE_MATRIX_SINGULAR
	_                                                                          = perrors.PULSE_MATRIX_SHAPE_MISMATCH
)
