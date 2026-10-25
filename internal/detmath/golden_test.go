package detmath

import (
	"math"
	"testing"
)

// goldenCase is one frozen detmath result. y is used by the two-argument
// kernels only. want is the exact result bit pattern; isNaN replaces it
// for NaN results, whose sign and payload bits Go does not pin (a runtime
// 0/0 is 0x7ff8… on arm64 and 0xfff8… on x86 SSE), so only NaN-ness is
// asserted.
type goldenCase struct {
	fn    string
	x, y  float64
	want  uint64
	isNaN bool
}

var (
	inf  = math.Inf(1)
	ninf = math.Inf(-1)
	nan  = math.NaN()
	nz   = math.Copysign(0, -1)
)

// goldenCases are the frozen inputs and results of
// TestDetmath_GoldenBits. Every input is an exact float literal (or an
// exact special value), so the table is architecture-independent. The
// results were produced by this package — never copied from math.*, whose
// bits are the architecture-dependent thing detmath replaces — on
// darwin/arm64 after `-d=fmahash=vy` reported no contraction, and checked
// identical on linux/amd64 (docker); CI re-runs it on linux/amd64.
//
// The rows cover ordinary values, ±0, ±Inf, NaN, subnormal inputs and
// results, overflow / underflow edges, Payne-Hanek trig range reduction
// (|x| ≥ 2^29), the inputs where the stdlib itself disagrees across
// architectures, and the amd64 assembly defects detmath does not share
// (math.Log of a subnormal, math.Exp overflowing early near 709.4–709.78).
// The Expm1 / Erf / Erfinv rows include the sweep inputs where arm64's
// contracted stdlib (Expm1(0.1466…), Erf(0.2291…), Erfinv(0.0340…)) and
// amd64's FMA-path Exp inside math.Erf (Erf(1.5911…)) leave the
// unbarriered result.
var goldenCases = []goldenCase{
	// Exp
	{fn: "Exp", x: 0, want: 0x3ff0000000000000},                  // 1
	{fn: "Exp", x: nz, want: 0x3ff0000000000000},                 // 1
	{fn: "Exp", x: 1, want: 0x4005bf0a8b145769},                  // 2.718281828459045
	{fn: "Exp", x: -1, want: 0x3fd78b56362cef38},                 // 0.36787944117144233
	{fn: "Exp", x: 0.5, want: 0x3ffa61298e1e069c},                // 1.6487212707001282
	{fn: "Exp", x: 1e-9, want: 0x3ff000000044b830},               // 1.000000001
	{fn: "Exp", x: 0x1p-30, want: 0x3ff0000000400000},            // 1.0000000009313226
	{fn: "Exp", x: 10, want: 0x40d5829dcf950560},                 // 22026.465794806718
	{fn: "Exp", x: -10, want: 0x3f07cd79b5647c9a},                // 4.539992976248485e-05
	{fn: "Exp", x: 100.5, want: 0x48ffcc37a76f9e76},              // 4.4319559098458955e+43
	{fn: "Exp", x: -4.447676987598294, want: 0x3f87f92c362c3112}, // 0.011705727961747434
	{fn: "Exp", x: 217.29735907128833, want: 0x538687de1a618c82}, // 2.3498707575302078e+94
	{fn: "Exp", x: 709.43, want: 0x7fe67d2a40c95256},             // 1.2633808701860081e+308
	{fn: "Exp", x: 709.5, want: 0x7fe81e9b4b52d0c9},              // 1.3549863193146328e+308
	{fn: "Exp", x: 709.78, want: 0x7fefe9ce5c4c52b4},             // 1.7928227943945155e+308
	{fn: "Exp", x: 709.8, want: 0x7ff0000000000000},              // +Inf
	{fn: "Exp", x: -708.5, want: 0x000e6cf6d08897ac},             // 2.006132305331306e-308
	{fn: "Exp", x: -740, want: 0x0000000000000055},               // 4.2e-322
	{fn: "Exp", x: -745.1, want: 0x0000000000000001},             // 5e-324
	{fn: "Exp", x: -746, want: 0x0000000000000000},               // 0
	{fn: "Exp", x: inf, want: 0x7ff0000000000000},                // +Inf
	{fn: "Exp", x: ninf, want: 0x0000000000000000},               // 0
	{fn: "Exp", x: nan, isNaN: true},
	{fn: "Exp", x: 0.278, want: 0x3ff520b6227d51cd}, // 1.3204861972090953
	{fn: "Exp", x: 4.502, want: 0x40568ca14feea20e}, // 90.19734571746832
	// Log
	{fn: "Log", x: 1, want: 0x0000000000000000},                      // 0
	{fn: "Log", x: 2, want: 0x3fe62e42fefa39ef},                      // 0.6931471805599453
	{fn: "Log", x: 0.5, want: 0xbfe62e42fefa39ef},                    // -0.6931471805599453
	{fn: "Log", x: 10, want: 0x40026bb1bbb55516},                     // 2.302585092994046
	{fn: "Log", x: 0.040071135050271586, want: 0xc009bc9e6a4bc062},   // -3.2170990280501863
	{fn: "Log", x: 1e-300, want: 0xc085963447f87fb5},                 // -690.7755278982137
	{fn: "Log", x: 0x1p-1022, want: 0xc086232bdd7abcd2},              // -708.3964185322641
	{fn: "Log", x: 1e-310, want: 0xc0864e69394d9508},                 // -713.8013788281542
	{fn: "Log", x: 5e-324, want: 0xc0874385446d71c3},                 // -744.4400719213812
	{fn: "Log", x: 1.7976931348623157e308, want: 0x40862e42fefa39ef}, // 709.782712893384
	{fn: "Log", x: 0, want: 0xfff0000000000000},                      // -Inf
	{fn: "Log", x: nz, want: 0xfff0000000000000},                     // -Inf
	{fn: "Log", x: -1, isNaN: true},
	{fn: "Log", x: inf, want: 0x7ff0000000000000}, // +Inf
	{fn: "Log", x: ninf, isNaN: true},
	{fn: "Log", x: nan, isNaN: true},
	{fn: "Log", x: 0.157, want: 0xbffd9fc865d7dd0f}, // -1.851509473633829
	{fn: "Log", x: 0.64, want: 0xbfdc8ff7c79a9a21},  // -0.4462871026284195
	// Log1p
	{fn: "Log1p", x: 0, want: 0x0000000000000000},                // 0
	{fn: "Log1p", x: nz, want: 0x8000000000000000},               // -0
	{fn: "Log1p", x: 1e-20, want: 0x3bc79ca10c924223},            // 1e-20
	{fn: "Log1p", x: 1e-10, want: 0x3ddb7cdfd9d1d693},            // 9.999999999500001e-11
	{fn: "Log1p", x: 0.3, want: 0x3fd0ca937be1b9dc},              // 0.26236426446749106
	{fn: "Log1p", x: -0.25, want: 0xbfd269621134db92},            // -0.2876820724517809
	{fn: "Log1p", x: -0.5, want: 0xbfe62e42fefa39ef},             // -0.6931471805599453
	{fn: "Log1p", x: -0.9999, want: 0xc0226bb1bbb55554},          // -9.210340371976294
	{fn: "Log1p", x: 1, want: 0x3fe62e42fefa39ef},                // 0.6931471805599453
	{fn: "Log1p", x: 1e10, want: 0x4037069e2aa3184e},             // 23.025850930040455
	{fn: "Log1p", x: 1e300, want: 0x4085963447f87fb5},            // 690.7755278982137
	{fn: "Log1p", x: 9007199254740994, want: 0x40425e4f7b2737fa}, // 36.7368005696771
	{fn: "Log1p", x: -1, want: 0xfff0000000000000},               // -Inf
	{fn: "Log1p", x: -2, isNaN: true},
	{fn: "Log1p", x: inf, want: 0x7ff0000000000000}, // +Inf
	{fn: "Log1p", x: ninf, isNaN: true},
	{fn: "Log1p", x: nan, isNaN: true},
	{fn: "Log1p", x: 0.211, want: 0x3fc8815158222f86}, // 0.1914464645709552
	// Sin
	{fn: "Sin", x: 0, want: 0x0000000000000000},                      // 0
	{fn: "Sin", x: nz, want: 0x8000000000000000},                     // -0
	{fn: "Sin", x: 1, want: 0x3feaed548f090cee},                      // 0.8414709848078965
	{fn: "Sin", x: -1, want: 0xbfeaed548f090cee},                     // -0.8414709848078965
	{fn: "Sin", x: 0.5, want: 0x3fdeaee8744b05f0},                    // 0.479425538604203
	{fn: "Sin", x: 3.141592653589793, want: 0x3ca1a62633145c00},      // 1.2246467991473515e-16
	{fn: "Sin", x: 1.5707963267948966, want: 0x3ff0000000000000},     // 1
	{fn: "Sin", x: 1e5, want: 0x3fa24daa9c527e96},                    // 0.03574879797201651
	{fn: "Sin", x: 1e9, want: 0x3fe1778cae83c69a},                    // 0.5458434494486994
	{fn: "Sin", x: 5.497633775832186e10, want: 0x3fec5ef94070a17e},   // 0.8865934618561793
	{fn: "Sin", x: 1e22, want: 0xbfeb453ab76bf398},                   // -0.8522008497671889
	{fn: "Sin", x: 1.7976931348623157e308, want: 0x3f7452fc98b34eb0}, // 0.004961954789184084
	{fn: "Sin", x: 5e-324, want: 0x0000000000000001},                 // 5e-324
	{fn: "Sin", x: inf, isNaN: true},
	{fn: "Sin", x: ninf, isNaN: true},
	{fn: "Sin", x: nan, isNaN: true},
	{fn: "Sin", x: 0.259, want: 0x3fd0642c1d85096e},             // 0.2561140335348203
	{fn: "Sin", x: 2.100000000005e11, want: 0x3fe6414b765da99a}, // 0.6954705535729773
	// Cos
	{fn: "Cos", x: 0, want: 0x3ff0000000000000},                      // 1
	{fn: "Cos", x: nz, want: 0x3ff0000000000000},                     // 1
	{fn: "Cos", x: 1, want: 0x3fe14a280fb5068c},                      // 0.5403023058681398
	{fn: "Cos", x: -1, want: 0x3fe14a280fb5068c},                     // 0.5403023058681398
	{fn: "Cos", x: 0.5, want: 0x3fec1528065b7d50},                    // 0.8775825618903728
	{fn: "Cos", x: 3.141592653589793, want: 0xbff0000000000000},      // -1
	{fn: "Cos", x: 1.5707963267948966, want: 0x3c91a62633145c00},     // 6.123233995736757e-17
	{fn: "Cos", x: 1e5, want: 0xbfeffac3841b3da8},                    // -0.9993608074382125
	{fn: "Cos", x: 1e9, want: 0x3feacff8c7364234},                    // 0.8378871813639024
	{fn: "Cos", x: 5.497633775832186e10, want: 0xbfdd9a693035e00d},   // -0.4625494929127861
	{fn: "Cos", x: 1e22, want: 0x3fe0be2cef01c8f3},                   // 0.5232147853951389
	{fn: "Cos", x: 1.7976931348623157e308, want: 0xbfefffe62ecfab75}, // -0.9999876894265599
	{fn: "Cos", x: 5e-324, want: 0x3ff0000000000000},                 // 1
	{fn: "Cos", x: inf, isNaN: true},
	{fn: "Cos", x: ninf, isNaN: true},
	{fn: "Cos", x: nan, isNaN: true},
	{fn: "Cos", x: 0.46, want: 0x3fecac76498be14c},      // 0.8960524975255253
	{fn: "Cos", x: 246000.25, want: 0x3fe1daed5acd5618}, // 0.5579745076635758
	// Tan
	{fn: "Tan", x: 0, want: 0x0000000000000000},                      // 0
	{fn: "Tan", x: nz, want: 0x8000000000000000},                     // -0
	{fn: "Tan", x: 1, want: 0x3ff8eb245cbee3a5},                      // 1.557407724654902
	{fn: "Tan", x: -1, want: 0xbff8eb245cbee3a5},                     // -1.557407724654902
	{fn: "Tan", x: 0.5, want: 0x3fe17b4f5bf3474a},                    // 0.5463024898437905
	{fn: "Tan", x: 1e-8, want: 0x3e45798ee2308c3a},                   // 1e-08
	{fn: "Tan", x: 3.141592653589793, want: 0xbca1a62633145c00},      // -1.2246467991473515e-16
	{fn: "Tan", x: 1.5707963267948966, want: 0x434d02967c31cdc0},     // 1.6331239353195392e+16
	{fn: "Tan", x: 1e5, want: 0xbfa250a9d503313d},                    // -0.035771662952898776
	{fn: "Tan", x: 1e9, want: 0x3fe4d8b249e3dba5},                    // 0.6514522021451411
	{fn: "Tan", x: 5.497633775832186e10, want: 0xbffeab05f33bffb5},   // -1.9167537213652228
	{fn: "Tan", x: 1e22, want: 0xbffa0f79c1b6b259},                   // -1.6287782256068992
	{fn: "Tan", x: 1.7976931348623157e308, want: 0xbf74530cfe72949d}, // -0.004962015874444917
	{fn: "Tan", x: 5e-324, want: 0x0000000000000001},                 // 5e-324
	{fn: "Tan", x: inf, isNaN: true},
	{fn: "Tan", x: ninf, isNaN: true},
	{fn: "Tan", x: nan, isNaN: true},
	{fn: "Tan", x: 0.212, want: 0x3fcb8ccb513fcf8e}, // 0.21523419825791418
	// Atan2
	{fn: "Atan2", x: 0, y: 1, want: 0x0000000000000000},       // 0
	{fn: "Atan2", x: nz, y: 1, want: 0x8000000000000000},      // -0
	{fn: "Atan2", x: 0, y: -1, want: 0x400921fb54442d18},      // 3.141592653589793
	{fn: "Atan2", x: nz, y: -1, want: 0xc00921fb54442d18},     // -3.141592653589793
	{fn: "Atan2", x: 0, y: nz, want: 0x400921fb54442d18},      // 3.141592653589793
	{fn: "Atan2", x: nz, y: nz, want: 0xc00921fb54442d18},     // -3.141592653589793
	{fn: "Atan2", x: 1, y: 0, want: 0x3ff921fb54442d18},       // 1.5707963267948966
	{fn: "Atan2", x: -1, y: 0, want: 0xbff921fb54442d18},      // -1.5707963267948966
	{fn: "Atan2", x: inf, y: inf, want: 0x3fe921fb54442d18},   // 0.7853981633974483
	{fn: "Atan2", x: ninf, y: inf, want: 0xbfe921fb54442d18},  // -0.7853981633974483
	{fn: "Atan2", x: inf, y: ninf, want: 0x4002d97c7f3321d2},  // 2.356194490192345
	{fn: "Atan2", x: ninf, y: ninf, want: 0xc002d97c7f3321d2}, // -2.356194490192345
	{fn: "Atan2", x: 1, y: inf, want: 0x0000000000000000},     // 0
	{fn: "Atan2", x: -1, y: inf, want: 0x8000000000000000},    // -0
	{fn: "Atan2", x: 1, y: ninf, want: 0x400921fb54442d18},    // 3.141592653589793
	{fn: "Atan2", x: -1, y: ninf, want: 0xc00921fb54442d18},   // -3.141592653589793
	{fn: "Atan2", x: inf, y: 1, want: 0x3ff921fb54442d18},     // 1.5707963267948966
	{fn: "Atan2", x: ninf, y: 1, want: 0xbff921fb54442d18},    // -1.5707963267948966
	{fn: "Atan2", x: nan, y: 1, isNaN: true},
	{fn: "Atan2", x: 1, y: nan, isNaN: true},
	{fn: "Atan2", x: 1, y: 2, want: 0x3fddac670561bb4f},          // 0.4636476090008061
	{fn: "Atan2", x: -3, y: -4, want: 0xc003fc176b7a8560},        // -2.498091544796509
	{fn: "Atan2", x: 3, y: -4, want: 0x4003fc176b7a8560},         // 2.498091544796509
	{fn: "Atan2", x: 0.6, y: 1, want: 0x3fe14b1dd5f90ce1},        // 0.5404195002705842
	{fn: "Atan2", x: 2.5, y: 1, want: 0x3ff30b6d796a4da8},        // 1.1902899496825317
	{fn: "Atan2", x: 1e300, y: 1e-300, want: 0x3ff921fb54442d18}, // 1.5707963267948966
	{fn: "Atan2", x: 5e-324, y: 1, want: 0x0000000000000001},     // 5e-324
	{fn: "Atan2", x: 0.309, y: 1.7, want: 0x3fc703be44ef3082},    // 0.17980173459944032
	// Pow
	{fn: "Pow", x: 2, y: 10, want: 0x4090000000000000},   // 1024
	{fn: "Pow", x: 2, y: 0.5, want: 0x3ff6a09e667f3bcd},  // 1.4142135623730951
	{fn: "Pow", x: 2, y: -0.5, want: 0x3fe6a09e667f3bcc}, // 0.7071067811865475
	{fn: "Pow", x: -2, y: 3, want: 0xc020000000000000},   // -8
	{fn: "Pow", x: -2, y: 0.5, isNaN: true},
	{fn: "Pow", x: 0, y: -1, want: 0x7ff0000000000000},    // +Inf
	{fn: "Pow", x: nz, y: -1, want: 0xfff0000000000000},   // -Inf
	{fn: "Pow", x: nz, y: 3, want: 0x8000000000000000},    // -0
	{fn: "Pow", x: 0, y: 3, want: 0x0000000000000000},     // 0
	{fn: "Pow", x: 0, y: -2, want: 0x7ff0000000000000},    // +Inf
	{fn: "Pow", x: 0, y: ninf, want: 0x7ff0000000000000},  // +Inf
	{fn: "Pow", x: 0, y: inf, want: 0x0000000000000000},   // 0
	{fn: "Pow", x: -1, y: inf, want: 0x3ff0000000000000},  // 1
	{fn: "Pow", x: 0.5, y: inf, want: 0x0000000000000000}, // 0
	{fn: "Pow", x: 2, y: ninf, want: 0x0000000000000000},  // 0
	{fn: "Pow", x: inf, y: 2, want: 0x7ff0000000000000},   // +Inf
	{fn: "Pow", x: inf, y: -2, want: 0x0000000000000000},  // 0
	{fn: "Pow", x: ninf, y: 3, want: 0xfff0000000000000},  // -Inf
	{fn: "Pow", x: ninf, y: -3, want: 0x8000000000000000}, // -0
	{fn: "Pow", x: nan, y: 1, isNaN: true},
	{fn: "Pow", x: 1, y: nan, want: 0x3ff0000000000000},                                    // 1
	{fn: "Pow", x: 7, y: 0, want: 0x3ff0000000000000},                                      // 1
	{fn: "Pow", x: nan, y: 0, want: 0x3ff0000000000000},                                    // 1
	{fn: "Pow", x: 3, y: 1, want: 0x4008000000000000},                                      // 3
	{fn: "Pow", x: 10, y: 308, want: 0x7fe1ccf385ebc8a3},                                   // 1.0000000000000006e+308
	{fn: "Pow", x: 10, y: 309, want: 0x7ff0000000000000},                                   // +Inf
	{fn: "Pow", x: 2, y: -1074, want: 0x0000000000000001},                                  // 5e-324
	{fn: "Pow", x: 2, y: -1075, want: 0x0000000000000000},                                  // 0
	{fn: "Pow", x: 0.040071135050271586, y: -25.421185145617727, want: 0x474fb76fcee2a31c}, // 3.2936348385197303e+35
	{fn: "Pow", x: 5.7800894285228965, y: -42.76587852748923, want: 0x392b0394f9138f77},    // 2.6013527955829284e-33
	{fn: "Pow", x: 1.0000001, y: 1e10, want: 0x7ff0000000000000},                           // +Inf
	{fn: "Pow", x: 3, y: 1e19, want: 0x7ff0000000000000},                                   // +Inf
	{fn: "Pow", x: 0.5, y: 1e19, want: 0x0000000000000000},                                 // 0
	{fn: "Pow", x: -1, y: 1e19, want: 0x3ff0000000000000},                                  // 1
	{fn: "Pow", x: 1.5, y: 2.75, want: 0x400865b271ccd152},                                 // 3.0496567621832265
	{fn: "Pow", x: 1.7, y: 0.521, want: 0x3ff5185fbd1648f2},                                // 1.3184506784642136
	{fn: "Pow", x: 1.7, y: 256.535, want: 0x4c34ea34005290c4},                              // 1.3128467874391292e+59
	// Expm1
	{fn: "Expm1", x: 0, want: 0x0000000000000000},                   // 0
	{fn: "Expm1", x: nz, want: 0x8000000000000000},                  // -0
	{fn: "Expm1", x: 1e-20, want: 0x3bc79ca10c924223},               // 1e-20
	{fn: "Expm1", x: 1e-10, want: 0x3ddb7cdfd9dda4e3},               // 1.00000000005e-10
	{fn: "Expm1", x: 0.1, want: 0x3fbaec7b35a00d3a},                 // 0.10517091807564763
	{fn: "Expm1", x: 0.14668497136738634, want: 0x3fc438fcad596ef6}, // 0.15798910583804798
	{fn: "Expm1", x: 0.3, want: 0x3fd6641632306a56},                 // 0.3498588075760031
	{fn: "Expm1", x: 0.4, want: 0x3fdf7a0e4beeff80},                 // 0.49182469764127035
	{fn: "Expm1", x: 0.5, want: 0x3fe4c2531c3c0d38},                 // 0.6487212707001282
	{fn: "Expm1", x: -0.5, want: 0xbfd92e9a0720d3ec},                // -0.3934693402873666
	{fn: "Expm1", x: 0.8, want: 0x3ff39bd0cdfc8dd1},                 // 1.2255409284924677
	{fn: "Expm1", x: 1.2, want: 0x40028f99761065a4},                 // 2.3201169227365472
	{fn: "Expm1", x: -2, want: 0xbfebab5557101f8d},                  // -0.8646647167633873
	{fn: "Expm1", x: 10, want: 0x40d5825dcf950560},                  // 22025.465794806718
	{fn: "Expm1", x: 30, want: 0x42a370470aec26ed},                  // 1.0686474581523463e+13
	{fn: "Expm1", x: 40, want: 0x438a220d397972ea},                  // 2.3538526683701997e+17
	{fn: "Expm1", x: -30, want: 0xbfeffffffffffcb5},                 // -0.9999999999999064
	{fn: "Expm1", x: -40, want: 0xbff0000000000000},                 // -1
	{fn: "Expm1", x: 709.7, want: 0x7fed75ae7a50ee14},               // 1.6549840276802644e+308
	{fn: "Expm1", x: 709.8, want: 0x7ff0000000000000},               // +Inf
	{fn: "Expm1", x: inf, want: 0x7ff0000000000000},                 // +Inf
	{fn: "Expm1", x: ninf, want: 0xbff0000000000000},                // -1
	{fn: "Expm1", x: nan, isNaN: true},
	// Erf
	{fn: "Erf", x: 0, want: 0x0000000000000000},                   // 0
	{fn: "Erf", x: nz, want: 0x8000000000000000},                  // -0
	{fn: "Erf", x: 5e-324, want: 0x0000000000000001},              // 5e-324
	{fn: "Erf", x: 1e-310, want: 0x000014c5898977c4},              // 1.1283791670955e-310
	{fn: "Erf", x: 1e-20, want: 0x3bcaa4a230244ae0},               // 1.1283791670955125e-20
	{fn: "Erf", x: 0.1, want: 0x3fbcca5ea24fb334},                 // 0.1124629160182849
	{fn: "Erf", x: 0.22917078131981217, want: 0x3fd043bf50375a1f}, // 0.2541349681245339
	{fn: "Erf", x: 0.5, want: 0x3fe0a7ef5c18edd2},                 // 0.5204998778130465
	{fn: "Erf", x: -0.5, want: 0xbfe0a7ef5c18edd2},                // -0.5204998778130465
	{fn: "Erf", x: 0.9, want: 0x3fe98045a6c8a2e6},                 // 0.7969082124228322
	{fn: "Erf", x: 1, want: 0x3feaf767a741088b},                   // 0.8427007929497149
	{fn: "Erf", x: -1.2, want: 0xbfed214ac7b2d067},                // -0.9103139782296353
	{fn: "Erf", x: 1.5911460540786109, want: 0x3fef37d42b70e196},  // 0.9755650375800993
	{fn: "Erf", x: 2, want: 0x3fefd9ae142795e3},                   // 0.9953222650189527
	{fn: "Erf", x: 2.5, want: 0x3feffcaa8f4c9bea},                 // 0.999593047982555
	{fn: "Erf", x: 3, want: 0x3fefffd1ac4135f9},                   // 0.9999779095030014
	{fn: "Erf", x: -4, want: 0xbfeffffff7b91176},                  // -0.9999999845827421
	{fn: "Erf", x: 5.9, want: 0x3fefffffffffffff},                 // 0.9999999999999999
	{fn: "Erf", x: 6, want: 0x3ff0000000000000},                   // 1
	{fn: "Erf", x: 30, want: 0x3ff0000000000000},                  // 1
	{fn: "Erf", x: inf, want: 0x3ff0000000000000},                 // 1
	{fn: "Erf", x: ninf, want: 0xbff0000000000000},                // -1
	{fn: "Erf", x: nan, isNaN: true},
	// Erfinv
	{fn: "Erfinv", x: 0, want: 0x0000000000000000},                   // 0
	{fn: "Erfinv", x: nz, want: 0x8000000000000000},                  // -0
	{fn: "Erfinv", x: 1e-300, want: 0x01a2fdf36bf69ba0},              // 8.862269254527582e-301
	{fn: "Erfinv", x: 0.03401419535294149, want: 0x3f9ee08a92787d9b}, // 0.030153432073459645
	{fn: "Erfinv", x: 0.5, want: 0x3fde861fbb24c00a},                 // 0.4769362762044699
	{fn: "Erfinv", x: -0.85, want: 0xbff04954184f8aa5},               // -1.0179024648320276
	{fn: "Erfinv", x: 0.85, want: 0x3ff04954184f8aa5},                // 1.0179024648320276
	{fn: "Erfinv", x: 0.9, want: 0x3ff29c014677064f},                 // 1.1630871536766738
	{fn: "Erfinv", x: -0.99, want: 0xbffd2466082bb20c},               // -1.821386367718449
	{fn: "Erfinv", x: 0.999999, want: 0x400babd964830d57},            // 3.4589107372754984
	{fn: "Erfinv", x: 1 - 0x1p-40, want: 0x4014347bf36fbae8},         // 5.05125408524939
	{fn: "Erfinv", x: 1 - 0x1p-52, want: 0x40173856d153f080},         // 5.8050186831934525
	{fn: "Erfinv", x: -(1 - 0x1p-52), want: 0xc0173856d153f080},      // -5.8050186831934525
	{fn: "Erfinv", x: 1, want: 0x7ff0000000000000},                   // +Inf
	{fn: "Erfinv", x: -1, want: 0xfff0000000000000},                  // -Inf
	{fn: "Erfinv", x: 1.5, isNaN: true},
	{fn: "Erfinv", x: -1.5, isNaN: true},
	{fn: "Erfinv", x: nan, isNaN: true},
	// Asin
	{fn: "Asin", x: 0, want: 0x0000000000000000},                   // 0
	{fn: "Asin", x: nz, want: 0x8000000000000000},                  // -0
	{fn: "Asin", x: 5e-324, want: 0x0000000000000001},              // 5e-324
	{fn: "Asin", x: 1e-300, want: 0x01a56e1fc2f8f359},              // 1e-300
	{fn: "Asin", x: 1e-20, want: 0x3bc79ca10c924223},               // 1e-20
	{fn: "Asin", x: 0.1, want: 0x3fb9a49276037884},                 // 0.1001674211615598
	{fn: "Asin", x: 0.23195033481076083, want: 0x3fcdf667feeff5d8}, // 0.234082221490026
	{fn: "Asin", x: 0.2666358693828154, want: 0x3fd1460e2df134ae},  // 0.2699008415846248
	{fn: "Asin", x: 0.5, want: 0x3fe0c152382d7366},                 // 0.5235987755982989
	{fn: "Asin", x: -0.5, want: 0xbfe0c152382d7366},                // -0.5235987755982989
	{fn: "Asin", x: 0.69999, want: 0x3fe8cff10b85e79c},             // 0.7753834939060087
	{fn: "Asin", x: 0.7, want: 0x3fe8d00e692afd95},                 // 0.775397496610753
	{fn: "Asin", x: 0.70001, want: 0x3fe8d02bc6ea7dd5},             // 0.7754114995076927
	{fn: "Asin", x: 0.71792499768851, want: 0x3fe9a04ac875528f},    // 0.8008169093323704
	{fn: "Asin", x: 0.8160808954098611, want: 0x3fee8c0effbaf9f0},  // 0.9545969958051597
	{fn: "Asin", x: 0.9, want: 0x3ff1ea93705fa172},                 // 1.1197695149986342
	{fn: "Asin", x: 0.925, want: 0x3ff2e585943a1802},               // 1.181035593997422
	{fn: "Asin", x: -0.925, want: 0xbff2e585943a1802},              // -1.181035593997422
	{fn: "Asin", x: -0.9893726507422578, want: 0xbff6cc4bd0f2f837}, // -1.4248769914697854
	{fn: "Asin", x: 0.99, want: 0x3ff6de3c6f33d51d},                // 1.4292568534704693
	{fn: "Asin", x: 0.9999, want: 0x3ff8e80e1a015571},              // 1.5566540733173861
	{fn: "Asin", x: 1, want: 0x3ff921fb54442d18},                   // 1.5707963267948966
	{fn: "Asin", x: -1, want: 0xbff921fb54442d18},                  // -1.5707963267948966
	{fn: "Asin", x: 1 + 0x1p-52, isNaN: true},
	{fn: "Asin", x: -1.5, isNaN: true},
	{fn: "Asin", x: inf, isNaN: true},
	{fn: "Asin", x: nan, isNaN: true},
}

func eval(c goldenCase) float64 {
	switch c.fn {
	case "Exp":
		return Exp(c.x)
	case "Log":
		return Log(c.x)
	case "Log1p":
		return Log1p(c.x)
	case "Sin":
		return Sin(c.x)
	case "Cos":
		return Cos(c.x)
	case "Tan":
		return Tan(c.x)
	case "Atan2":
		return Atan2(c.x, c.y)
	case "Pow":
		return Pow(c.x, c.y)
	case "Expm1":
		return Expm1(c.x)
	case "Erf":
		return Erf(c.x)
	case "Erfinv":
		return Erfinv(c.x)
	case "Asin":
		return Asin(c.x)
	}
	panic("unknown kernel " + c.fn)
}

// TestDetmath_GoldenBits is the cross-architecture gate: every kernel's
// result for the frozen table must equal the pinned bits exactly, on
// every GOARCH and CPU. A dropped float64(...) barrier moves these bits
// on a contracting architecture (arm64 locally); a
// flipped constant or a mis-ported branch moves them everywhere.
func TestDetmath_GoldenBits(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range goldenCases {
		seen[c.fn] = true
		got := eval(c)
		if c.isNaN {
			if !math.IsNaN(got) {
				t.Errorf("%s(%v, %v) = %v (0x%016x), want NaN", c.fn, c.x, c.y, got, math.Float64bits(got))
			}
			continue
		}
		if math.IsNaN(got) || math.Float64bits(got) != c.want {
			t.Errorf("%s(%v, %v) = %v (0x%016x), want %v (0x%016x)",
				c.fn, c.x, c.y, got, math.Float64bits(got), math.Float64frombits(c.want), c.want)
		}
	}
	for _, fn := range []string{"Exp", "Log", "Log1p", "Sin", "Cos", "Tan", "Atan2", "Pow", "Expm1", "Erf", "Erfinv", "Asin"} {
		if !seen[fn] {
			t.Errorf("golden table has no %s row", fn)
		}
	}
}
