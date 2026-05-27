# Request Hashing

**Audience:** Go embedders building caches, deduplication layers, or
materialization stores on top of Pulse.

Every Pulse request type implements `Hash() string` — a 32-character
hex digest of the request's canonical JSON form. Same logical request
produces the same hash, across processes and across Pulse versions
where the request's semantic meaning is unchanged.

## Supported types

| Request shape | Method |
|---|---|
| `pulse.Request` | `req.Hash()` |
| `pulse.ComposedRequest` | `req.Hash()` |
| `pulse.FacetRequest` | `req.Hash()` |
| `pulse.ChainRequest` | `req.Hash()` |
| `pulse.SynthSpec` (`synth.Spec`) | `spec.Hash()` |

The lower-level helper `types.CanonicalHash(tag, v)` hashes any
JSON-serializable value with a caller-chosen namespace tag.

## Guarantees

- **Deterministic.** Same in, same out — every call, every process.
- **Round-trip stable.** `json.Marshal` → `json.Unmarshal` → `Hash()`
  returns the original digest.
- **Field-order invariant.** Struct field order normalised by the
  canonical encoder; map keys sorted.
- **Default-normalising.** Explicit zero (`Limit: 0`) and the omitted
  equivalent hash identically because the request struct tags carry
  `omitempty`.
- **Negative-zero collapsing.** `-0.0` hashes identically to `0.0`.
- **Type-namespaced.** A `Request` and a `ComposedRequest` with
  identical wire bytes never collide — the hash function prefixes a
  namespace tag per request shape.

## Use cases

### Cache key

```go
key := req.Hash()
if hit, ok := cache.Get(key); ok {
    return hit
}
resp, err := p.Process(ctx, req)
if err == nil {
    cache.Set(key, resp)
}
```

### Filename suffix

```go
out := fmt.Sprintf("derived_%s.pulse", req.Hash())
```

### Idempotency token

```go
// Reject duplicate work in a queue / job system.
if seen.Add(req.Hash()) == false {
    return errAlreadyQueued
}
```

### Dedup across consumers

Independent processes can build the same `Request` (programmatically
or from JSON) and reach the same hash without coordination. Combined
with [Deterministic FilterToFile](./filter-to-file.md), this is the
foundation for shared materialization caches.

## Algorithm

1. `json.Marshal(v)` — produces canonical-ordered struct fields and
   sorted map keys.
2. Walk the resulting JSON tree, dropping representational variants
   (collapse `-0.0` → `0.0`, integer-valued floats keep integer form).
3. Re-emit with sorted map keys and no whitespace.
4. `SHA-256(tag + 0x00 + canonical)`.
5. First 16 bytes as 32-character lowercase hex.

The implementation lives in `types/hash.go`; tests covering the
guarantees above are in `types/hash_test.go`.
