# Third-party notices

Pulse is MIT-licensed (see `LICENSE`). Portions of it are derived from the
third-party software below, whose licences require this notice to ship with
every source and binary distribution.

## ReproBLAS

`internal/reprosum` (binned reproducible summation) is a Go port of the
scalar double-precision binned kernels of ReproBLAS v2.1.0 by Peter (Willow)
Ahrens, James Demmel and Hong Diep Nguyen,
<https://github.com/peterahrens/ReproBLAS>.

```
Software Development License

Copyright © 2016, University of California All rights reserved.

Redistribution and use in source and binary forms, with or without modification,
are permitted provided that the following conditions are met:

* Redistributions of source code must retain the above copyright notice, this list
of conditions and the disclaimer below;

* Redistributions in binary form must reproduce the above copyright notice, this
list of conditions and the disclaimer below in the documentation and/or other
materials provided with the distribution.

* Neither the name of the University of California nor the names of its
contributors may be used to endorse or promote products derived from this
software without specific prior written permission.

DISCLAIMER

THIS SOFTWARE IS PROVIDED BY UNIVERSITY OF CALIFORNIA AS IS AND ANY EXPRESS OR
IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF
MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT
SHALL UNIVERSITY OF CALIFORNIA BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO,
PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR
BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN
CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING
IN ANY WAY OUT OF THE USE OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY
OF SUCH DAMAGE.
```

## The Go standard library

`internal/detmath` (deterministic transcendental kernels) is a port of the
pure-Go `exp`, `expm1`, `log`, `log1p`, `sin`, `cos`, `tan`, `trigReduce`,
`atan`, `atan2`, `asin`, `pow`, `erf` and `erfinv` sources of the Go 1.26 standard
library (`src/math/`), and of its ziggurat `NormFloat64` / `ExpFloat64` and
their `kn` / `wn` / `fn` / `ke` / `we` / `fe` tables (`src/math/rand/v2/normal.go`,
`exp.go`),
<https://go.dev/>, with every floating-point product wrapped in an explicit
`float64(...)` (or, for float32 arithmetic, `float32(...)`) conversion. Those sources in turn carry the FreeBSD msun
(Sun Microsystems) and Cephes (Stephen L. Moshier) notices reproduced in the
ported files.

```
Copyright 2009 The Go Authors.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following disclaimer
in the documentation and/or other materials provided with the
distribution.
   * Neither the name of Google LLC nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

## Genz's bivariate normal distribution (MVBVU, MVPHI)

`internal/sem/bvn.go` and the normal CDF in `internal/sem/normal.go` are
FMA-free Go translations of Alan Genz's `MVBVU` (after Drezner and
Wesolowsky, *J. Stat. Comput. Simul.* 35 (1989) pp. 101-107, with
double-precision extensions by Alan Genz and Yihong Ge) and `MVPHI`
(after Schonfelder, *Math. Comp.* 32 (1978) pp. 1232-1240), including their
Gauss-Legendre and Chebyshev coefficient tables, from `mvtdstpack.f`,
<https://www.math.wsu.edu/faculty/genz/software/fort77/mvtdstpack.f>,
by Alan Genz, Department of Mathematics, Washington State University.
`internal/sem/normal.go`'s normal quantile implements Wichura's Algorithm
AS 241 (*Applied Statistics* 37 (1988) pp. 477-484) from its published
coefficients.
