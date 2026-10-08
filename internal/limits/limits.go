// Package limits is the one definition of Pulse's instance resource
// limits: the Limits struct the root facade aliases as pulse.Limits,
// the built-in defaults, the per-field precedence resolver, the
// construction-time validation and the shared constructors every
// PULSE_LIMIT_* raise site uses.
//
// It is a leaf over the standard library and Pulse's errors package so
// that predict (internal/descriptor, no-execute) and the runtime
// (internal/service, internal/processing) can both import it and agree
// on every rule.
//
// Encoding, identical on every input layer (Options, feature profile,
// --limit): 0 means "use the default", Unlimited (-1) means no limit,
// and any other negative value is refused. An EFFECTIVE (resolved)
// Limits never carries 0: every field is either a positive bound or
// Unlimited.
package limits

import (
	"strconv"
	"strings"
	"time"

	"github.com/frankbardon/pulse/errors"
)

// Unlimited disables a limit. Untyped, so it is assignable to both the
// time.Duration and the int64 fields.
const Unlimited = -1

// Built-in defaults. RequestTimeout and MaxEstimatedMemory are opt-in
// (Unlimited by default); the others are set high enough that no
// legitimate request trips them.
const (
	DefaultRequestTimeout     time.Duration = Unlimited
	DefaultMaxGroups          int64         = 10_000_000
	DefaultMaxCrosstabCells   int64         = 10_000_000
	DefaultMaxEstimatedMemory int64         = Unlimited
	DefaultMaxMatrixDim       int64         = 2_048
	DefaultMaxComposeSlots    int64         = 1_000
	DefaultMaxChainStages     int64         = 1_000
	DefaultMaxJoinBuildRows   int64         = 100_000_000
)

// Limits holds the instance resource limits. On an input layer a zero
// field means "use the default" and Unlimited (-1) means no limit; any
// other negative value is refused at pulse.New with PULSE_LIMIT_INVALID.
// Requests can never override an instance limit.
type Limits struct {
	// RequestTimeout bounds the wall-clock time of one top-level call
	// (Process, ProcessStream / ProcessStreamResult, Facet /
	// FacetSchema, the whole Compose / ComposeParallel / ProcessChain
	// call). A trip is PULSE_LIMIT_EXCEEDED; the caller's own ctx
	// deadline or cancel passes through unchanged. Default: no timeout.
	RequestTimeout time.Duration

	// MaxGroups bounds the number of distinct group buckets one grouped
	// run may create. Default 10,000,000.
	MaxGroups int64

	// MaxCrosstabCells bounds rows x columns of one crosstab. Default
	// 10,000,000.
	MaxCrosstabCells int64

	// MaxEstimatedMemory bounds the predicted in-memory footprint of a
	// run, in bytes. The estimate is an upper bound. Default: no limit.
	MaxEstimatedMemory int64

	// MaxMatrixDim bounds the dimension p of one requested matrix.
	// Default 2,048.
	MaxMatrixDim int64

	// MaxComposeSlots bounds the number of requests in one Compose call.
	// Default 1,000.
	MaxComposeSlots int64

	// MaxChainStages bounds the number of stages in one ProcessChain
	// call. Default 1,000.
	MaxChainStages int64

	// MaxJoinBuildRows bounds the record count of a join's build (right)
	// side, which is held in memory. Default 100,000,000.
	MaxJoinBuildRows int64
}

// Name is a limit's snake_case key — the spelling the feature profile
// `limits` section, `pulse mcp --limit` and every PULSE_LIMIT_* error's
// `limit` detail use.
type Name string

// The limit names, in declaration order.
const (
	RequestTimeout     Name = "request_timeout"
	MaxGroups          Name = "max_groups"
	MaxCrosstabCells   Name = "max_crosstab_cells"
	MaxEstimatedMemory Name = "max_estimated_memory"
	MaxMatrixDim       Name = "max_matrix_dim"
	MaxComposeSlots    Name = "max_compose_slots"
	MaxChainStages     Name = "max_chain_stages"
	MaxJoinBuildRows   Name = "max_join_build_rows"
)

// field binds a Name to its Go field.
type field struct {
	name   Name
	goName string
	get    func(*Limits) int64
	set    func(*Limits, int64)
	def    int64
}

var fields = []field{
	{RequestTimeout, "RequestTimeout",
		func(l *Limits) int64 { return int64(l.RequestTimeout) },
		func(l *Limits, v int64) { l.RequestTimeout = time.Duration(v) },
		int64(DefaultRequestTimeout)},
	{MaxGroups, "MaxGroups",
		func(l *Limits) int64 { return l.MaxGroups },
		func(l *Limits, v int64) { l.MaxGroups = v },
		DefaultMaxGroups},
	{MaxCrosstabCells, "MaxCrosstabCells",
		func(l *Limits) int64 { return l.MaxCrosstabCells },
		func(l *Limits, v int64) { l.MaxCrosstabCells = v },
		DefaultMaxCrosstabCells},
	{MaxEstimatedMemory, "MaxEstimatedMemory",
		func(l *Limits) int64 { return l.MaxEstimatedMemory },
		func(l *Limits, v int64) { l.MaxEstimatedMemory = v },
		DefaultMaxEstimatedMemory},
	{MaxMatrixDim, "MaxMatrixDim",
		func(l *Limits) int64 { return l.MaxMatrixDim },
		func(l *Limits, v int64) { l.MaxMatrixDim = v },
		DefaultMaxMatrixDim},
	{MaxComposeSlots, "MaxComposeSlots",
		func(l *Limits) int64 { return l.MaxComposeSlots },
		func(l *Limits, v int64) { l.MaxComposeSlots = v },
		DefaultMaxComposeSlots},
	{MaxChainStages, "MaxChainStages",
		func(l *Limits) int64 { return l.MaxChainStages },
		func(l *Limits, v int64) { l.MaxChainStages = v },
		DefaultMaxChainStages},
	{MaxJoinBuildRows, "MaxJoinBuildRows",
		func(l *Limits) int64 { return l.MaxJoinBuildRows },
		func(l *Limits, v int64) { l.MaxJoinBuildRows = v },
		DefaultMaxJoinBuildRows},
}

func lookup(n Name) (field, bool) {
	for _, f := range fields {
		if f.name == n {
			return f, true
		}
	}
	return field{}, false
}

// Names returns every limit name in declaration order.
func Names() []Name {
	out := make([]Name, len(fields))
	for i, f := range fields {
		out[i] = f.name
	}
	return out
}

// Option returns the Go spelling of the limit's option,
// "Options.Limits.<Field>"; empty for an unknown name.
func Option(n Name) string {
	f, ok := lookup(n)
	if !ok {
		return ""
	}
	return "Options.Limits." + f.goName
}

// Value returns the limit's field from l as an int64 (nanoseconds for
// RequestTimeout); 0 for an unknown name.
func Value(l Limits, n Name) int64 {
	f, ok := lookup(n)
	if !ok {
		return 0
	}
	return f.get(&l)
}

// Defaults returns the built-in effective limits.
func Defaults() Limits {
	var l Limits
	for _, f := range fields {
		f.set(&l, f.def)
	}
	return l
}

// Resolve returns the effective limits, field by field: a non-zero opts
// value wins, else a non-zero profile value, else the built-in default.
// profile may be nil (no feature profile, or a profile without a
// `limits` section). Pure: neither input is validated or modified —
// Validate the inputs first.
func Resolve(opts Limits, profile *Limits) Limits {
	var out Limits
	for _, f := range fields {
		v := f.get(&opts)
		if v == 0 && profile != nil {
			v = f.get(profile)
		}
		if v == 0 {
			v = f.def
		}
		f.set(&out, v)
	}
	return out
}

// Validate refuses the first field holding a negative value other than
// Unlimited with PULSE_LIMIT_INVALID; nil when every field is valid.
func Validate(l Limits) error {
	for _, f := range fields {
		if v := f.get(&l); v < Unlimited {
			return Invalid(f.name, v)
		}
	}
	return nil
}

// IsUnlimited reports whether an effective limit value disables the
// limit (Unlimited, or a non-positive value that never resolves).
func IsUnlimited(v int64) bool { return v <= 0 }

// Invalid builds the PULSE_LIMIT_INVALID error for a refused value.
// Details: {limit, value}.
func Invalid(n Name, value int64) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_LIMIT_INVALID,
		"pulse: "+Option(n)+" is "+format(n, value)+
			"; use 0 for the default, -1 (pulse.Unlimited) for no limit, or a positive value",
		map[string]any{
			"limit": string(n),
			"value": value,
		})
}

// Exceeded builds the PULSE_LIMIT_EXCEEDED error every raise site
// returns, so the details are identical everywhere and the message
// carries the configured value. Details: {limit, configured, observed,
// option}; RequestTimeout values are nanoseconds.
func Exceeded(n Name, configured, observed int64) *errors.CodedError {
	msg := "limit " + string(n) + " exceeded: observed " + format(n, observed) +
		", configured " + format(n, configured) +
		" (raise " + Option(n) + ", feature profile limits." + string(n) +
		" or pulse mcp --limit " + string(n) + "=, or reduce the request)"
	return errors.NewCodedErrorWithDetails(errors.PULSE_LIMIT_EXCEEDED, msg,
		map[string]any{
			"limit":      string(n),
			"configured": configured,
			"observed":   observed,
			"option":     Option(n),
		})
}

// format renders a limit value for a message: a Go duration for
// RequestTimeout, a thousands-separated integer otherwise.
func format(n Name, v int64) string {
	if n == RequestTimeout {
		return time.Duration(v).String()
	}
	return groupThousands(v)
}

func groupThousands(v int64) string {
	s := strconv.FormatInt(v, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}
