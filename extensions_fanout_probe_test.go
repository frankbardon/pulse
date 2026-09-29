package pulse_test

import (
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// requireCodedError is assertCodedError's value-returning sibling, for
// tests that go on to inspect the Details payload.
func requireCodedError(t *testing.T, err error, want perr.Code) *perr.CodedError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %s, got nil", want)
	}
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("expected *errors.CodedError with code %s, got %T: %v", want, err, err)
	}
	if ce.Code != want {
		t.Fatalf("expected code %s, got %s (msg=%q details=%v)", want, ce.Code, ce.Message, ce.Details)
	}
	return ce
}

// multiKeyGrouper implements Grouper + MultiKeyStreamingGrouper — the
// runtime shape of a fan-out grouper: one record contributes to N
// buckets. GROUP_SET_PER_ELEMENT is the built-in equivalent.
type multiKeyGrouper struct{}

func (multiKeyGrouper) Group(records []*processing.Record, field string) (map[string][]*processing.Record, error) {
	_, _ = records, field
	return nil, nil
}

func (multiKeyGrouper) KeysForRow(*processing.Record, string) ([]string, bool, error) {
	return []string{"a", "b"}, true, nil
}

func multiKeyGrouperFactory(*types.Group, *encoding.Schema) (processing.Grouper, error) {
	return multiKeyGrouper{}, nil
}

// singleKeyGrouperFactory returns a grouper that maps each record to
// exactly one key (stubGrouper implements StreamingGrouper only).
func singleKeyGrouperFactory(*types.Group, *encoding.Schema) (processing.Grouper, error) {
	return stubGrouper{}, nil
}

// TestExtensions_ProbeGrouper_FanOutClaimVerified drives all four
// (declared, observed) combinations through pulse.New. The two
// agreeing pairs must register cleanly; the two disagreeing pairs must
// be refused with PULSE_EXTENSION_FANOUT_MISMATCH. Without the probe a
// declared-false multi-key grouper registers silently and every
// per-record-denominator gate reads it as single-key.
func TestExtensions_ProbeGrouper_FanOutClaimVerified(t *testing.T) {
	cases := []struct {
		name     string
		regName  types.GroupType
		fansOut  bool
		factory  processing.GrouperFactory
		wantCode perr.Code // "" means the registration must be accepted
	}{
		{
			name:    "declared true, factory multi-key",
			regName: "GROUP_ACME_FANOUT_OK",
			fansOut: true,
			factory: multiKeyGrouperFactory,
		},
		{
			name:    "declared false, factory single-key",
			regName: "GROUP_ACME_SINGLE_OK",
			fansOut: false,
			factory: singleKeyGrouperFactory,
		},
		{
			name:     "declared true, factory single-key",
			regName:  "GROUP_ACME_OVERCLAIM_BAD",
			fansOut:  true,
			factory:  singleKeyGrouperFactory,
			wantCode: perr.PULSE_EXTENSION_FANOUT_MISMATCH,
		},
		{
			name:     "declared false, factory multi-key",
			regName:  "GROUP_ACME_UNDERCLAIM_BAD",
			fansOut:  false,
			factory:  multiKeyGrouperFactory,
			wantCode: perr.PULSE_EXTENSION_FANOUT_MISMATCH,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ext := pulse.Extensions{
				Groupers: []pulse.GrouperRegistration{{
					Name:    tc.regName,
					Factory: tc.factory,
					FansOut: tc.fansOut,
				}},
			}
			_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: ext})
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("coherent fan-out registration rejected: %v", err)
				}
				return
			}
			assertCodedError(t, err, tc.wantCode)
		})
	}
}

// TestExtensions_ProbeGrouper_FanOutOmittedDefaultsFalse pins the
// default. An embedder who forgets the field gets false, so a
// multi-key factory is REFUSED rather than silently admitted as
// single-key — the safe direction for the default to fail in.
func TestExtensions_ProbeGrouper_FanOutOmittedDefaultsFalse(t *testing.T) {
	t.Run("multi-key factory without the field is refused", func(t *testing.T) {
		ext := pulse.Extensions{
			Groupers: []pulse.GrouperRegistration{{
				Name:    "GROUP_ACME_OMITTED_MULTI",
				Factory: multiKeyGrouperFactory,
			}},
		}
		_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: ext})
		assertCodedError(t, err, perr.PULSE_EXTENSION_FANOUT_MISMATCH)
	})

	t.Run("single-key factory without the field is accepted", func(t *testing.T) {
		ext := pulse.Extensions{
			Groupers: []pulse.GrouperRegistration{{
				Name:    "GROUP_ACME_OMITTED_SINGLE",
				Factory: singleKeyGrouperFactory,
			}},
		}
		if _, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: ext}); err != nil {
			t.Fatalf("single-key registration omitting FansOut rejected: %v", err)
		}
	})
}

// TestExtensions_ProbeGrouper_FanOutMismatchDetails asserts the details
// payload names the operator plus BOTH values, matching the shape
// PULSE_EXTENSION_STREAMABLE_MISMATCH uses. A bare code cannot tell an
// embedder which half of the disagreement to change.
func TestExtensions_ProbeGrouper_FanOutMismatchDetails(t *testing.T) {
	cases := []struct {
		name         string
		regName      string
		fansOut      bool
		factory      processing.GrouperFactory
		wantDeclared bool
		wantObserved bool
	}{
		{
			name:         "overclaim",
			regName:      "GROUP_ACME_OVERCLAIM_DETAIL",
			fansOut:      true,
			factory:      singleKeyGrouperFactory,
			wantDeclared: true,
			wantObserved: false,
		},
		{
			name:         "underclaim",
			regName:      "GROUP_ACME_UNDERCLAIM_DETAIL",
			fansOut:      false,
			factory:      multiKeyGrouperFactory,
			wantDeclared: false,
			wantObserved: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ext := pulse.Extensions{
				Groupers: []pulse.GrouperRegistration{{
					Name:    types.GroupType(tc.regName),
					Factory: tc.factory,
					FansOut: tc.fansOut,
				}},
			}
			_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: ext})
			ce := requireCodedError(t, err, perr.PULSE_EXTENSION_FANOUT_MISMATCH)

			if got := ce.Details["category"]; got != "grouper" {
				t.Errorf("details[category] = %v, want %q", got, "grouper")
			}
			if got := ce.Details["name"]; got != tc.regName {
				t.Errorf("details[name] = %v, want %q", got, tc.regName)
			}
			if got := ce.Details["declared"]; got != tc.wantDeclared {
				t.Errorf("details[declared] = %v, want %v", got, tc.wantDeclared)
			}
			if got := ce.Details["observed"]; got != tc.wantObserved {
				t.Errorf("details[observed] = %v, want %v", got, tc.wantObserved)
			}
		})
	}
}

// TestExtensions_ProbeGrouper_FanOutPanicStaysFactoryPanic pins the
// precedence: a factory that panics never reaches the fan-out
// assertion, so the embedder sees the panic code rather than a
// mismatch derived from a nil instance.
func TestExtensions_ProbeGrouper_FanOutPanicStaysFactoryPanic(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fansOut bool
	}{
		{name: "declared false", fansOut: false},
		{name: "declared true", fansOut: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ext := pulse.Extensions{
				Groupers: []pulse.GrouperRegistration{{
					Name:    "GROUP_ACME_PANICKY_FANOUT",
					FansOut: tc.fansOut,
					Factory: func(*types.Group, *encoding.Schema) (processing.Grouper, error) {
						panic("boom")
					},
				}},
			}
			_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: ext})
			assertCodedError(t, err, perr.PULSE_EXTENSION_FACTORY_PANIC)
		})
	}
}
