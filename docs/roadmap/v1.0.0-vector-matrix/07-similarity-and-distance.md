# 07 — Similarity & distance on non-embedding vectors

**Question from the planning review:** do cosine similarity and the other functions popularised by embeddings make sense for Pulse's vectors, which are rating batteries, time profiles, share-of-spend splits and multi-select sets rather than model embeddings?

**Short answer:** most of them do. The maths is just geometry, and much of it predates embeddings by decades: cosine similarity comes from 1970s information retrieval, and Jaccard is from 1901. But there is one rule embeddings let you ignore that Pulse cannot: **the right metric depends on what the vector means.** Embeddings are trained so that cosine works. Survey and business vectors are not, and the naive choice can give confidently wrong answers.

---

## The rule: match the metric to the vector's meaning

Pulse's vectors fall into five kinds:

| Vector kind | Examples | What "similar" should mean | Good metrics | Poor fits |
|---|---|---|---|---|
| **measure** — magnitude matters | hourly kWh load curve, spend per category, store KPIs | close in level *and* shape | Euclidean (standardized if units differ), Manhattan, Mahalanobis | cosine (ignores level: a 10-unit store looks identical to a 10,000-unit store with the same mix) |
| **scale** — bounded ratings | 1–7 Likert battery, brand × attribute ratings | same *pattern of preference* | **correlation** (= centered cosine), Euclidean on ipsatized values | **raw cosine** (see the trap below) |
| **composition** — parts of a whole | share of wallet, channel mix, product mix | same *proportions* | Aitchison (log-ratio), Hellinger, Manhattan on shares (= 2 × total variation distance), cosine is acceptable | Euclidean on raw amounts |
| **binary / set** — membership | `set_*` multi-selects, `packed_bool` batteries, baskets | share the same members | Jaccard, Dice, Hamming, overlap coefficient | cosine on 0/1 (works, but Jaccard is what analysts expect and can explain) |
| **profile of a crosstab row** | brand's attribute profile from a brand × attribute table | same relative profile | chi-square distance (what correspondence analysis uses), correlation | Euclidean on raw counts (dominated by row size) |

### The cosine trap on rating scales

Two respondents rate four items on a 1–7 scale:

```
A = [6, 7, 6, 7]     prefers items 2 and 4
B = [7, 6, 7, 6]     prefers items 1 and 3 — the exact opposite pattern
```

- `cosine(A, B) = 168 / 170 = 0.988` → "almost identical".
- `correlation(A, B) = −1.0` → "perfectly opposite preferences".

All ratings are positive and clustered near the top of the scale, so every vector points the same general direction, and cosine mostly measures *both people rated things highly*. Centering each vector first, i.e. subtracting its own mean, turns cosine into Pearson correlation and recovers the real answer. This is why recommender-system practice calls it "adjusted cosine". Pulse should make the safe choice the default, not something the analyst has to know.

---

## Function-by-function verdict

| Embedding-world function | Verdict for Pulse | Where it lands |
|---|---|---|
| **Cosine similarity** | ✅ for composition, binary and crosstab profiles; ⚠️ on scale vectors only after centering | `cosine()` expr function, metric option everywhere; a warning when used raw on a `scale` vector |
| **Dot product** | ✅ a weighted score, e.g. importance weights · performance ratings = a driver-weighted index; quantity · price = basket value | `dot()` expr function (already in F5) |
| **Euclidean / L2 distance** | ✅ for measures; standardize first when units differ | metric option; `ATTR_VEC_DISTANCE` |
| **Manhattan / L1** | ✅ robust to one wild element; on shares it is "total share shift" | metric option |
| **L2 normalization** (unit vectors) | ⚠️ mostly a precursor to cosine; little standalone meaning | internal only |
| **L1 normalization** (to shares) | ✅ turns amounts into a composition | `vnormalize(v, "l1")` expr function |
| **Mean pooling / centroid** | ✅ the average profile of a segment | `AGG_VEC_MEAN` (already planned) |
| **Exact top-k nearest neighbours** | ✅ "the 20 stores most like this one", "respondents closest to persona X", matched controls | new: exact, streamable top-k (below) |
| **Approximate NN indexes** (HNSW, IVF) | ❌ non-deterministic and index-maintenance heavy; exact search is fast enough at Pulse's p and n | excluded |
| **k-means / clustering** | ✅ segmentation | `GROUP_KMEANS` (now committed) |
| **PCA** | ✅ | `MAT_PCA` |
| **t-SNE / UMAP** | ❌ stochastic, non-linear, visual-only; the coordinates have no stable meaning | excluded; use correspondence analysis or PCA for maps |
| **Hamming distance** | ✅ for binary batteries and set masks: "how many answers differ" | `hamming()` expr function |
| **Jaccard / Dice / overlap** | ✅ the natural similarity for Pulse's existing `set_*` fields | expr functions + `MAT_SET_AFFINITY` (below) |
| **Quantization / binarization** | ❌ storage tricks for billion-vector search; `vec_u8` already covers compact grids | excluded |
| **Softmax** | ❌ in general. The one legitimate use is share-of-preference simulation from choice-model utilities (conjoint), which is a separate topic | post-1.0, only with a conjoint theme |
| **Maximal marginal relevance / diversity selection** | ⚠️ conceivable for picking a diverse sample of verbatims or stores; low demand | not planned |

---

## New proposals arising from this review

### S1. One shared metric registry — `linalg/metric` (Committed)
Every similarity or distance consumer resolves `metric: "<name>"` through one registry:
- expr functions;
- `ATTR_VEC_DISTANCE` / `ATTR_PROFILE_SIMILARITY`;
- `MAT_DISTANCE`;
- `OVERLAY_PROFILE_SIMILARITY`;
- `GROUP_KMEANS`;
- top-k.

That registry gives:
- one implementation;
- one NaN policy;
- one manifest list (`capabilities.matrix.metrics`);
- one atomic skill per metric family.

**v1.0.0 set:**
- `euclidean`, `sq_euclidean`, `std_euclidean`
- `manhattan`, `chebyshev`
- `cosine`, `correlation`
- `mahalanobis`
- `chi_square`, `hellinger`, `aitchison`
- `jaccard`, `dice`, `hamming`, `overlap`

Each metric declares the vector kinds it suits and whether it is a similarity (higher means closer) or a distance. Outputs never mix the two silently; similarity-returning operators say so in their output key.

`GROUP_KMEANS` accepts only the Euclidean family. k-means minimizes squared Euclidean error, so a "cosine k-means" is a different algorithm (spherical k-means) and is out of scope.

### S2. Vector `kind` with metric defaults (Committed)
`Request.Vectors[i]` and the native `vec_*` schema section gain an optional `kind`: `measure` | `scale` | `composition` | `binary`. It works like the existing smart defaults:

| Kind | Default metric | Default similarity |
|---|---|---|
| measure | `std_euclidean` | — |
| scale | `euclidean` | `correlation` |
| composition | `aitchison` | `cosine` |
| binary | `hamming` | `jaccard` |

- An explicit `metric` always wins. Defaults never cross kinds. Predict reports `DefaultsApplied`.
- Raw `cosine` on a `scale` vector raises the warning `PULSE_VECTOR_METRIC_UNSUITED`, naming the suggested alternative. With `--strict` it is an error.
- No `kind` means no defaults and no warnings; the operator then requires an explicit metric.
- The native vector section already reserves a "compositional" flag bit (F4), and that becomes `kind`.
- Under `kind: scale`, the declared `scale_min` / `scale_max` also feed reverse-scoring in `MAT_RELIABILITY` and `ATTR_SCALE_SCORE`.

### S3. Row-wise centering & ipsatization in expr (Committed, extends F5)
`vcenter(v)`, `vzscore(v)` and `vnormalize(v, "l1"|"l2")` return vectors usable *inside* an expression, e.g. `cosine(vcenter(a), vcenter(b))`.

Within-respondent standardization (ipsatization) is a recognised survey technique for removing response style: acquiescence, and extreme or midpoint responding. The top-level expression result stays scalar, as the `ATTR_FORMULA` contract requires.

### S4. Set similarity on existing `set_*` fields (Committed)
Pulse already stores multi-selects as bitmasks over a dictionary, so set similarity is close to free:
- **expr functions:** `jaccard(a, b)`, `dice(a, b)`, `hamming(a, b)`, `overlap(a, b)` over two set fields (or a set field and a literal list of members). On the mask representation each is a popcount, so it's fast even on `set_u256`.
- **`MAT_SET_AFFINITY` (Stretch):** a members × members matrix for one set field (Stretch rather than Committed because it is a new matrix operator rather than a function).
  - `primary`: co-occurrence count.
  - `auxiliary`: jaccard, lift (`P(a∧b) / P(a)P(b)`), conditional probability `P(b|a)`.
  - Answers "which brands are considered together?", "which products are bought together?", "which issues are mentioned together?".
  - It is pure counting, so it is **mergeable and streamable**.
  - The dictionary size bounds the matrix (≤ 256 for `set_u256`), so the size is known at predict.

### S5. Exact top-k nearest rows (Stretch)
"Find the k rows most similar to this reference vector" without an index:
- **Composition.** `ATTR_VEC_DISTANCE` (or `ATTR_PROFILE_SIMILARITY`) plus `Sort` plus a row limit.
- **If no row limit exists.** Where the request surface has no general row limit, add a dedicated bounded top-k path: a fixed-size heap per worker, merged at the end. That is **mergeable**, O(n · p) time and O(k) memory, and deterministic with ties broken by record position.
- **Reference.** The reference vector comes from a literal, a lookup table, a group centroid (`AGG_VEC_MEAN` of a named filter), or a specific record found via the point-lookup sidecar.
- **Uses.**
  - lookalike stores for test/control matching;
  - "respondents most like persona X" for qualitative recruitment;
  - nearest historical week to this week's profile.
- **Why exact is enough.** At p ≤ a few hundred and n in the tens of millions, one pass is seconds, and Pulse already does full scans. Approximate indexes would add non-determinism and a new sidecar class for little gain.

---

## What this changes elsewhere in the roadmap

- **00 feature map:** adds the metric registry, vector `kind`, set similarity, centering functions (Committed), and `MAT_SET_AFFINITY` and exact top-k (Stretch).
- **01 F5:** the expr function table gains `vcenter`, `vzscore`, `vnormalize`, `jaccard`, `dice`, `hamming`, `overlap`.
- **03:** `MAT_DISTANCE`, `ATTR_VEC_DISTANCE`, `ATTR_PROFILE_SIMILARITY` and `GROUP_KMEANS` take `metric` from the registry; `GROUP_KMEANS` is Euclidean-only.
- **05 X6 error codes:** add `PULSE_VECTOR_METRIC_UNSUITED` (warning) and `PULSE_VECTOR_METRIC_UNKNOWN`.
- **06:** metric registry and vector `kind` go into E5. Set similarity functions go into E5. `MAT_SET_AFFINITY` and top-k go into E8.
