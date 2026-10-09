package pulse

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
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
	if !stderrors.As(err, &ce) || ce.Code != errors.DATA_FILE {
		t.Errorf("missing cohort: err = %v, want DATA_FILE", err)
	}
	// The request is checked before the cohort is opened.
	_, err = p.Recommend(context.Background(), descriptor.RecommendRequest{Intent: "nope", Cohort: &types.Cohort{Filename: "x.pulse"}})
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_RECOMMEND_INTENT_UNKNOWN {
		t.Errorf("unknown intent with a cohort: err = %v", err)
	}
}

// readSpyFs records, per file, the furthest byte any Read or ReadAt
// reached, so a test can prove a payload was never touched.
type readSpyFs struct {
	afero.Fs
	mu      sync.Mutex
	maxRead map[string]int64
}

func (s *readSpyFs) note(name string, end int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if end > s.maxRead[name] {
		s.maxRead[name] = end
	}
}

func (s *readSpyFs) Open(name string) (afero.File, error) {
	f, err := s.Fs.Open(name)
	if err != nil {
		return nil, err
	}
	return &readSpyFile{File: f, fs: s, name: name}, nil
}

func (s *readSpyFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	f, err := s.Fs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	return &readSpyFile{File: f, fs: s, name: name}, nil
}

type readSpyFile struct {
	afero.File
	fs   *readSpyFs
	name string
}

func (f *readSpyFile) Read(b []byte) (int, error) {
	pos, _ := f.File.Seek(0, io.SeekCurrent)
	n, err := f.File.Read(b)
	f.fs.note(f.name, pos+int64(n))
	return n, err
}

func (f *readSpyFile) ReadAt(b []byte, off int64) (int, error) {
	n, err := f.File.ReadAt(b, off)
	f.fs.note(f.name, off+int64(n))
	return n, err
}

// wideRecommendCohort writes a 36-field cohort (32 numeric, 4
// categorical) with rows zeroed records and returns the byte length of
// its header + schema.
func wideRecommendCohort(t *testing.T, fsys afero.Fs, path string, rows int) int64 {
	t.Helper()
	s := &encoding.Schema{}
	for i := range 32 {
		s.Fields = append(s.Fields, encoding.Field{Name: "m" + strconv.Itoa(i), Type: encoding.FieldTypeF64, Description: "Measured amount number " + strconv.Itoa(i)})
	}
	for i := range 4 {
		d := encoding.NewDictionary()
		for _, v := range []string{"x", "y", "z"} {
			if _, err := d.Add(v); err != nil {
				t.Fatal(err)
			}
		}
		s.Fields = append(s.Fields, encoding.Field{Name: "c" + strconv.Itoa(i), Type: encoding.FieldTypeCategoricalU8, Description: "Category label number " + strconv.Itoa(i), Dictionary: d})
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, s); err != nil {
		t.Fatal(err)
	}
	head := int64(buf.Len())
	buf.Write(make([]byte, rows*s.RecordByteSize()))
	if err := afero.WriteFile(fsys, path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return head
}

// TestRecommend_FacadeBoundReadsNoRecord: bound Recommend over a wide
// cohort reads the header and schema only — no byte of the record
// payload — caps the list at the default limit, and every fully bound
// draft passes the facade's own Predict.
func TestRecommend_FacadeBoundReadsNoRecord(t *testing.T) {
	spy := &readSpyFs{Fs: afero.NewMemMapFs(), maxRead: map[string]int64{}}
	head := wideRecommendCohort(t, spy.Fs, "wide.pulse", 500)
	p, err := New(Options{FS: spy})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cohort := &types.Cohort{Filename: "wide.pulse"}
	res, err := p.Recommend(ctx, descriptor.RecommendRequest{Intent: "compare_groups", Cohort: cohort})
	if err != nil {
		t.Fatal(err)
	}
	if got := spy.maxRead["wide.pulse"]; got == 0 || got > head {
		t.Fatalf("Recommend read up to byte %d; header + schema end at %d", got, head)
	}
	full, err := p.Recommend(ctx, descriptor.RecommendRequest{Intent: "compare_groups", Cohort: cohort, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Bound || len(res.Recommendations) != 10 || !res.Truncated || res.CandidatesConsidered != len(full.Recommendations) {
		t.Fatalf("bound=%v n=%d truncated=%v considered=%d (uncapped %d)", res.Bound, len(res.Recommendations), res.Truncated, res.CandidatesConsidered, len(full.Recommendations))
	}
	checked := 0
	for _, r := range full.Recommendations {
		if !r.Bound {
			continue
		}
		var req Request
		if err := json.Unmarshal(r.Request, &req); err != nil {
			t.Fatal(err)
		}
		pr, err := p.Predict(ctx, &req)
		if err != nil || !pr.Valid {
			t.Errorf("%s: draft fails Predict: %v %s", r.Operator, err, r.Request)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("vacuous: no bound draft")
	}
}

// TestRecommend_FacadeBoundSidecarAdvisories: bound drafts carry the
// sidecar-fed advisories the facade's Predict raises, and the
// instance's suppression list drops them.
func TestRecommend_FacadeBoundSidecarAdvisories(t *testing.T) {
	codesFor := func(p *Pulse) []string {
		res, err := p.Recommend(context.Background(), descriptor.RecommendRequest{
			Intent: "describe", Cohort: &types.Cohort{Filename: suggestCohort}, Fields: []string{"REGION"}, Limit: 1000,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range res.Recommendations {
			if r.Operator == "AGG_AVERAGE" && strings.Contains(string(r.Request), `"REGION"`) {
				var out []string
				for _, a := range r.Advisories {
					out = append(out, a.Code)
				}
				return out
			}
		}
		t.Fatal("no AGG_AVERAGE draft over REGION")
		return nil
	}
	if got := codesFor(importSav(t, afero.NewMemMapFs(), measuredSavSpec(), Options{})); !slices.Contains(got, advNominal) {
		t.Errorf("advisories = %v, want %s", got, advNominal)
	}
	sp := importSav(t, afero.NewMemMapFs(), measuredSavSpec(), Options{SuppressAdvisories: []string{advNominal}})
	if got := codesFor(sp); slices.Contains(got, advNominal) {
		t.Errorf("suppressed %s still attached: %v", advNominal, got)
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
