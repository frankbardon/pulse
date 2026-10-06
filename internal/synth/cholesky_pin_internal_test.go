package synth

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	stderrors "errors"
	"math"
	mrand "math/rand/v2"
	"slices"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// The bitwise pin on synth's Cholesky factor and on the correlated draw
// it feeds.
//
// # Why this exists
//
// Every correlated synth value — value-scale (buildCorrelator) and
// residual-scale (buildResidualCorrelator) alike — goes through ONE
// factor, built by cholesky via factorCorrelations. Nothing else in the
// suite pins that factor bit-for-bit: swapping it for a factorisation
// that is "the same maths" in a different operation order moves a few
// ulps in most matrices, flips the ok/not-ok decision on roughly a fifth
// of exactly rank-deficient ones (changing the ridge the caller is
// warned about), and every other synth test still passes. A failure here
// is therefore never noise: the factor, the accumulated ridge or the
// draw changed, and a same-seed-same-bytes promise moved with it.
//
// # Why hard-coded digests are portable
//
// The pinned values are architecture-independent BY CONSTRUCTION, and
// that was verified rather than assumed (the suite was run natively on
// arm64 and as GOARCH=amd64, and `-d=fmahash=vy` reports no contraction
// in this file or in copula.go): every product in the factor loop, the
// corpus builder and the normal quantile is wrapped as float64(a*b), the
// only construct Go forbids from contracting into a fused multiply-add
// (see .claude/reference/synthetic-data.md, Float fusion); math.Sqrt is
// correctly rounded by IEEE-754; math.Pow(10, k) for an integer k is a
// chain of exact-power multiplies. The draw is pinned through the
// `normal` quantile (mean + float64(std*u)) only, deliberately: the
// other quantiles reach math.Erf / math.Exp / math.Log, which the
// standard library implements per architecture.
//
// # Regenerating
//
// Never, for a refactor — this pin exists to stay green across one. A
// DELIBERATE numerical change prints the new values on failure
// (`go test ./internal/synth/ -run TestCholeskyPin -v`); paste them in
// and say in the commit why the bits moved.

// cholPinCase is one matrix of the pinned corpus.
type cholPinCase struct {
	name string
	m    [][]float64
}

// cholPinCorpus is the fixed corpus: random SPD correlation matrices,
// EXACTLY rank-deficient unit-diagonal ones (the inputs the ridge loop
// exists for; their ok/not-ok decision is pure roundoff), near-singular
// ones, the documented jointly-inconsistent triple, and one no ridge can
// repair.
func cholPinCorpus() []cholPinCase {
	rng := mrand.New(mrand.NewPCG(20261005, 15))
	var out []cholPinCase
	for _, n := range []int{2, 3, 4, 5, 7, 10, 16} {
		for rep := 0; rep < 3; rep++ {
			out = append(out, cholPinCase{
				name: "spd/n=" + strconv.Itoa(n) + "/" + strconv.Itoa(rep),
				m:    cholPinCorrelation(rng, n, n+2),
			})
		}
	}
	for _, nk := range [][2]int{{3, 1}, {3, 2}, {4, 2}, {5, 3}, {6, 2}, {8, 5}, {10, 4}, {12, 11}} {
		for rep := 0; rep < 4; rep++ {
			out = append(out, cholPinCase{
				name: "rankdef/n=" + strconv.Itoa(nk[0]) + "/k=" + strconv.Itoa(nk[1]) + "/" + strconv.Itoa(rep),
				m:    cholPinCorrelation(rng, nk[0], nk[1]),
			})
		}
	}
	for _, exp := range []int{4, 8, 12, 15} {
		rho := 1 - math.Pow(10, float64(-exp))
		out = append(out, cholPinCase{
			name: "near-singular/1-1e-" + strconv.Itoa(exp),
			m: [][]float64{
				{1, rho, rho},
				{rho, 1, rho},
				{rho, rho, 1},
			},
		})
	}
	ones := make([][]float64, 4)
	for i := range ones {
		ones[i] = []float64{1, 1, 1, 1}
	}
	out = append(out,
		cholPinCase{name: "rank-one/all-ones", m: ones},
		cholPinCase{name: "inconsistent/0.9,0.9,-0.9", m: [][]float64{
			{1, 0.9, -0.9},
			{0.9, 1, 0.9},
			{-0.9, 0.9, 1},
		}},
		cholPinCase{name: "identity/1", m: [][]float64{{1}}},
		cholPinCase{name: "unrepairable/rho=5", m: [][]float64{
			{1, 5},
			{5, 1},
		}},
	)
	return out
}

// cholPinCorrelation returns the n×n correlation matrix of a random
// n×k loading matrix A (S = A·Aᵀ, scaled to a unit diagonal), so its
// rank is min(n, k). Every product feeding a sum is barriered — the
// corpus bytes have to be as portable as the code they pin.
func cholPinCorrelation(rng *mrand.Rand, n, k int) [][]float64 {
	a := make([][]float64, n)
	for i := range a {
		a[i] = make([]float64, k)
		for j := range a[i] {
			// 2x-1 in [-1, 1). Float64 is an inlined scale by 2^-53
			// that the compiler would otherwise contract into the
			// subtraction; every step is exact so fusion could not move
			// a bit, but the barrier keeps the fusion audit
			// (-d=fmahash) silent for this file.
			x := float64(rng.Float64())
			a[i][j] = float64(x*2) - 1
		}
	}
	s := make([][]float64, n)
	for i := range s {
		s[i] = make([]float64, n)
		for j := range s[i] {
			sum := 0.0
			for t := 0; t < k; t++ {
				sum += float64(a[i][t] * a[j][t])
			}
			s[i][j] = sum
		}
	}
	m := make([][]float64, n)
	for i := range m {
		m[i] = make([]float64, n)
		for j := range m[i] {
			if i == j {
				m[i][j] = 1
				continue
			}
			m[i][j] = s[i][j] / math.Sqrt(float64(s[i][i]*s[j][j]))
		}
	}
	return m
}

// cholPinWant is one case's pinned outcome: the accumulated ridge as raw
// bits, and either the SHA-256 of the factor's raw bits or the refusal.
type cholPinWant struct {
	ridge  uint64
	factor string // hex SHA-256 of every L[i][j] (row-major, full n×n); "" on refusal
	err    bool
}

var cholPinWants = map[string]cholPinWant{
	"spd/n=2/0":                 {ridge: 0x0, factor: "8b66ba51bc3584e021c19c2ad78be9a21c12d4484e4e055c77ac7dbf718f9c22", err: false},
	"spd/n=2/1":                 {ridge: 0x0, factor: "f59d94684327074b8e9e11bfc489c7211537b198fd77aa763c8e01edbba2f434", err: false},
	"spd/n=2/2":                 {ridge: 0x0, factor: "d626978bf25c642b9e003bed462833674699a98a2703765c011e26e64af35394", err: false},
	"spd/n=3/0":                 {ridge: 0x0, factor: "6d9f873ce09ff691f8d2449e1b044cc7dc10736fd994130a5db963a54055e64a", err: false},
	"spd/n=3/1":                 {ridge: 0x0, factor: "e00198903c2bc9e25ba389bab52b5a8ce1facdc5dea8c278f939ed7c82351fa4", err: false},
	"spd/n=3/2":                 {ridge: 0x0, factor: "171e070a9c0160c74af6dbedc22520826bd864a4607773bdd1c0a698a2f18925", err: false},
	"spd/n=4/0":                 {ridge: 0x0, factor: "e5d83cbad286c80b2ffc19770608a867e8deea1bc80a375e00b73fad86b63c87", err: false},
	"spd/n=4/1":                 {ridge: 0x0, factor: "67856ac72869a0bbefd6810f09c8df059dc5fe500a7b20d1744723d1f207471d", err: false},
	"spd/n=4/2":                 {ridge: 0x0, factor: "77ecdc5f3b03b1db39c53ff981c8ba6e2e8ae1cb91b294594b9096aa5bbe7845", err: false},
	"spd/n=5/0":                 {ridge: 0x0, factor: "db8994aab3d684de19ef22683e1d6783c4ce2b2f042b1ca6a8895b72fd6722ad", err: false},
	"spd/n=5/1":                 {ridge: 0x0, factor: "b5be8fdce11faa1288a7086dfbbe0819a68deee5cb7ce253ca74bdccdb13b8d3", err: false},
	"spd/n=5/2":                 {ridge: 0x0, factor: "1402f12d6ca85adfbd9cc5f86f30553cb9c5c6ba0dab608161cab6d259911327", err: false},
	"spd/n=7/0":                 {ridge: 0x0, factor: "a5c0f081eec7d2005ae4ddfe8cda46e2228bf2dfa039628d7d86da6cef02ed20", err: false},
	"spd/n=7/1":                 {ridge: 0x0, factor: "92b10eb35c5856e9ad73f58c39b7fdea5e0241025899c34bef41aa1821c2c8de", err: false},
	"spd/n=7/2":                 {ridge: 0x0, factor: "458e233b83a8df5271c6c9275db0a2e7dae0ba0c20fc3141a820bd626c4364d4", err: false},
	"spd/n=10/0":                {ridge: 0x0, factor: "67214600ccb2f77c474595eaf0309be0d21012f2ead80bce11c3e40611bf8d41", err: false},
	"spd/n=10/1":                {ridge: 0x0, factor: "45bd2cde3deef882be22cb5c73e2743b3746b0afce5513dcdf0f7ad9265d42a9", err: false},
	"spd/n=10/2":                {ridge: 0x0, factor: "bc2271a68e2805cb782aef4ac22e761ae1ea0cafcedfcb960a060fad2f8af003", err: false},
	"spd/n=16/0":                {ridge: 0x0, factor: "6241135cf1f7419459a5d36fde8fa855eae507de6a06be78ef91d88fe876b27d", err: false},
	"spd/n=16/1":                {ridge: 0x0, factor: "142a93c8a487c2c16843fd50c93c52b480442ee00bf4b76333e31f9b624b751e", err: false},
	"spd/n=16/2":                {ridge: 0x0, factor: "48384ea8a04d7780249c6fa55fed4507d6459554fca7ac0dc7a6e22d6dc140f3", err: false},
	"rankdef/n=3/k=1/0":         {ridge: 0x3eb0c6f7a0b5ed8d, factor: "4fdbf00bc7279c00dea361bae9869999d41df801ffc4da41766e975133aeff01", err: false},
	"rankdef/n=3/k=1/1":         {ridge: 0x3eb0c6f7a0b5ed8d, factor: "009b2b3d04f4768e6312d95f79795116312139c84d30633f9eb670900b7589b5", err: false},
	"rankdef/n=3/k=1/2":         {ridge: 0x3eb0c6f7a0b5ed8d, factor: "8c1d090959eb72d493efd168521a3c84f5bd805a9896689d467752d270fda3b8", err: false},
	"rankdef/n=3/k=1/3":         {ridge: 0x3eb0c6f7a0b5ed8d, factor: "c385da9c0676bbe4b8d3f09e0fa964217ceffb4e2d1605a954d1b8c622a3d709", err: false},
	"rankdef/n=3/k=2/0":         {ridge: 0x3eb0c6f7a0b5ed8d, factor: "25ee4aeb95c627ecddbdce297eb10d8a2d96a1967aca9018890117797fe52a3b", err: false},
	"rankdef/n=3/k=2/1":         {ridge: 0x3eb0c6f7a0b5ed8d, factor: "8a2533d7a0bd117c0f8d635017b1e67194e7146003073130d4474283769d0f31", err: false},
	"rankdef/n=3/k=2/2":         {ridge: 0x0, factor: "f52336dd9e3e87b3ad56d188ed0cda036510dd8fae071547d4e861342f08b029", err: false},
	"rankdef/n=3/k=2/3":         {ridge: 0x0, factor: "f23aa092a1c075735f3900d8db36de64f334bbef1dd89435f5ccb0417a912227", err: false},
	"rankdef/n=4/k=2/0":         {ridge: 0x3eb0c6f7a0b5ed8d, factor: "eaba1b18a83479a018afc830149a05398f3f372610191de56ecc8c9e19a05c25", err: false},
	"rankdef/n=4/k=2/1":         {ridge: 0x3eb0c6f7a0b5ed8d, factor: "5986839ab7b0176622d8e789cfb75b9f84d1eced141507c70b16a459a9054afa", err: false},
	"rankdef/n=4/k=2/2":         {ridge: 0x3eb0c6f7a0b5ed8d, factor: "2dd94221223f0e4ba82caa4bb244a9bffefc6ffd6be4e94f800a2924e571aec4", err: false},
	"rankdef/n=4/k=2/3":         {ridge: 0x3eb0c6f7a0b5ed8d, factor: "5f53a2e17d4fbc695f0a2bf7049785ebb7dc9b235f1a1cf09a12e327091bddfe", err: false},
	"rankdef/n=5/k=3/0":         {ridge: 0x0, factor: "42f435afd50c9461a3a46eb8b3e49c2266062239d65aeb74c521fdd86fcce59c", err: false},
	"rankdef/n=5/k=3/1":         {ridge: 0x3eb0c6f7a0b5ed8d, factor: "4d3088af8166d82f3763b5fabd997a22ad224b60f69033362cdc4d9b5edc62c4", err: false},
	"rankdef/n=5/k=3/2":         {ridge: 0x3eb0c6f7a0b5ed8d, factor: "4bd450cb746c7ac42a56dd2faa57703a0be1cabe518e2469743771d3460c613f", err: false},
	"rankdef/n=5/k=3/3":         {ridge: 0x3eb0c6f7a0b5ed8d, factor: "8ed81b0c0bd510bf873e662b6bf8ff311d3ba42c5033ab3ed59a714122208c0d", err: false},
	"rankdef/n=6/k=2/0":         {ridge: 0x3eb0c6f7a0b5ed8d, factor: "8f92730c945ff82d5adfa29d81be1de2e1dea06c98f81b348492b386d7219052", err: false},
	"rankdef/n=6/k=2/1":         {ridge: 0x3eb0c6f7a0b5ed8d, factor: "2ae927201d2095640498b978417426135745468374c79bcf6d4cea38b882b864", err: false},
	"rankdef/n=6/k=2/2":         {ridge: 0x3eb0c6f7a0b5ed8d, factor: "fe6a2d53d85329822971799c8fcc5167b677ee239d2e99fc41b4296e5fd9143d", err: false},
	"rankdef/n=6/k=2/3":         {ridge: 0x3eb0c6f7a0b5ed8d, factor: "9262a713cb65b6a3cf0578b5072192377aee5bd19b12f553f8ffa4d64677bf65", err: false},
	"rankdef/n=8/k=5/0":         {ridge: 0x3eb0c6f7a0b5ed8d, factor: "9ee859c72835ccb3f8921d3dc500e46bf5d36f969e3e28cc15e1da0eb15c8663", err: false},
	"rankdef/n=8/k=5/1":         {ridge: 0x3eb0c6f7a0b5ed8d, factor: "2e84e8683505ee9bb027451f6704160564885e6447a7aacb3812f84a10aff9ba", err: false},
	"rankdef/n=8/k=5/2":         {ridge: 0x3eb0c6f7a0b5ed8d, factor: "cfae3afc1601e2098b893c102a273688d0ec0867d025d9400737de754cb0d31b", err: false},
	"rankdef/n=8/k=5/3":         {ridge: 0x3eb0c6f7a0b5ed8d, factor: "13c30dbd27572dbd959e29a292d2426029aee831a7db62301ab4fbdeb2322427", err: false},
	"rankdef/n=10/k=4/0":        {ridge: 0x3eb0c6f7a0b5ed8d, factor: "0651903cb28de414f0a025e292674cbcb8620bf487036cc2ad08be73126a8448", err: false},
	"rankdef/n=10/k=4/1":        {ridge: 0x3eb0c6f7a0b5ed8d, factor: "80ba4897cf0137b3a3814fcb52ab11d2e2b368eb87f3e4fd1b3c5dc7bc4e8fea", err: false},
	"rankdef/n=10/k=4/2":        {ridge: 0x3eb0c6f7a0b5ed8d, factor: "5ca8897041198bc18efd77c9705010afcee14de8d55770eefa49e449c064e07c", err: false},
	"rankdef/n=10/k=4/3":        {ridge: 0x3eb0c6f7a0b5ed8d, factor: "8d5cf1ef06e1ea9085feabec15c4b90a886f07d248598dca7ce9d3d2070ec71f", err: false},
	"rankdef/n=12/k=11/0":       {ridge: 0x0, factor: "36ad353a98be805e7ced34c73f5b6c2da426c2d398ccef81fd17eaa4b0b63027", err: false},
	"rankdef/n=12/k=11/1":       {ridge: 0x3eb0c6f7a0b5ed8d, factor: "8e4e238f12393a991f77b3a454a240a86f635874e50093a806db01b530773c95", err: false},
	"rankdef/n=12/k=11/2":       {ridge: 0x3eb0c6f7a0b5ed8d, factor: "5ae68b92fd79151b6909c28c2c083bfbd7e251721c4e824914075c8c49406d35", err: false},
	"rankdef/n=12/k=11/3":       {ridge: 0x3eb0c6f7a0b5ed8d, factor: "6b4e08eeba2e9accaefe581ac0fcffe0aa0c2e370de6f480a31dc23567462798", err: false},
	"near-singular/1-1e-4":      {ridge: 0x0, factor: "839f6cae1c2a50a47592ac4ed8615d2d26af3085d2225646917c30595b48a56a", err: false},
	"near-singular/1-1e-8":      {ridge: 0x0, factor: "672bd65c7d60693738c0ab5d7d335641e00915c06295e0e4eff35fc60a1970cf", err: false},
	"near-singular/1-1e-12":     {ridge: 0x0, factor: "460c9f87285ea9a6bff2388113df196efd3b9bf6f4227d236ad665706f7bc37c", err: false},
	"near-singular/1-1e-15":     {ridge: 0x0, factor: "b127dfb11eacea31538664f0bd0551e21cb653df5cf3924a49d35b7eb886934f", err: false},
	"rank-one/all-ones":         {ridge: 0x3eb0c6f7a0b5ed8d, factor: "1b9601e94b5eb06fa2f21125b00f44a208264799a5c8513b094ff06f89c7b2ca", err: false},
	"inconsistent/0.9,0.9,-0.9": {ridge: 0x3ff1c71c53f39d1b, factor: "1a27559c6e2c36d2af704d497d2e4c2c624ec0b828ac48f6ef630d65d2d1bf74", err: false},
	"identity/1":                {ridge: 0x0, factor: "7b7c534f75278483fc012dbb043b0f033e3e2a04c9e970020d17336ea0f2ef4d", err: false},
	"unrepairable/rho=5":        {ridge: 0x402638e38a7e73a3, factor: "", err: true},
}

// cholPinDigest hashes a factor's dimensions and raw float bits.
func cholPinDigest(L [][]float64) string {
	h := sha256.New()
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(len(L)))
	h.Write(buf[:])
	for _, row := range L {
		binary.LittleEndian.PutUint64(buf[:], uint64(len(row)))
		h.Write(buf[:])
		for _, v := range row {
			binary.LittleEndian.PutUint64(buf[:], math.Float64bits(v))
			h.Write(buf[:])
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestCholeskyPin_FactorAndRidgeBits pins, per corpus case, the factor
// bits and the accumulated ridge bits cholesky returns, and that a
// matrix no ridge repairs is refused as SERVICE_VALIDATION with the
// established message and no details.
func TestCholeskyPin_FactorAndRidgeBits(t *testing.T) {
	corpus := cholPinCorpus()
	if len(corpus) != len(cholPinWants) {
		t.Errorf("corpus has %d cases, pin table has %d", len(corpus), len(cholPinWants))
	}
	var ridged, refused int
	for _, c := range corpus {
		in := make([][]float64, len(c.m))
		for i := range c.m {
			in[i] = append([]float64(nil), c.m[i]...)
		}
		L, ridge, err := cholesky(in)

		for i := range c.m {
			for j := range c.m[i] {
				if math.Float64bits(in[i][j]) != math.Float64bits(c.m[i][j]) {
					t.Fatalf("%s: cholesky mutated its input at (%d, %d)", c.name, i, j)
				}
			}
		}

		got := cholPinWant{ridge: math.Float64bits(ridge)}
		if err != nil {
			got.err = true
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) {
				t.Fatalf("%s: error is not a *CodedError: %v", c.name, err)
			}
			if ce.Code != errors.SERVICE_VALIDATION {
				t.Errorf("%s: code = %s, want SERVICE_VALIDATION", c.name, ce.Code)
			}
			const msg = "correlation matrix is not positive semi-definite even after ridge regularization"
			if ce.Message != msg {
				t.Errorf("%s: message = %q, want %q", c.name, ce.Message, msg)
			}
			if len(ce.Details) != 0 {
				t.Errorf("%s: details = %v, want none", c.name, ce.Details)
			}
			if L != nil {
				t.Errorf("%s: refusal returned a factor", c.name)
			}
			refused++
		} else {
			got.factor = cholPinDigest(L)
			for i := range L {
				for j := i + 1; j < len(L[i]); j++ {
					if L[i][j] != 0 {
						t.Errorf("%s: L[%d][%d] = %v above the diagonal", c.name, i, j, L[i][j])
					}
				}
			}
		}
		if ridge > 0 {
			ridged++
		}

		want, ok := cholPinWants[c.name]
		if !ok || got != want {
			t.Errorf("%s: got {ridge: %s (%#x), factor: %q, err: %v}, want %+v\n\tpin line: %q: {ridge: %#x, factor: %q, err: %v},",
				c.name, strconv.FormatFloat(ridge, 'x', -1, 64), got.ridge, got.factor, got.err, want,
				c.name, got.ridge, got.factor, got.err)
		}
	}
	// The corpus must actually exercise the ridge loop and the refusal,
	// or the pin says nothing about the schedule.
	if ridged < 5 || refused < 1 {
		t.Errorf("corpus exercises the ridge on %d case(s) and refuses %d; want >= 5 and >= 1", ridged, refused)
	}
}

// cholPinDrawWant pins one end-to-end correlated draw: the SHA-256 of
// every drawn value's raw bits in row then participant order, and the
// warnings buildCorrelator raised.
type cholPinDrawWant struct {
	values   string
	warnings []string
}

var cholPinDrawWants = map[string]cholPinDrawWant{
	"consistent": {
		values: "c5a001db054e4675467ca89d2faa91c0df959c33b916010132e20cbc132c288b",
		warnings: []string{
			"correlation matrix completed by assumption: 3 of 6 pair(s) among 4 correlated field(s) were never supplied and are drawn as independent (rho = 0); an absent pair is unknown, not known to be uncorrelated",
		},
	},
	"inconsistent-ridged": {
		values: "0c3d6409e74ac93e4539dacc41cd8141d638c0c0f22efb2b095132ed5f31cb86",
		warnings: []string{
			"correlation matrix completed by assumption: 2 of 6 pair(s) among 4 correlated field(s) were never supplied and are drawn as independent (rho = 0); an absent pair is unknown, not known to be uncorrelated",
			"correlation matrix is not positive definite as requested and was ridge-regularized (diagonal jitter 1.111111) to factorize: the requested pairwise correlations are not jointly consistent, so realized correlations will be pulled toward zero",
		},
	},
}

// TestCholeskyPin_CorrelatedDrawBits pins the end-to-end value-scale
// correlated draw — buildCorrelator's factor, correlatedNormals and the
// copula transform — as raw float bits for a fixed seed, on a consistent
// matrix and on a jointly-inconsistent one that needs the ridge (whose
// warning, carrying the ridge total, is pinned verbatim). The residual
// scale shares factorCorrelations and correlatedNormals, so the factor
// and the L·z step it rides are the ones pinned here.
func TestCholeskyPin_CorrelatedDrawBits(t *testing.T) {
	normal := func(name string, mean, std float64, extra map[string]any) FieldSpec {
		p := map[string]any{"mean": mean, "std": std}
		for k, v := range extra {
			p[k] = v
		}
		return FieldSpec{Name: name, Type: "f64", Distribution: DistNormal, Params: p}
	}
	fields := []FieldSpec{
		normal("a", 10, 2, nil),
		normal("b", -3, 0.5, nil),
		normal("c", 100, 15, map[string]any{"min": 80.0, "max": 125.0}),
		normal("d", 0, 1, nil),
	}
	cases := []struct {
		name  string
		corrs []CorrelationSpec
	}{
		{"consistent", []CorrelationSpec{
			{A: "a", B: "b", Correlation: 0.6},
			{A: "b", B: "c", Correlation: -0.3},
			{A: "a", B: "d", Correlation: 0.25},
		}},
		{"inconsistent-ridged", []CorrelationSpec{
			{A: "a", B: "b", Correlation: 0.9},
			{A: "b", B: "c", Correlation: 0.9},
			{A: "a", B: "c", Correlation: -0.9},
			{A: "c", B: "d", Correlation: 0.5},
		}},
	}
	if len(cases) != len(cholPinDrawWants) {
		t.Errorf("%d draw cases, pin table has %d", len(cases), len(cholPinDrawWants))
	}
	for _, c := range cases {
		spec := &Spec{RowCount: 1, Fields: fields, Correlations: c.corrs}
		_, wfs, err := buildSchema(spec)
		if err != nil {
			t.Fatalf("%s: buildSchema: %v", c.name, err)
		}
		corr, warnings, err := buildCorrelator(spec.Correlations, wfs)
		if err != nil {
			t.Fatalf("%s: buildCorrelator: %v", c.name, err)
		}
		rng := mrand.New(mrand.NewPCG(7, 11))
		h := sha256.New()
		var buf [8]byte
		for r := 0; r < 500; r++ {
			row := map[string]any{}
			corr.transform(rng, row)
			for _, name := range corr.fieldNames {
				binary.LittleEndian.PutUint64(buf[:], math.Float64bits(row[name].(float64)))
				h.Write(buf[:])
			}
		}
		got := cholPinDrawWant{values: hex.EncodeToString(h.Sum(nil)), warnings: warnings}
		want, ok := cholPinDrawWants[c.name]
		if !ok || got.values != want.values || !slices.Equal(got.warnings, want.warnings) {
			t.Errorf("%s: got values %q warnings %q, want %+v", c.name, got.values, got.warnings, want)
		}
	}
}
