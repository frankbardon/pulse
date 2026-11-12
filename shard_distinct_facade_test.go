package pulse

import (
	"context"
	"testing"

	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/spf13/afero"
)

// TestShardDistinct_OptionsReachTheWriter: Options.ShardDistinctLimit
// and Options.DisableShardDistinct steer the per-shard distinct counts a
// facade archive write records in `_schema.pulse` (U73 #500).
func TestShardDistinct_OptionsReachTheWriter(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		opts Options
		want encx.DistinctCount
	}{
		"default":  {Options{}, encx.DistinctCount{Status: encx.DistinctExact, Count: 3}},
		"limit 2":  {Options{ShardDistinctLimit: 2}, encx.DistinctCount{Status: encx.DistinctOverLimit, Count: 2}},
		"disabled": {Options{DisableShardDistinct: true}, encx.DistinctCount{Status: encx.DistinctOptedOut}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			memFs := afero.NewMemMapFs()
			createTestPulseFile(t, memFs, "s1.pulse", []string{"x"}, [][]string{{"1.5"}, {"2.5"}, {"3.5"}, {"1.5"}})
			c.opts.FS = memFs
			p, err := New(c.opts)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.CreateShardArchive(context.Background(), "arch.pulse", []string{"s1.pulse"}); err != nil {
				t.Fatal(err)
			}
			f, err := memFs.Open("arch.pulse")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			st, _ := f.Stat()
			arch, err := encx.OpenArchive(f, st.Size())
			if err != nil {
				t.Fatal(err)
			}
			rc, err := arch.Open(encx.ReservedSchemaName)
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()
			_, dist, err := encx.ReadSchemaDocDistinct(rc)
			if err != nil {
				t.Fatal(err)
			}
			if got := dist.Lookup("s1.pulse", "x"); got != c.want {
				t.Errorf("x = %+v, want %+v (fields %+v)", got, c.want, dist.Fields)
			}
		})
	}
}
