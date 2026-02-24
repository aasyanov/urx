# hashx

Password hashing for industrial Go services.

## Philosophy

One concern — **password hashing and verification**. Three algorithms
(Argon2id, scrypt, bcrypt) behind a unified API. Three security tiers
(Min, Default, Max) for quick setup. No framework, no reflection.

## Quick start

```go
// Hash a password (Argon2id, TierDefault)
hash, err := hashx.Generate(ctx, "secret")

// Verify
err = hashx.Compare(ctx, hash, "secret")
```

Custom hasher with pepper:

```go
h := hashx.New(
    hashx.WithAlgorithm(hashx.Argon2id),
    hashx.WithTier(hashx.TierMax),
    hashx.WithPepper([]byte(os.Getenv("PASSWORD_PEPPER"))),
)
hash, err := h.Generate(ctx, password)
```

Transparent migration after login:

```go
if h.NeedsRehash(storedHash) {
    newHash, _ := h.Generate(ctx, password)
    // update DB
}
```

## API

| Function / Method | Description |
|---|---|
| `New(opts ...Option) *Hasher` | Create a hasher |
| `h.Generate(ctx, password) (string, error)` | Derive a hash from password |
| `h.Compare(ctx, hash, password) error` | Verify password against hash (auto-detects algorithm) |
| `h.NeedsRehash(hash) bool` | Report whether hash needs upgrade (algorithm or tier changed) |
| `Generate(ctx, password)` | Convenience: Argon2id at TierDefault (no pepper) |
| `Compare(ctx, hash, password)` | Convenience: auto-detect algorithm (no pepper) |

## Options

| Option | Default | Description |
|---|---|---|
| `WithAlgorithm(alg)` | Argon2id | Select algorithm |
| `WithTier(tier)` | TierDefault | Select security level |
| `WithPepper([]byte)` | none | Server-side secret mixed into every password via HMAC-SHA256 |
| `WithArgon2Params(mem, iter, par)` | — | Custom Argon2id parameters |
| `WithScryptParams(n, r, p)` | — | Custom scrypt parameters |
| `WithBcryptCost(cost)` | — | Custom bcrypt cost (4–31) |

## Algorithms and tiers

| Algorithm | TierMin | TierDefault | TierMax |
|---|---|---|---|
| Argon2id | 32 MiB, 2 iter, 2 par | 64 MiB, 3 iter, 4 par | 128 MiB, 4 iter, 4 par |
| Scrypt | N=16384, r=8, p=1 | N=32768, r=8, p=1 | N=65536, r=8, p=1 |
| Bcrypt | cost 10 | cost 12 | cost 14 |

Argon2id parameters meet or exceed OWASP 2025–2026 recommendations
(RFC 9106: minimum 19 MiB, 2 iterations). TierDefault is the right
choice for 95% of production services.

## Hash formats

| Algorithm | Format |
|---|---|
| Argon2id | `$argon2id$v=19$m=<mem>,t=<iter>,p=<par>$<salt>$<key>` |
| Scrypt | `<N>:<r>:<p>:<salt>:<key>` |
| Bcrypt | Standard `$2a$`/`$2b$` format |

`Compare` auto-detects the algorithm from the hash prefix, so a single
`Hasher` can verify hashes produced by any algorithm.

## Behavior details

- **Nil context**: `Generate` and `Compare` treat a nil context as
  `context.Background()`.
- **Context cancellation**: all KDF operations run in a goroutine and
  respect context cancellation. If the context is cancelled before the
  KDF completes, a `CANCELLED` error is returned immediately.
- **Pepper**: when configured via `WithPepper`, every password is
  preprocessed with `HMAC-SHA256(pepper, password)` before being passed
  to the KDF. This produces a fixed-length 32-byte output, which also
  avoids bcrypt's 72-byte truncation problem. The pepper byte slice is
  defensively copied — mutating the original after `New` has no effect.
  The same pepper must be used for both `Generate` and `Compare`.
- **Scrypt DoS protection**: `Compare` validates scrypt N/r/p parsed
  from the hash before executing the KDF. Values exceeding safe bounds
  are rejected with `INVALID_HASH`.
- **Bcrypt error mapping**: malformed bcrypt hashes return `INVALID_HASH`,
  not `MISMATCH`.
- **Bcrypt 72-byte limit**: bcrypt silently truncates passwords longer
  than 72 bytes. When pepper is enabled, HMAC-SHA256 output is always
  44 bytes (base64), so truncation does not occur. Without pepper,
  use Argon2id for passwords that may exceed 72 bytes.

## Error diagnostics

All errors are `*errx.Error` with domain `HASH`:

| Code | Meaning |
|---|---|
| `EMPTY_PASSWORD` | Empty password or empty hash provided |
| `MISMATCH` | Password does not match hash (severity: Warn) |
| `INVALID_HASH` | Stored hash is malformed or has unsafe parameters |
| `INTERNAL` | Cryptographic operation failed |
| `CANCELLED` | Context cancelled during KDF |

## Thread safety

`Hasher` is immutable after construction. Concurrent calls to `Generate`,
`Compare`, and `NeedsRehash` are safe. The convenience functions `Generate`
and `Compare` create a fresh `Hasher` per call and are also safe.

## Tests

**38 tests, 89.5% statement coverage.**

```bash
go test -race -count=1 -cover ./pkg/hashx
ok  github.com/aasyanov/urx/pkg/hashx  coverage: 89.5% of statements
```

Coverage includes:
- Generate/Compare: all three algorithms at all tiers
- Custom parameters: Argon2, scrypt, bcrypt
- Auto-detection across algorithms
- NeedsRehash: same params, different algorithm, upgraded tier, malformed
- Pepper: generate+compare with pepper, wrong pepper mismatch,
  no-pepper vs pepper mismatch, all three algorithms with pepper,
  defensive copy verification
- Nil context handling
- Empty password/hash rejection
- Invalid/malformed hash detection (argon2, scrypt, bcrypt)
- Bcrypt malformed hash returns INVALID_HASH (not MISMATCH)
- Scrypt DoS protection (huge N rejected)
- Context cancellation during KDF
- Error constructors and domain/code constants

### Benchmark analysis

**Argon2id TierMin:** ~40 ms/op, 33 MiB/op — this is the KDF computation itself.
The memory allocation is intentional (Argon2 is memory-hard by design). In
production, use a worker pool to limit concurrent hash operations and prevent
OOM under load.

**Bcrypt TierMin:** ~66 ms/op — CPU-bound, lower memory. Suitable when memory
is constrained.

All benchmarks reflect real-world KDF cost, not framework overhead.

## What hashx does NOT do

- **Store hashes** — that is your database layer's job.
- **Rotate peppers** — manage pepper versions in your auth service.
- **Rate-limit** — brute-force protection belongs in your auth middleware.
- **Log attempts** — emit metrics/logs in your auth service, not here.
- **Hash non-passwords** — use `crypto/sha256` or similar for data integrity.

## File structure

```text
pkg/hashx/
    hashx.go       -- Hasher, New, Generate, Compare, NeedsRehash, applyPepper
    errors.go      -- DomainHash, Code constants, error constructors
    hashx_test.go  -- 38 tests + 3 benchmarks, 89.5% coverage
    README.md
```
