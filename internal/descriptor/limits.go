package descriptor

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// RequestLimitFindings is the one predict-time limit rule for a single
// Request against the schema it executes over: every effective limit in
// l (predict passes the snapshot's, the runtime the service's — the
// same resolved values pulse.New installs on both) a figure predict can
// compute from the request + schema exceeds, graded
// limits.Certain or limits.Possible, in limit-declaration order.
// Predict reports them (PredictResult.LimitFindings); the process
// pre-flight (LimitRefusal) refuses the first Certain one before any
// record is decoded. Reads only the schema.
//
//   - MaxMatrixDim (Certain): each matrix's dimension p — its resolved
//     member count, from vectors.ResolveMatrices, the resolver the
//     runtime mints its accumulators from. A refused spec yields no
//     finding (the field-reference pass reports the refusal).
func RequestLimitFindings(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, l limits.Limits) []limits.Finding {
	if req == nil {
		return nil
	}
	var out []limits.Finding
	if len(req.Matrices) > 0 {
		plans, err := vectors.ResolveMatrices(req, schema, func(t types.MatrixType) bool {
			return isBuiltinMatrixType(opRoute(inst, t))
		})
		if err == nil {
			for _, m := range plans {
				if f, ok := limits.Evaluate(l, limits.MaxMatrixDim, int64(len(m.Members.Members)), limits.Certain); ok {
					out = append(out, f)
				}
			}
		}
	}
	return out
}

// LimitRefusal is the process pre-flight: the PULSE_LIMIT_EXCEEDED
// error of the first Certain RequestLimitFindings finding, nil when
// none. The runtime calls it right after the field-reference pass, so a
// certain breach is refused before any record is decoded — with the
// error predict reports for the same request.
func LimitRefusal(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, l limits.Limits) *errors.CodedError {
	return limits.FirstCertain(RequestLimitFindings(req, schema, inst, l))
}

// limitFindingsDescriptor projects findings onto the public
// PredictResult shape (nil when there are none, so the field is
// omitted).
func limitFindingsDescriptor(fs []limits.Finding) []descriptor.LimitFinding {
	if len(fs) == 0 {
		return nil
	}
	out := make([]descriptor.LimitFinding, len(fs))
	for i, f := range fs {
		out[i] = descriptor.LimitFinding{
			Limit:      string(f.Limit),
			Configured: f.Configured,
			Estimated:  f.Estimated,
			Grade:      descriptor.LimitGrade(f.Grade),
		}
	}
	return out
}

// joinBuildLimitFinding is the predict-time MaxJoinBuildRows rule: a
// Certain finding when req carries exactly one JoinSpec whose right
// (build) side holds more records than l allows. rightRows is the
// count opts.RecordCounter reads from the right cohort's header — the
// runtime decodes the right side unfiltered, so it is the exact build
// size. The runtime raises the same breach through
// limits.CheckJoinBuildRows on the same header-only count before the
// build decodes a record.
func joinBuildLimitFinding(req *types.Request, l limits.Limits, rightRows func(path string) (int64, error)) (limits.Finding, bool) {
	if req == nil || len(req.Joins) != 1 || req.Joins[0] == nil || rightRows == nil {
		return limits.Finding{}, false
	}
	if limits.IsUnlimited(limits.Value(l, limits.MaxJoinBuildRows)) {
		return limits.Finding{}, false
	}
	n, err := rightRows(req.Joins[0].Right)
	if err != nil {
		// The right cohort's open fault is reported by the joined-schema
		// pass (predictJoinedSchema) under its own code.
		return limits.Finding{}, false
	}
	return limits.Evaluate(l, limits.MaxJoinBuildRows, n, limits.Certain)
}

// predictLimits fills PredictResult.LimitFindings and adds the
// PULSE_LIMIT_EXCEEDED error of every Certain finding — the error the
// process pre-flight raises — so a certain finding leaves Valid false.
// A Possible finding is reported only. Findings follow limit
// declaration order: the request-level rules, then MaxJoinBuildRows.
func predictLimits(env *descriptor.Envelope, result *descriptor.PredictResult, req *types.Request, schema *encoding.Schema, opts *PredictOptions) {
	inst := opts.instance()
	l := inst.Limits()
	fs := RequestLimitFindings(req, schema, inst, l)
	if f, ok := joinBuildLimitFinding(req, l, opts.RecordCounter); ok {
		fs = append(fs, f)
	}
	result.LimitFindings = limitFindingsDescriptor(fs)
	for _, f := range fs {
		if f.Grade == limits.Certain {
			addCodedError(env, f.Err())
		}
	}
}
