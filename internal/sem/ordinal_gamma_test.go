package sem

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"testing"
)

// gammaRef is the gamma block of a fit_lavaan_polychoric.R reference:
// lavaan's muthen1984 pieces on the (replicated) rows.
type gammaRef struct {
	Gamma *struct {
		N      int         `json:"n"`
		A11    [][]float64 `json:"a11"`
		A21    [][]float64 `json:"a21"`
		A22    []float64   `json:"a22"`
		WLSW   [][]float64 `json:"wls_w"`
		WLSObs []float64   `json:"wls_obs"`
	} `json:"gamma"`
}

// matrixClose fails every entry of got (times scale) outside
// tol·max(1, |want|) of want's and returns their count.
func matrixClose(t *testing.T, what string, got, want [][]float64, scale, tol float64) int {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d rows, lavaan %d", what, len(got), len(want))
	}
	bad := 0
	for i := range want {
		if len(got[i]) != len(want[i]) {
			t.Fatalf("%s row %d: %d columns, lavaan %d", what, i, len(got[i]), len(want[i]))
		}
		for j := range want[i] {
			g, w := scale*got[i][j], scale*want[i][j]
			if math.Abs(g-w) > tol*math.Max(1, math.Abs(w)) {
				if bad < 5 {
					t.Errorf("%s[%d][%d] = %.12g, lavaan %.12g", what, i, j, got[i][j], want[i][j])
				}
				bad++
			}
		}
	}
	return bad
}

// TestWLSMV_GammaMatchesLavaan holds every piece of the asymptotic
// covariance — A11 (a numeric column's observed information included),
// A21 and A22 (a pair touching a numeric column: the observed second
// derivatives), WLS.W after the correlation → covariance map and the
// numeric-mean sign flip — and WLS.obs to lavaan's muthen1984 on every
// ungrouped fixture: the brand survey (nine ratings and the numeric
// recommend), the extreme, sparse and binary edge sets and the
// frequency-weighted one (lavaan sees replicated rows). Evaluated at
// LAVAAN'S estimates the pieces agree within 1e-9 (WLS.W within 1e-10
// on the correlation scale, covClose); at the port's own estimates
// (lavaan's nlminb stop is ~1e-7 off the root) within 1e-5.
func TestWLSMV_GammaMatchesLavaan(t *testing.T) {
	t.Parallel()

	for _, fx := range twoStepFixtures {
		var gr gammaRef
		readJSON(t, "testdata/"+fx.ref+"_lavaan.json", &gr)
		if gr.Gamma == nil {
			continue
		}
		t.Run(fx.ref, func(t *testing.T) {
			ref, in, ss := fitTwoStepFixture(t, fx.ref, fx.data)
			want := gr.Gamma
			at := atLavaan(ss[0], ref)
			g, err := at.Gamma(in)
			if err != nil {
				t.Fatal(err)
			}
			if g.N != float64(want.N) {
				t.Fatalf("N = %v, lavaan %d", g.N, want.N)
			}
			matrixClose(t, "A11", g.A11, want.A11, 1, 1e-9)
			matrixClose(t, "A21", g.A21, want.A21, 1, 1e-9)
			matrixClose(t, "A22", [][]float64{g.A22}, [][]float64{want.A22}, 1, 1e-9)
			covClose(t, "WLS.W", g.WLSW, want.WLSW, 1e-10)
			matrixClose(t, "WLS.obs", [][]float64{at.WLSObs()}, [][]float64{want.WLSObs}, 1, 1e-12)
			own, err := ss[0].Gamma(in)
			if err != nil {
				t.Fatal(err)
			}
			covClose(t, "WLS.W (own estimates)", own.WLSW, want.WLSW, 1e-5)
			matrixClose(t, "WLS.obs (own estimates)", [][]float64{ss[0].WLSObs()}, [][]float64{want.WLSObs}, 1, 1e-6)
			nacov := own.NACOV()
			for i := range nacov {
				if nacov[i][i] <= 0 {
					t.Errorf("NACOV[%d][%d] = %v, want > 0", i, i, nacov[i][i])
				}
			}
		})
	}
}

// covClose holds a covariance matrix entry by entry on its correlation
// scale: |got − want| ≤ tol·√(want_ii·want_jj). A pair pinned at the
// ±0.999 bound beside an empty cell has an asymptotic variance near
// 1e10 (poly_binary), whose cross entries a plain relative test would
// judge on catastrophic cancellation alone.
func covClose(t *testing.T, what string, got, want [][]float64, tol float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: %d rows, lavaan %d", what, len(got), len(want))
	}
	bad := 0
	for i := range want {
		for j := range want[i] {
			if math.Abs(got[i][j]-want[i][j]) > tol*math.Sqrt(want[i][i]*want[j][j]) {
				if bad < 5 {
					t.Errorf("%s[%d][%d] = %.12g, lavaan %.12g", what, i, j, got[i][j], want[i][j])
				}
				bad++
			}
		}
	}
}

// TestWLSMV_GammaGoldenBits pins a SHA-256 of every WLS.obs entry and
// every N·WLS.W entry of the ungrouped fixtures. The Gamma kernel is
// FMA-free like the rest of the two-step numerics
// (TestPolychoric_NoFusibleProducts scans ordinal_gamma.go too) and
// inverts through linalg.InverseSPD, a reference kernel, so the pins
// hold on amd64 and arm64 alike. Never regenerate them for a refactor;
// a deliberate numerical change prints the new digests on failure.
func TestWLSMV_GammaGoldenBits(t *testing.T) {
	t.Parallel()

	digests := map[string]string{}
	for _, fx := range twoStepFixtures {
		var gr gammaRef
		readJSON(t, "testdata/"+fx.ref+"_lavaan.json", &gr)
		if gr.Gamma == nil {
			continue
		}
		_, in, ss := fitTwoStepFixture(t, fx.ref, fx.data)
		g, err := ss[0].Gamma(in)
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.New()
		put := func(v float64) {
			var b [8]byte
			binary.LittleEndian.PutUint64(b[:], math.Float64bits(v))
			h.Write(b[:])
		}
		for _, v := range ss[0].WLSObs() {
			put(v)
		}
		for _, row := range g.NACOV() {
			for _, v := range row {
				put(v)
			}
		}
		digests[fx.ref] = hex.EncodeToString(h.Sum(nil))
	}
	want := map[string]string{
		"brand_twostep": "e16d91f8c523b4d208d44d4c704cdd7c570b0a971cbb548fc5fc589cf28f1c5b",
		"poly_extreme":  "b66e6301f51b9d9e461f25c9da694933a1105aad9b3fb873d18be73b4db5c4e7",
		"poly_sparse":   "afd93c9eb9228455c27054d71ac332143132b6e23b9a1b0fb4790ed7b8a5e921",
		"poly_binary":   "9ccff814395c5deedb2e0c5eba39ac4b8d35883776222a8207296614ac431a36",
		"poly_weighted": "52283d73ee5352851fbfea39f329e1c6fba0371a5e7dffe10ccd0341e3aec2db",
	}
	if len(digests) != len(want) {
		t.Errorf("%d fixtures carry a gamma block, %d are pinned", len(digests), len(want))
	}
	for name, got := range digests {
		if want[name] != got {
			t.Errorf("%q: %q,", name, got)
		}
	}
}
