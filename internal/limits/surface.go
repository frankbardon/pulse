package limits

import (
	"crypto/sha256"
	"encoding/hex"
	stderrors "errors"
	"strconv"
	"strings"
	"time"
)

// DigestPrefix versions the limits_digest algorithm. A change to the
// hashed form (not to a limit value) bumps it.
const DigestPrefix = "lim1:"

// Units a limit value is expressed in, as the manifest reports them.
const (
	UnitNanoseconds = "nanoseconds"
	UnitBytes       = "bytes"
	UnitCount       = "count"
)

// Unit returns the unit a limit's value is expressed in: nanoseconds
// for RequestTimeout, bytes for MaxEstimatedMemory, count otherwise;
// empty for an unknown name.
func Unit(n Name) string {
	switch n {
	case RequestTimeout:
		return UnitNanoseconds
	case MaxEstimatedMemory:
		return UnitBytes
	}
	if _, ok := lookup(n); !ok {
		return ""
	}
	return UnitCount
}

// Default returns the limit's built-in default (nanoseconds for
// RequestTimeout); 0 for an unknown name.
func Default(n Name) int64 {
	f, ok := lookup(n)
	if !ok {
		return 0
	}
	return f.def
}

// Set stores v in the limit's field of l. It reports false (and leaves
// l untouched) for an unknown name.
func Set(l *Limits, n Name, v int64) bool {
	f, ok := lookup(n)
	if !ok {
		return false
	}
	f.set(l, v)
	return true
}

// ErrUnknownName is returned by ParseName for a key that names no
// limit.
var ErrUnknownName = stderrors.New("unknown limit")

// ParseName resolves a snake_case limit key; ErrUnknownName when no
// limit has that name.
func ParseName(s string) (Name, error) {
	if _, ok := lookup(Name(s)); !ok {
		return "", ErrUnknownName
	}
	return Name(s), nil
}

// ParseValue parses one input-layer value of limit n as written in a
// text form (`pulse mcp --limit`, the feature profile's request_timeout
// string): "unlimited" or "-1" is Unlimited, "0" is "use the default";
// request_timeout takes a Go duration ("30s", "2m"), every other limit
// a base-10 integer (underscores allowed: "10_000_000"). A malformed
// value, or any negative other than Unlimited, is refused.
func ParseValue(n Name, s string) (int64, error) {
	if _, ok := lookup(n); !ok {
		return 0, ErrUnknownName
	}
	switch strings.TrimSpace(s) {
	case "unlimited", "-1":
		return Unlimited, nil
	case "0":
		return 0, nil
	case "":
		return 0, stderrors.New("empty value")
	}
	var v int64
	if n == RequestTimeout {
		d, err := time.ParseDuration(strings.TrimSpace(s))
		if err != nil {
			return 0, stderrors.New("not a Go duration (e.g. 30s, 2m) or \"unlimited\"")
		}
		v = int64(d)
	} else {
		i, err := strconv.ParseInt(strings.ReplaceAll(strings.TrimSpace(s), "_", ""), 10, 64)
		if err != nil {
			return 0, stderrors.New("not an integer or \"unlimited\"")
		}
		v = i
	}
	if v < Unlimited {
		return 0, stderrors.New("negative; use -1 or \"unlimited\" for no limit")
	}
	return v, nil
}

// Spell renders an effective limit value in the text form ParseValue
// accepts: "unlimited" for a disabled limit, a Go duration for
// RequestTimeout, a plain integer otherwise — so a reported value can
// be pasted back into `--limit name=value`.
func Spell(n Name, v int64) string {
	if IsUnlimited(v) {
		return "unlimited"
	}
	if n == RequestTimeout {
		return time.Duration(v).String()
	}
	return strconv.FormatInt(v, 10)
}

// Digest returns the limits_digest of the effective limits l:
// DigestPrefix + sha256hex over "name=value\n" lines in declaration
// order. Two instances share a digest iff every effective limit is
// equal; it is independent of the feature-set digest.
func Digest(l Limits) string {
	var b strings.Builder
	for _, f := range fields {
		b.WriteString(string(f.name))
		b.WriteByte('=')
		b.WriteString(strconv.FormatInt(f.get(&l), 10))
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return DigestPrefix + hex.EncodeToString(sum[:])
}
