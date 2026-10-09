package pulse

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func TestRecommend_FacadeUnbound(t *testing.T) {
	p, err := New(Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Recommend(context.Background(), descriptor.RecommendRequest{Intent: "compare_groups"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Bound || len(res.Recommendations) == 0 || res.Intent != "compare_groups" {
		t.Fatalf("unexpected result: %+v", res)
	}
	_, err = p.Recommend(context.Background(), descriptor.RecommendRequest{Intent: "nope"})
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_RECOMMEND_INTENT_UNKNOWN {
		t.Errorf("unknown intent: err = %v", err)
	}
	_, err = p.Recommend(context.Background(), descriptor.RecommendRequest{Intent: "describe", Cohort: &types.Cohort{Filename: "x.pulse"}})
	if !stderrors.As(err, &ce) || ce.Code != errors.SERVICE_VALIDATION {
		t.Errorf("cohort-bound: err = %v", err)
	}
}

// TestRecommend_FacadeProfiled: the facade reads the instance snapshot,
// so a profiled instance never names a hidden operator anywhere in the
// result.
func TestRecommend_FacadeProfiled(t *testing.T) {
	const hidden = "TEST_TUKEY_HSD"
	p, err := New(Options{FS: afero.NewMemMapFs(), FeatureProfile: &FeatureProfile{Features: allFeaturesBut(hidden)}})
	if err != nil {
		t.Fatal(err)
	}
	full, err := New(Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	req := descriptor.RecommendRequest{Intent: "compare_groups", Limit: 1000}
	names := func(p *Pulse) string {
		res, err := p.Recommend(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for _, r := range res.Recommendations {
			b.WriteString(r.Operator + " " + string(r.Request) + " ")
			for _, a := range append(r.Alternatives, r.FollowUps...) {
				b.WriteString(a.Use + " ")
			}
		}
		return b.String()
	}
	if !strings.Contains(names(full), hidden) {
		t.Fatalf("fixture drift: %s absent on the default instance", hidden)
	}
	if got := names(p); strings.Contains(got, hidden) {
		t.Errorf("profiled instance names hidden %s", hidden)
	}
}
