package reglink

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestCheck_Pairs: every family accepts exactly its three links (and an
// empty link, the canonical default); a link of another family or an
// unknown one is PROCESSING_REGRESSION_INVALID_LINK naming the allowed
// set; an unknown family and a non-GLM spec are not this rule's.
func TestCheck_Pairs(t *testing.T) {
	t.Parallel()

	for _, fam := range Families() {
		for _, link := range append(Links(fam), "") {
			if cerr := CheckPair(fam, link); cerr != nil {
				t.Errorf("%s/%q refused: %v", fam, link, cerr)
			}
		}
		for _, link := range append(All(), "probit_x") {
			if contains(Links(fam), link) {
				continue
			}
			cerr := Check(&types.RegressionSpec{Type: types.REG_GLM, Family: fam, Link: link})
			if cerr == nil || cerr.Code != errors.PROCESSING_REGRESSION_INVALID_LINK {
				t.Fatalf("%s/%s: %v, want INVALID_LINK", fam, link, cerr)
			}
			if !reflect.DeepEqual(cerr.Details["allowed"], Links(fam)) || cerr.Details["link"] != link || cerr.Details["family"] != fam {
				t.Errorf("%s/%s details %v", fam, link, cerr.Details)
			}
		}
	}
	if cerr := CheckPair("tweedie", "log"); cerr != nil {
		t.Errorf("unknown family judged here: %v", cerr)
	}
	if cerr := Check(&types.RegressionSpec{Type: types.REG_OLS, Family: "binomial", Link: "log"}); cerr != nil {
		t.Errorf("REG_OLS judged here: %v", cerr)
	}
}

// TestTable: canonical links first, the sorted union is the enum, and
// the tables are copies.
func TestTable(t *testing.T) {
	t.Parallel()

	for fam, want := range map[string]string{"binomial": "logit", "poisson": "log", "gamma": "inverse"} {
		if Canonical(fam) != want || Resolve(fam, "") != want || Resolve(fam, "identity") != "identity" {
			t.Errorf("%s canonical %q", fam, Canonical(fam))
		}
	}
	if got := All(); !reflect.DeepEqual(got, []string{"cloglog", "identity", "inverse", "log", "logit", "probit", "sqrt"}) {
		t.Errorf("All() = %v", got)
	}
	b := ByFamily()
	b["binomial"][0] = "x"
	if Links("binomial")[0] != "logit" {
		t.Error("ByFamily shares its slices")
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
