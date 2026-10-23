package cli

import (
	"bytes"
	"context"
	stderrors "errors"
	"path/filepath"
	"strings"
	"testing"

	cli "github.com/urfave/cli/v3"

	perr "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
)

// The pins in this file were written BEFORE the closed flag
// vocabularies moved onto library listers / parsers (shell-completion
// E2-S1): every spelling a flag accepted then it still accepts, and
// every refusal keeps its code (or stays uncoded).

// runDSTPolicy parses --dst-policy through the shared source-zone flag
// set, returning what parseSourceZoneFlags returned.
func runDSTPolicy(t *testing.T, args ...string) (pio.DSTPolicy, error) {
	t.Helper()
	var got pio.DSTPolicy
	var perr error
	cmd := &cli.Command{
		Name:  "zone",
		Flags: sourceZoneCLIFlags(),
		Action: func(_ context.Context, cmd *cli.Command) error {
			f, err := parseSourceZoneFlags(cmd)
			got, perr = f.policy, err
			return nil
		},
	}
	if err := cmd.Run(context.Background(), append([]string{"zone"}, args...)); err != nil {
		t.Fatalf("run: %v", err)
	}
	return got, perr
}

func TestFlagVocab_DSTPolicyPin(t *testing.T) {
	for _, in := range []string{"error", "earlier", "later"} {
		got, err := runDSTPolicy(t, "--dst-policy", in)
		if err != nil || got != pio.DSTPolicy(in) {
			t.Errorf("--dst-policy %q = (%q, %v), want (%q, nil)", in, got, err, in)
		}
	}
	if got, err := runDSTPolicy(t, "--dst-policy", ""); err != nil || got != "" {
		t.Errorf("--dst-policy \"\" = (%q, %v), want the library default (empty)", got, err)
	}
	if got, err := runDSTPolicy(t); err != nil || got != pio.DSTPolicyError {
		t.Errorf("flag default = (%q, %v), want error", got, err)
	}
	for _, in := range []string{"Error", "late", " later", "none"} {
		_, err := runDSTPolicy(t, "--dst-policy", in)
		var ce *perr.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perr.CLI_INPUT {
			t.Fatalf("--dst-policy %q: err %v, want CLI_INPUT", in, err)
		}
		if want := `--dst-policy "` + in + `": want error, earlier or later`; ce.Message != want {
			t.Errorf("message %q, want %q", ce.Message, want)
		}
		if ce.Details["flag"] != "dst-policy" || ce.Details["value"] != in {
			t.Errorf("details %v", ce.Details)
		}
	}
}

func TestFlagVocab_LookupModePin(t *testing.T) {
	cases := map[string]types.LookupMultiplicity{
		"assert-unique": types.LookupMultiplicityAssertUnique,
		"assert_unique": types.LookupMultiplicityAssertUnique,
		"first":         types.LookupMultiplicityFirst,
		"all":           types.LookupMultiplicityAll,
	}
	for in, want := range cases {
		got, err := translateLookupMode(in)
		if err != nil || got != want {
			t.Errorf("--mode %q = (%q, %v), want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "First", "ALL", "assert unique", "any"} {
		_, err := translateLookupMode(in)
		if err == nil {
			t.Fatalf("--mode %q accepted", in)
		}
		var ce *perr.CodedError
		if stderrors.As(err, &ce) {
			t.Errorf("--mode %q: refusal became coded (%s); it was uncoded", in, ce.Code)
		}
		if !strings.HasPrefix(err.Error(), `invalid --mode "`+in+`", want `) {
			t.Errorf("--mode %q: message %q", in, err.Error())
		}
	}
}

func TestFlagVocab_LabelModePin(t *testing.T) {
	cases := map[string]types.LabelMode{
		"f=t":           types.LabelModeReplace,
		"f=t:":          types.LabelModeReplace,
		"f=t:replace":   types.LabelModeReplace,
		"f=t: replace ": types.LabelModeReplace,
		"f=t:augment":   types.LabelModeAugment,
	}
	for in, want := range cases {
		got, err := parseLabelBindings([]string{in})
		if err != nil || len(got) != 1 || got[0].Mode != want || got[0].Table != "t" {
			t.Errorf("--labels %q = (%+v, %v), want mode %q", in, got, err, want)
		}
	}
	for _, mode := range []string{"Augment", "REPLACE", "highlight"} {
		_, err := parseLabelBindings([]string{"f=t:" + mode})
		if err == nil {
			t.Fatalf("mode %q accepted", mode)
		}
		var ce *perr.CodedError
		if stderrors.As(err, &ce) {
			t.Errorf("mode %q: refusal became coded (%s)", mode, ce.Code)
		}
		if want := `labels[0]: unknown mode "` + mode + `" (expected replace or augment)`; err.Error() != want {
			t.Errorf("message %q, want %q", err.Error(), want)
		}
	}
}

// runExplain runs the explain leaf and returns its error text.
func runExplain(t *testing.T, args ...string) string {
	t.Helper()
	cmd := ExplainCommand()
	var buf bytes.Buffer
	cmd.Writer = &buf
	err := cmd.Run(context.Background(), append([]string{"explain"}, args...))
	if err == nil {
		t.Fatalf("explain %v: no error", args)
	}
	return err.Error()
}

func TestFlagVocab_ExplainRootPin(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.json")
	for _, root := range []string{"request", "composed", "chain", "facet", "sample"} {
		msg := runExplain(t, "--root", root, "--request", missing)
		if strings.Contains(msg, "--root") || !strings.Contains(msg, "--request "+missing) {
			t.Errorf("--root %s refused or misrouted: %s", root, msg)
		}
	}
	for _, root := range []string{"Request", "response", "composed_response", "facet_result", ""} {
		msg := runExplain(t, "--root", root, "--request", missing)
		if want := "CLI_INPUT: --root " + root + " is not one of request, composed, chain, facet, sample"; msg != want {
			t.Errorf("--root %q: %q, want %q", root, msg, want)
		}
	}
	if msg := runExplain(t, "--root", "sample", "--response", missing); msg != "CLI_INPUT: --root sample has no response to read; a sample result is rows, not findings" {
		t.Errorf("sample response: %q", msg)
	}
	if msg := runExplain(t); msg != "CLI_INPUT: usage: pulse explain --request FILE | --response FILE [--request FILE] [--root request|composed|chain|facet|sample] [--detail terse|full]" {
		t.Errorf("usage: %q", msg)
	}
}
