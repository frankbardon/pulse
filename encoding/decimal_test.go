package encoding

import (
	"math/big"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

func TestParseDecimal128_Success(t *testing.T) {
	cases := []struct {
		in        string
		wantStr   string
		wantScale uint8
	}{
		{"0", "0", 0},
		{"123", "123", 0},
		{"-123", "-123", 0},
		{"+0.001", "0.001", 3},
		{"123.45", "123.45", 2},
		{"-123.45", "-123.45", 2},
		{"0.0", "0.0", 1},
		{"00012.3400", "12.3400", 4},
	}
	for _, tc := range cases {
		d, sc, err := ParseDecimal128(tc.in)
		if err != nil {
			t.Errorf("ParseDecimal128(%q) error: %v", tc.in, err)
			continue
		}
		if sc != tc.wantScale {
			t.Errorf("ParseDecimal128(%q) scale = %d, want %d", tc.in, sc, tc.wantScale)
		}
		got := d.String(sc)
		if got != tc.wantStr {
			t.Errorf("ParseDecimal128(%q).String() = %q, want %q", tc.in, got, tc.wantStr)
		}
	}
}

func TestParseDecimal128_Reject(t *testing.T) {
	bad := []string{
		"",
		"   ",
		"$1,234.56",
		"1 234.56",
		"1.5e3",
		"1.5E3",
		"abc",
		"1.2.3",
		"--1",
		"  1.5",
		"1.5  ",
		".",
		"+",
		"-",
	}
	for _, in := range bad {
		_, _, err := ParseDecimal128(in)
		if err == nil {
			t.Errorf("ParseDecimal128(%q) accepted, expected error", in)
		}
	}
}

func TestDecimal128_Add_Overflow(t *testing.T) {
	maxStr := strings.Repeat("9", 38)
	a, _, err := ParseDecimal128(maxStr)
	if err != nil {
		t.Fatal(err)
	}
	b := NewDecimal128FromInt(1)
	_, err = a.Add(b)
	if err == nil {
		t.Fatal("expected overflow")
	}
	if ce, ok := err.(*errors.CodedError); !ok || ce.Code != errors.PULSE_DECIMAL_OVERFLOW {
		t.Fatalf("expected PULSE_DECIMAL_OVERFLOW, got %v", err)
	}
}

func TestDecimal128_BankerRound(t *testing.T) {
	// 1.5 round to scale 0 => 2 (even)
	d15, _, _ := ParseDecimal128("1.5")
	r, err := d15.Rescale(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.String(0) != "2" {
		t.Errorf("1.5 rescaled to 0 = %s, want 2", r.String(0))
	}
	// 2.5 round to scale 0 => 2 (even)
	d25, _, _ := ParseDecimal128("2.5")
	r, err = d25.Rescale(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.String(0) != "2" {
		t.Errorf("2.5 rescaled to 0 = %s, want 2", r.String(0))
	}
	// 0.5 round to scale 0 => 0 (even)
	d05, _, _ := ParseDecimal128("0.5")
	r, err = d05.Rescale(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.String(0) != "0" {
		t.Errorf("0.5 rescaled to 0 = %s, want 0", r.String(0))
	}
	// -1.5 => -2 (even)
	dn15, _, _ := ParseDecimal128("-1.5")
	r, err = dn15.Rescale(1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.String(0) != "-2" {
		t.Errorf("-1.5 rescaled to 0 = %s, want -2", r.String(0))
	}
	// 1.6 => 2 (round up)
	d16, _, _ := ParseDecimal128("1.6")
	r, _ = d16.Rescale(1, 0)
	if r.String(0) != "2" {
		t.Errorf("1.6 rescaled to 0 = %s, want 2", r.String(0))
	}
	// 1.4 => 1 (round down)
	d14, _, _ := ParseDecimal128("1.4")
	r, _ = d14.Rescale(1, 0)
	if r.String(0) != "1" {
		t.Errorf("1.4 rescaled to 0 = %s, want 1", r.String(0))
	}
}

func TestPromoteAdd(t *testing.T) {
	p, s := PromoteAdd(10, 4, 10, 4)
	if p != 11 || s != 4 {
		t.Errorf("PromoteAdd(10,4,10,4) = (%d,%d), want (11,4)", p, s)
	}
	p, s = PromoteAdd(38, 0, 38, 0)
	if p != 38 || s != 0 {
		t.Errorf("PromoteAdd(38,0,38,0) clamps to (%d,%d), want (38,0)", p, s)
	}
}

func TestPromoteMul(t *testing.T) {
	p, s := PromoteMul(10, 4, 10, 4)
	if p != 20 || s != 8 {
		t.Errorf("PromoteMul(10,4,10,4) = (%d,%d), want (20,8)", p, s)
	}
}

func TestPromoteDiv(t *testing.T) {
	p, s := PromoteDiv(10, 4, 10, 4)
	if s < 4 {
		t.Errorf("PromoteDiv must not drop below MIN_SCALE; got s=%d", s)
	}
	if p == 0 {
		t.Errorf("PromoteDiv produced zero precision")
	}
}

func TestDecimal128_DivByZero(t *testing.T) {
	a, _, _ := ParseDecimal128("100")
	b := ZeroDecimal128()
	_, err := a.Div(b, 0, 0, 4)
	if err == nil {
		t.Fatal("expected divide-by-zero error")
	}
	if ce, ok := err.(*errors.CodedError); !ok || ce.Code != errors.PULSE_DECIMAL_DIVIDE_BY_ZERO {
		t.Fatalf("expected PULSE_DECIMAL_DIVIDE_BY_ZERO, got %v", err)
	}
}

func TestDecimal128_RoundTripEncoding(t *testing.T) {
	cases := []string{
		"0",
		"1",
		"-1",
		"123456789012345678901234567",
		"-987654321098765432109876543",
		"99999999999999999999999999999999999999",  // 38 nines
		"-99999999999999999999999999999999999999", // -38 nines
	}
	for _, in := range cases {
		var m big.Int
		m.SetString(in, 10)
		d, err := NewDecimal128FromBigInt(&m)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		buf := EncodeDecimal128(d)
		d2, isNull := DecodeDecimal128(buf)
		if isNull {
			t.Fatalf("%s decoded as null", in)
		}
		if d2.String(0) != d.String(0) {
			t.Errorf("round trip %s: got %s", in, d2.String(0))
		}
	}
}

func TestDecimal128_NullSentinel(t *testing.T) {
	null := NullDecimalSentinel()
	_, isNull := DecodeDecimal128(null)
	if !isNull {
		t.Fatal("expected null sentinel to decode as null")
	}
}

func TestDecimal128_AggregateAgainstReference(t *testing.T) {
	// Sum of 1..1000 in decimal128(20, 6) => 500500.000000.
	acc := ZeroDecimal128()
	for i := 1; i <= 1000; i++ {
		v := NewDecimal128FromBigInt
		_ = v
		var m big.Int
		m.SetInt64(int64(i) * 1_000_000) // mantissa at scale 6
		d, _ := NewDecimal128FromBigInt(&m)
		var err error
		acc, err = acc.Add(d)
		if err != nil {
			t.Fatal(err)
		}
	}
	want := "500500.000000"
	got := acc.String(6)
	if got != want {
		t.Errorf("sum = %s, want %s", got, want)
	}
}
