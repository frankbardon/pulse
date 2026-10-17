package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAPICompose_BannerSurfaces: `pulse api compose` runs a banner with
// no new flag — --json carries the stitched `banner` beside the slot
// responses, and --stream (the matrix slots have no rows) ends with
// exactly one {"banner":{...}} record; a banner-free batch streams none.
func TestAPICompose_BannerSurfaces(t *testing.T) {
	dir := withTempDataDir(t)
	cohort := seedCohortViaImport(t, dir, "bncli.csv", "n,cat,g\n1,a,x\n2,b,y\n3,a,y\n4,b,x\n")
	write := func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	base := `{"cohort":{"filename":"` + cohort + `"},"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"cat"}],"cell":{"type":"AGG_SUM","field":"n"}}}`
	path := write("banner.json", `{"banner":{"request":`+base+`,"sections":[{"label":"s"}],"total":true,`+
		`"groups":[{"label":"G","columns":[{"type":"GROUP_CATEGORY","field":"g"}]}]}}`)

	out, err := runAPI(t, "compose", "--json", "-r", path)
	if err != nil {
		t.Fatalf("compose --json: %v\n%s", err, out)
	}
	var env struct {
		Data struct {
			Responses []json.RawMessage `json:"responses"`
			Banner    struct {
				Sections []struct {
					Blocks []struct {
						Slot string `json:"slot"`
					} `json:"blocks"`
				} `json:"sections"`
			} `json:"banner"`
		} `json:"data"`
		Errors []any `json:"errors"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil || len(env.Errors) != 0 {
		t.Fatalf("envelope: %v %v\n%s", err, env.Errors, out)
	}
	if len(env.Data.Responses) != 2 || len(env.Data.Banner.Sections) != 1 || len(env.Data.Banner.Sections[0].Blocks) != 2 ||
		env.Data.Banner.Sections[0].Blocks[1].Slot != "s:G" {
		t.Fatalf("compose --json = %s", out)
	}

	stream := func(p string) []string {
		t.Helper()
		out, err := runAPI(t, "compose", "--stream", "-r", p)
		if err != nil {
			t.Fatalf("--stream: %v\n%s", err, out)
		}
		return strings.Split(strings.TrimSpace(out), "\n")
	}
	lines := stream(path)
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, `{"banner":`) || strings.Count(strings.Join(lines, "\n"), `{"banner":`) != 1 {
		t.Fatalf("--stream lines = %v, want one trailing banner record", lines)
	}
	plain := write("plain.json", `{"requests":[`+base+`]}`)
	if got := strings.Join(stream(plain), "\n"); strings.Contains(got, `"banner"`) {
		t.Fatalf("a banner-free stream carries a banner record: %s", got)
	}
}
