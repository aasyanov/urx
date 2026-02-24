// Package hashx provides password hashing for industrial Go services.
//
// A [Hasher] generates and verifies password hashes using one of three
// algorithms: [Argon2id] (recommended), [Scrypt], or [Bcrypt].
// Three security tiers are available: [TierMin], [TierDefault], [TierMax].
//
//	h := hashx.New(hashx.WithAlgorithm(hashx.Argon2id))
//
//	hash, err := h.Generate(ctx, "secret")
//	err = h.Compare(ctx, hash, "secret")
//
// Convenience functions [Generate] and [Compare] use Argon2id with
// [TierDefault] and are sufficient for most applications.
//
package hashx

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/scrypt"
)

// --- Algorithm ---

// Algorithm selects the password hashing algorithm.
type Algorithm uint8

const (
	// Argon2id is the recommended modern algorithm (RFC 9106).
	Argon2id Algorithm = iota
	// Scrypt is a memory-hard algorithm (RFC 7914).
	Scrypt
	// Bcrypt is the legacy but widely-used algorithm.
	Bcrypt
)

// String returns the canonical lowercase name ("argon2id", "scrypt", "bcrypt").
func (a Algorithm) String() string {
	switch a {
	case Argon2id:
		return "argon2id"
	case Scrypt:
		return "scrypt"
	case Bcrypt:
		return "bcrypt"
	default:
		return "unknown"
	}
}

// --- Tier ---

// Tier selects a pre-configured security level that determines the
// algorithm parameters (memory, iterations, cost).
type Tier uint8

const (
	// TierMin provides a solid baseline. Suitable for development or
	// low-value assets.
	TierMin Tier = iota
	// TierDefault provides the recommended balance between security and
	// performance for typical production use.
	TierDefault
	// TierMax provides stronger brute-force resistance at the expense
	// of higher computational cost.
	TierMax
)

// --- Configuration ---

type config struct {
	algorithm Algorithm

	// Argon2id params
	argonMemory      uint32
	argonIterations  uint32
	argonParallelism uint8

	// Scrypt params
	scryptN int
	scryptR int
	scryptP int

	// Bcrypt params
	bcryptCost int

	saltLen int
	keyLen  int

	pepper []byte
}

func defaultConfig() config {
	return tierParams(Argon2id, TierDefault)
}

func tierParams(alg Algorithm, tier Tier) config {
	c := config{algorithm: alg, saltLen: 16, keyLen: 32}
	switch alg {
	case Argon2id:
		switch tier {
		case TierMin:
			c.argonMemory, c.argonIterations, c.argonParallelism = 32*1024, 2, 2
		case TierMax:
			c.argonMemory, c.argonIterations, c.argonParallelism = 128*1024, 4, 4
		default:
			c.argonMemory, c.argonIterations, c.argonParallelism = 64*1024, 3, 4
		}
	case Scrypt:
		switch tier {
		case TierMin:
			c.scryptN, c.scryptR, c.scryptP = 1<<14, 8, 1
		case TierMax:
			c.scryptN, c.scryptR, c.scryptP = 1<<16, 8, 1
		default:
			c.scryptN, c.scryptR, c.scryptP = 1<<15, 8, 1
		}
	case Bcrypt:
		switch tier {
		case TierMin:
			c.bcryptCost = 10
		case TierMax:
			c.bcryptCost = 14
		default:
			c.bcryptCost = 12
		}
	}
	return c
}

// --- Options ---

// Option configures [New] behavior.
type Option func(*config)

// WithAlgorithm selects the hashing algorithm. Default: [Argon2id].
func WithAlgorithm(alg Algorithm) Option {
	return func(c *config) {
		base := tierParams(alg, TierDefault)
		*c = base
	}
}

// WithTier selects a pre-configured security level. Applied after
// [WithAlgorithm]. Default: [TierDefault].
func WithTier(tier Tier) Option {
	return func(c *config) {
		base := tierParams(c.algorithm, tier)
		*c = base
	}
}

// WithArgon2Params sets custom Argon2id parameters. Implies [Argon2id].
func WithArgon2Params(memory, iterations uint32, parallelism uint8) Option {
	return func(c *config) {
		c.algorithm = Argon2id
		if memory > 0 {
			c.argonMemory = memory
		}
		if iterations > 0 {
			c.argonIterations = iterations
		}
		if parallelism > 0 {
			c.argonParallelism = parallelism
		}
	}
}

// WithScryptParams sets custom scrypt parameters. Implies [Scrypt].
func WithScryptParams(n, r, p int) Option {
	return func(c *config) {
		c.algorithm = Scrypt
		if n > 1 {
			c.scryptN = n
		}
		if r > 0 {
			c.scryptR = r
		}
		if p > 0 {
			c.scryptP = p
		}
	}
}

// WithBcryptCost sets a custom bcrypt cost. Implies [Bcrypt].
// Valid range: 4–31.
func WithBcryptCost(cost int) Option {
	return func(c *config) {
		c.algorithm = Bcrypt
		if cost >= bcrypt.MinCost && cost <= bcrypt.MaxCost {
			c.bcryptCost = cost
		}
	}
}

// WithPepper sets a server-side secret that is mixed into every password
// before hashing. This protects against offline brute-force even if the
// database (hashes + salts) is fully compromised.
//
// The pepper is applied via HMAC-SHA256(pepper, password), which produces
// a fixed-length output and avoids bcrypt's 72-byte truncation issue.
//
// The same pepper must be used for both [Hasher.Generate] and
// [Hasher.Compare]. Rotating peppers is the caller's responsibility.
func WithPepper(pepper []byte) Option {
	return func(c *config) {
		c.pepper = append([]byte(nil), pepper...)
	}
}

// --- Hasher ---

// Hasher generates and verifies password hashes. Create with [New].
type Hasher struct {
	cfg config
}

// New creates a [Hasher] with the given options. Without options it uses
// [Argon2id] at [TierDefault].
func New(opts ...Option) *Hasher {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	return &Hasher{cfg: cfg}
}

// Generate derives a hash from the password. The operation runs the
// CPU-intensive KDF in a goroutine so it can be cancelled via ctx.
// A nil ctx is treated as [context.Background].
func (h *Hasher) Generate(ctx context.Context, password string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if password == "" {
		return "", errEmptyPassword()
	}
	password = h.applyPepper(password)

	switch h.cfg.algorithm {
	case Argon2id:
		return h.generateArgon2(ctx, password)
	case Scrypt:
		return h.generateScrypt(ctx, password)
	case Bcrypt:
		return h.generateBcrypt(ctx, password)
	default:
		return h.generateArgon2(ctx, password)
	}
}

// Compare verifies that password matches the stored hash. The algorithm
// is auto-detected from the hash format, so a single [Hasher] instance
// can verify hashes produced by any algorithm.
// A nil ctx is treated as [context.Background].
func (h *Hasher) Compare(ctx context.Context, hash, password string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if hash == "" || password == "" {
		return errEmptyPassword()
	}
	password = h.applyPepper(password)

	switch {
	case strings.HasPrefix(hash, "$argon2id$"):
		return h.compareArgon2(ctx, hash, password)
	case strings.HasPrefix(hash, "$2a$") || strings.HasPrefix(hash, "$2b$"):
		return h.compareBcrypt(ctx, hash, password)
	default:
		if parts := strings.Split(hash, ":"); len(parts) == 5 {
			return h.compareScrypt(ctx, hash, password)
		}
		return errInvalidHash(fmt.Errorf("unrecognized hash prefix")) //nolint:forbidigo // internal wrap
	}
}

// NeedsRehash reports whether hash was produced by a different algorithm
// or weaker tier than the one configured in this [Hasher]. Callers
// typically use this after a successful [Compare] to transparently
// upgrade stored hashes on login.
func (h *Hasher) NeedsRehash(hash string) bool {
	switch h.cfg.algorithm {
	case Argon2id:
		if !strings.HasPrefix(hash, "$argon2id$") {
			return true
		}
		var memory, iterations uint32
		var parallelism uint8
		parts := strings.Split(hash, "$")
		if len(parts) != 6 {
			return true
		}
		if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
			return true
		}
		return memory != h.cfg.argonMemory || iterations != h.cfg.argonIterations || parallelism != h.cfg.argonParallelism
	case Scrypt:
		parts := strings.Split(hash, ":")
		if len(parts) != 5 {
			return true
		}
		var n, r, p int
		if _, err := fmt.Sscan(parts[0], &n); err != nil {
			return true
		}
		if _, err := fmt.Sscan(parts[1], &r); err != nil {
			return true
		}
		if _, err := fmt.Sscan(parts[2], &p); err != nil {
			return true
		}
		return n != h.cfg.scryptN || r != h.cfg.scryptR || p != h.cfg.scryptP
	case Bcrypt:
		if !strings.HasPrefix(hash, "$2a$") && !strings.HasPrefix(hash, "$2b$") {
			return true
		}
		cost, err := bcrypt.Cost([]byte(hash))
		if err != nil {
			return true
		}
		return cost != h.cfg.bcryptCost
	default:
		return true
	}
}

// --- Convenience functions ---

// Generate derives an Argon2id hash at [TierDefault]. For most applications
// this is all you need.
func Generate(ctx context.Context, password string) (string, error) {
	return New().Generate(ctx, password)
}

// Compare verifies a password against a hash. The algorithm is auto-detected.
func Compare(ctx context.Context, hash, password string) error {
	return New().Compare(ctx, hash, password)
}

// applyPepper mixes the pepper into the password via HMAC-SHA256.
// Returns the original password unchanged when no pepper is configured.
func (h *Hasher) applyPepper(password string) string {
	if len(h.cfg.pepper) == 0 {
		return password
	}
	mac := hmac.New(sha256.New, h.cfg.pepper)
	mac.Write([]byte(password))
	return base64.RawStdEncoding.EncodeToString(mac.Sum(nil))
}

// --- Internal: Argon2id ---

func (h *Hasher) generateArgon2(ctx context.Context, password string) (string, error) {
	salt := make([]byte, h.cfg.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", errInternal(err)
	}

	type result struct{ key []byte }
	done := make(chan result, 1)
	go func() {
		key := argon2.IDKey([]byte(password), salt, h.cfg.argonIterations, h.cfg.argonMemory, h.cfg.argonParallelism, uint32(h.cfg.keyLen))
		done <- result{key: key}
	}()

	select {
	case <-ctx.Done():
		return "", errCancelled(ctx.Err())
	case res := <-done:
		b64Salt := base64.RawStdEncoding.EncodeToString(salt)
		b64Key := base64.RawStdEncoding.EncodeToString(res.key)
		return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
			argon2.Version, h.cfg.argonMemory, h.cfg.argonIterations, h.cfg.argonParallelism, b64Salt, b64Key), nil
	}
}

func (h *Hasher) compareArgon2(ctx context.Context, hash, password string) error {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 {
		return errInvalidHash(fmt.Errorf("expected 6 parts, got %d", len(parts))) //nolint:forbidigo // internal wrap
	}

	var memory, iterations uint32
	var parallelism uint8
	_, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism)
	if err != nil {
		return errInvalidHash(err)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return errInvalidHash(err)
	}
	storedKey, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return errInvalidHash(err)
	}

	type result struct{ key []byte }
	done := make(chan result, 1)
	go func() {
		key := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(storedKey)))
		done <- result{key: key}
	}()

	select {
	case <-ctx.Done():
		return errCancelled(ctx.Err())
	case res := <-done:
		if subtle.ConstantTimeCompare(storedKey, res.key) != 1 {
			return errMismatch()
		}
		return nil
	}
}

// --- Internal: Scrypt ---

func (h *Hasher) generateScrypt(ctx context.Context, password string) (string, error) {
	salt := make([]byte, h.cfg.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", errInternal(err)
	}

	type result struct {
		key []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		dk, err := scrypt.Key([]byte(password), salt, h.cfg.scryptN, h.cfg.scryptR, h.cfg.scryptP, h.cfg.keyLen)
		done <- result{key: dk, err: err}
	}()

	select {
	case <-ctx.Done():
		return "", errCancelled(ctx.Err())
	case res := <-done:
		if res.err != nil {
			return "", errInternal(res.err)
		}
		return fmt.Sprintf("%d:%d:%d:%s:%s",
			h.cfg.scryptN, h.cfg.scryptR, h.cfg.scryptP,
			base64.RawStdEncoding.EncodeToString(salt),
			base64.RawStdEncoding.EncodeToString(res.key)), nil
	}
}

func (h *Hasher) compareScrypt(ctx context.Context, hash, password string) error {
	parts := strings.Split(hash, ":")
	if len(parts) != 5 {
		return errInvalidHash(fmt.Errorf("expected 5 parts, got %d", len(parts))) //nolint:forbidigo // internal wrap
	}

	var n, r, p int
	_, err := fmt.Sscan(parts[0], &n)
	if err != nil {
		return errInvalidHash(err)
	}
	_, err = fmt.Sscan(parts[1], &r)
	if err != nil {
		return errInvalidHash(err)
	}
	_, err = fmt.Sscan(parts[2], &p)
	if err != nil {
		return errInvalidHash(err)
	}

	const maxScryptN = 1 << 20 // 1 MiB limit on N to prevent DoS via crafted hashes
	if n < 2 || n > maxScryptN || r < 1 || r > 128 || p < 1 || p > 128 {
		return errInvalidHash(fmt.Errorf("scrypt params out of safe range: n=%d r=%d p=%d", n, r, p)) //nolint:forbidigo // internal wrap
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return errInvalidHash(err)
	}
	storedKey, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return errInvalidHash(err)
	}

	type result struct {
		key []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		dk, dkErr := scrypt.Key([]byte(password), salt, n, r, p, len(storedKey))
		done <- result{key: dk, err: dkErr}
	}()

	select {
	case <-ctx.Done():
		return errCancelled(ctx.Err())
	case res := <-done:
		if res.err != nil {
			return errInternal(res.err)
		}
		if subtle.ConstantTimeCompare(storedKey, res.key) != 1 {
			return errMismatch()
		}
		return nil
	}
}

// --- Internal: Bcrypt ---

func (h *Hasher) generateBcrypt(ctx context.Context, password string) (string, error) {
	type result struct {
		hash []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		hsh, err := bcrypt.GenerateFromPassword([]byte(password), h.cfg.bcryptCost)
		done <- result{hash: hsh, err: err}
	}()

	select {
	case <-ctx.Done():
		return "", errCancelled(ctx.Err())
	case res := <-done:
		if res.err != nil {
			return "", errInternal(res.err)
		}
		return string(res.hash), nil
	}
}

func (h *Hasher) compareBcrypt(ctx context.Context, hash, password string) error {
	type result struct{ err error }
	done := make(chan result, 1)
	go func() {
		done <- result{err: bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))}
	}()

	select {
	case <-ctx.Done():
		return errCancelled(ctx.Err())
	case res := <-done:
		if res.err == nil {
			return nil
		}
		if errors.Is(res.err, bcrypt.ErrMismatchedHashAndPassword) {
			return errMismatch()
		}
		return errInvalidHash(res.err)
	}
}
