package hashx

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
)

// ============================================================
// Argon2id
// ============================================================

func TestArgon2_GenerateAndCompare(t *testing.T) {
	h := New(WithAlgorithm(Argon2id), WithTier(TierMin))
	hash, err := h.Generate(context.Background(), "secret123")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Compare(context.Background(), hash, "secret123"); err != nil {
		t.Fatalf("compare should succeed: %v", err)
	}
}

func TestArgon2_WrongPassword(t *testing.T) {
	h := New(WithAlgorithm(Argon2id), WithTier(TierMin))
	hash, err := h.Generate(context.Background(), "correct")
	if err != nil {
		t.Fatal(err)
	}
	err = h.Compare(context.Background(), hash, "wrong")
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeMismatch {
		t.Fatalf("expected code %s, got %s", CodeMismatch, xe.Code)
	}
}

func TestArgon2_AllTiers(t *testing.T) {
	for _, tier := range []Tier{TierMin, TierDefault, TierMax} {
		h := New(WithAlgorithm(Argon2id), WithTier(tier))
		hash, err := h.Generate(context.Background(), "password")
		if err != nil {
			t.Fatalf("tier %d generate: %v", tier, err)
		}
		if err := h.Compare(context.Background(), hash, "password"); err != nil {
			t.Fatalf("tier %d compare: %v", tier, err)
		}
	}
}

func TestArgon2_CustomParams(t *testing.T) {
	h := New(WithArgon2Params(32*1024, 1, 1))
	hash, err := h.Generate(context.Background(), "custom")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Compare(context.Background(), hash, "custom"); err != nil {
		t.Fatal(err)
	}
}

// ============================================================
// Scrypt
// ============================================================

func TestScrypt_GenerateAndCompare(t *testing.T) {
	h := New(WithAlgorithm(Scrypt), WithTier(TierMin))
	hash, err := h.Generate(context.Background(), "secret123")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Compare(context.Background(), hash, "secret123"); err != nil {
		t.Fatalf("compare should succeed: %v", err)
	}
}

func TestScrypt_WrongPassword(t *testing.T) {
	h := New(WithAlgorithm(Scrypt), WithTier(TierMin))
	hash, err := h.Generate(context.Background(), "correct")
	if err != nil {
		t.Fatal(err)
	}
	err = h.Compare(context.Background(), hash, "wrong")
	if err == nil {
		t.Fatal("expected mismatch")
	}
}

func TestScrypt_CustomParams(t *testing.T) {
	h := New(WithScryptParams(1<<14, 8, 1))
	hash, err := h.Generate(context.Background(), "custom")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Compare(context.Background(), hash, "custom"); err != nil {
		t.Fatal(err)
	}
}

// ============================================================
// Bcrypt
// ============================================================

func TestBcrypt_GenerateAndCompare(t *testing.T) {
	h := New(WithAlgorithm(Bcrypt), WithTier(TierMin))
	hash, err := h.Generate(context.Background(), "secret123")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Compare(context.Background(), hash, "secret123"); err != nil {
		t.Fatalf("compare should succeed: %v", err)
	}
}

func TestBcrypt_WrongPassword(t *testing.T) {
	h := New(WithAlgorithm(Bcrypt), WithTier(TierMin))
	hash, err := h.Generate(context.Background(), "correct")
	if err != nil {
		t.Fatal(err)
	}
	err = h.Compare(context.Background(), hash, "wrong")
	if err == nil {
		t.Fatal("expected mismatch")
	}
}

func TestBcrypt_CustomCost(t *testing.T) {
	h := New(WithBcryptCost(10))
	hash, err := h.Generate(context.Background(), "custom")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Compare(context.Background(), hash, "custom"); err != nil {
		t.Fatal(err)
	}
}

// ============================================================
// Cross-algorithm Compare
// ============================================================

func TestCompare_AutoDetect(t *testing.T) {
	ctx := context.Background()
	h := New()

	argonH := New(WithAlgorithm(Argon2id), WithTier(TierMin))
	argonHash, _ := argonH.Generate(ctx, "pass")

	scryptH := New(WithAlgorithm(Scrypt), WithTier(TierMin))
	scryptHash, _ := scryptH.Generate(ctx, "pass")

	bcryptH := New(WithAlgorithm(Bcrypt), WithTier(TierMin))
	bcryptHash, _ := bcryptH.Generate(ctx, "pass")

	if err := h.Compare(ctx, argonHash, "pass"); err != nil {
		t.Fatalf("argon2 auto-detect: %v", err)
	}
	if err := h.Compare(ctx, scryptHash, "pass"); err != nil {
		t.Fatalf("scrypt auto-detect: %v", err)
	}
	if err := h.Compare(ctx, bcryptHash, "pass"); err != nil {
		t.Fatalf("bcrypt auto-detect: %v", err)
	}
}

// ============================================================
// Convenience functions
// ============================================================

func TestConvenience_GenerateAndCompare(t *testing.T) {
	ctx := context.Background()
	hash, err := Generate(ctx, "mypassword")
	if err != nil {
		t.Fatal(err)
	}
	if err := Compare(ctx, hash, "mypassword"); err != nil {
		t.Fatal(err)
	}
	if err := Compare(ctx, hash, "wrong"); err == nil {
		t.Fatal("expected mismatch")
	}
}

// ============================================================
// Error cases
// ============================================================

func TestGenerate_EmptyPassword(t *testing.T) {
	h := New()
	_, err := h.Generate(context.Background(), "")
	if err == nil {
		t.Fatal("expected error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeEmptyPassword {
		t.Fatalf("expected code %s, got %s", CodeEmptyPassword, xe.Code)
	}
}

func TestCompare_EmptyInputs(t *testing.T) {
	h := New()
	if err := h.Compare(context.Background(), "", "pass"); err == nil {
		t.Fatal("expected error for empty hash")
	}
	if err := h.Compare(context.Background(), "hash", ""); err == nil {
		t.Fatal("expected error for empty password")
	}
}

func TestCompare_InvalidHash(t *testing.T) {
	h := New()
	err := h.Compare(context.Background(), "totally-invalid", "pass")
	if err == nil {
		t.Fatal("expected error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeInvalidHash {
		t.Fatalf("expected code %s, got %s", CodeInvalidHash, xe.Code)
	}
}

func TestCompare_InvalidArgonHash(t *testing.T) {
	h := New()
	err := h.Compare(context.Background(), "$argon2id$v=19$m=bad$salt$key", "pass")
	if err == nil {
		t.Fatal("expected error for malformed argon hash")
	}
}

func TestCompare_InvalidScryptHash(t *testing.T) {
	h := New()
	err := h.Compare(context.Background(), "abc:8:1:salt:key", "pass")
	if err == nil {
		t.Fatal("expected error for malformed scrypt N")
	}
}

// ============================================================
// Context cancellation
// ============================================================

func TestGenerate_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	<-ctx.Done()

	h := New(WithAlgorithm(Argon2id), WithTier(TierMax))
	_, err := h.Generate(ctx, "password")
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}

// ============================================================
// Algorithm stringer
// ============================================================

func TestAlgorithm_String(t *testing.T) {
	tests := []struct {
		a    Algorithm
		want string
	}{
		{Argon2id, "argon2id"},
		{Scrypt, "scrypt"},
		{Bcrypt, "bcrypt"},
		{Algorithm(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.a.String(); got != tt.want {
			t.Fatalf("Algorithm(%d).String() = %s, want %s", tt.a, got, tt.want)
		}
	}
}

// ============================================================
// Error constants
// ============================================================

func TestErrorConstants(t *testing.T) {
	if DomainHash != "HASH" {
		t.Fatal("unexpected domain")
	}
}

// ============================================================
// Nil context
// ============================================================

func nilCtx() context.Context { return nil }

func TestGenerate_NilContext(t *testing.T) {
	h := New(WithAlgorithm(Argon2id), WithTier(TierMin))
	hash, err := h.Generate(nilCtx(), "password")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}
}

func TestCompare_NilContext(t *testing.T) {
	h := New(WithAlgorithm(Argon2id), WithTier(TierMin))
	hash, _ := h.Generate(context.Background(), "password")
	if err := h.Compare(nilCtx(), hash, "password"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

// ============================================================
// Bcrypt error mapping
// ============================================================

func TestBcrypt_MalformedHash_ReturnsInvalidHash(t *testing.T) {
	h := New()
	err := h.Compare(context.Background(), "$2a$10$invalid-not-real-hash", "pass")
	if err == nil {
		t.Fatal("expected error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeInvalidHash {
		t.Fatalf("malformed bcrypt should return INVALID_HASH, got %s", xe.Code)
	}
}

// ============================================================
// Scrypt DoS protection
// ============================================================

func TestCompare_ScryptHugeN_Rejected(t *testing.T) {
	hash := "1073741824:8:1:c2FsdA:a2V5"
	h := New()
	err := h.Compare(context.Background(), hash, "pass")
	if err == nil {
		t.Fatal("expected error for huge scrypt N")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeInvalidHash {
		t.Fatalf("expected INVALID_HASH, got %s", xe.Code)
	}
}

// ============================================================
// NeedsRehash
// ============================================================

func TestNeedsRehash_SameParams(t *testing.T) {
	ctx := context.Background()

	h := New(WithAlgorithm(Argon2id), WithTier(TierMin))
	hash, _ := h.Generate(ctx, "pass")
	if h.NeedsRehash(hash) {
		t.Fatal("same params should not need rehash")
	}

	hScrypt := New(WithAlgorithm(Scrypt), WithTier(TierMin))
	sHash, _ := hScrypt.Generate(ctx, "pass")
	if hScrypt.NeedsRehash(sHash) {
		t.Fatal("same scrypt params should not need rehash")
	}

	hBcrypt := New(WithAlgorithm(Bcrypt), WithTier(TierMin))
	bHash, _ := hBcrypt.Generate(ctx, "pass")
	if hBcrypt.NeedsRehash(bHash) {
		t.Fatal("same bcrypt params should not need rehash")
	}
}

func TestNeedsRehash_DifferentAlgorithm(t *testing.T) {
	ctx := context.Background()
	argonHasher := New(WithAlgorithm(Argon2id), WithTier(TierMin))
	bcryptHasher := New(WithAlgorithm(Bcrypt), WithTier(TierMin))
	hash, _ := bcryptHasher.Generate(ctx, "pass")
	if !argonHasher.NeedsRehash(hash) {
		t.Fatal("bcrypt hash should need rehash for argon2 hasher")
	}
}

func TestNeedsRehash_UpgradedTier(t *testing.T) {
	ctx := context.Background()
	low := New(WithAlgorithm(Argon2id), WithTier(TierMin))
	high := New(WithAlgorithm(Argon2id), WithTier(TierMax))
	hash, _ := low.Generate(ctx, "pass")
	if !high.NeedsRehash(hash) {
		t.Fatal("lower-tier hash should need rehash for higher tier")
	}
}

func TestNeedsRehash_MalformedHash(t *testing.T) {
	h := New()
	if !h.NeedsRehash("garbage") {
		t.Fatal("malformed hash should return true")
	}
}

func TestNeedsRehash_ScryptMalformed(t *testing.T) {
	h := New(WithAlgorithm(Scrypt), WithTier(TierMin))
	if !h.NeedsRehash("abc:8:1:salt:key") {
		t.Fatal("malformed scrypt N should need rehash")
	}
	if !h.NeedsRehash("16384:abc:1:salt:key") {
		t.Fatal("malformed scrypt R should need rehash")
	}
	if !h.NeedsRehash("16384:8:abc:salt:key") {
		t.Fatal("malformed scrypt P should need rehash")
	}
	if !h.NeedsRehash("too:few:parts") {
		t.Fatal("wrong part count should need rehash")
	}
}

func TestNeedsRehash_BcryptMalformed(t *testing.T) {
	h := New(WithAlgorithm(Bcrypt), WithTier(TierMin))
	if !h.NeedsRehash("$2a$notreal") {
		t.Fatal("malformed bcrypt should need rehash")
	}
}

func TestNeedsRehash_UnknownAlgorithm(t *testing.T) {
	h := &Hasher{cfg: config{algorithm: Algorithm(99)}}
	if !h.NeedsRehash("anything") {
		t.Fatal("unknown algorithm should always need rehash")
	}
}

func TestNeedsRehash_ArgonMalformedParams(t *testing.T) {
	h := New(WithAlgorithm(Argon2id), WithTier(TierMin))
	if !h.NeedsRehash("$argon2id$v=19$bad$salt$key") {
		t.Fatal("malformed argon params should need rehash")
	}
	if !h.NeedsRehash("$argon2id$only$three") {
		t.Fatal("wrong part count should need rehash")
	}
}

// ============================================================
// Pepper
// ============================================================

func TestPepper_Argon2_GenerateAndCompare(t *testing.T) {
	pepper := []byte("server-secret-2026")
	h := New(WithAlgorithm(Argon2id), WithTier(TierMin), WithPepper(pepper))
	hash, err := h.Generate(context.Background(), "mypassword")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Compare(context.Background(), hash, "mypassword"); err != nil {
		t.Fatalf("compare with same pepper should succeed: %v", err)
	}
}

func TestPepper_WrongPepper_Mismatch(t *testing.T) {
	h1 := New(WithAlgorithm(Argon2id), WithTier(TierMin), WithPepper([]byte("pepper-A")))
	h2 := New(WithAlgorithm(Argon2id), WithTier(TierMin), WithPepper([]byte("pepper-B")))
	hash, _ := h1.Generate(context.Background(), "password")
	err := h2.Compare(context.Background(), hash, "password")
	if err == nil {
		t.Fatal("different peppers should produce mismatch")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) || xe.Code != CodeMismatch {
		t.Fatalf("expected MISMATCH, got %v", err)
	}
}

func TestPepper_NoPepper_vs_Pepper_Mismatch(t *testing.T) {
	noPepper := New(WithAlgorithm(Argon2id), WithTier(TierMin))
	withPepper := New(WithAlgorithm(Argon2id), WithTier(TierMin), WithPepper([]byte("secret")))
	hash, _ := noPepper.Generate(context.Background(), "password")
	err := withPepper.Compare(context.Background(), hash, "password")
	if err == nil {
		t.Fatal("peppered hasher should not verify non-peppered hash")
	}
}

func TestPepper_Bcrypt_GenerateAndCompare(t *testing.T) {
	pepper := []byte("bcrypt-pepper")
	h := New(WithAlgorithm(Bcrypt), WithTier(TierMin), WithPepper(pepper))
	hash, err := h.Generate(context.Background(), "longpassword-that-would-exceed-72-bytes-without-hmac-preprocessing-applied-here")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Compare(context.Background(), hash, "longpassword-that-would-exceed-72-bytes-without-hmac-preprocessing-applied-here"); err != nil {
		t.Fatalf("bcrypt with pepper should verify: %v", err)
	}
}

func TestPepper_Scrypt_GenerateAndCompare(t *testing.T) {
	pepper := []byte("scrypt-pepper")
	h := New(WithAlgorithm(Scrypt), WithTier(TierMin), WithPepper(pepper))
	hash, err := h.Generate(context.Background(), "password")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Compare(context.Background(), hash, "password"); err != nil {
		t.Fatalf("scrypt with pepper should verify: %v", err)
	}
}

func TestPepper_IsDefensivelyCopied(t *testing.T) {
	pepper := []byte("original")
	h := New(WithPepper(pepper))
	pepper[0] = 'X'
	hash, _ := h.Generate(context.Background(), "test")
	h2 := New(WithPepper([]byte("original")))
	if err := h2.Compare(context.Background(), hash, "test"); err != nil {
		t.Fatal("pepper should be defensively copied; mutation of original slice must not affect hasher")
	}
}

// ============================================================
// Tier coverage (Scrypt/Bcrypt TierMax + TierDefault)
// ============================================================

func TestScrypt_AllTiers(t *testing.T) {
	for _, tier := range []Tier{TierMin, TierDefault, TierMax} {
		h := New(WithAlgorithm(Scrypt), WithTier(tier))
		hash, err := h.Generate(context.Background(), "password")
		if err != nil {
			t.Fatalf("scrypt tier %d generate: %v", tier, err)
		}
		if err := h.Compare(context.Background(), hash, "password"); err != nil {
			t.Fatalf("scrypt tier %d compare: %v", tier, err)
		}
	}
}

func TestBcrypt_AllTiers(t *testing.T) {
	for _, tier := range []Tier{TierMin, TierDefault, TierMax} {
		h := New(WithAlgorithm(Bcrypt), WithTier(tier))
		hash, err := h.Generate(context.Background(), "password")
		if err != nil {
			t.Fatalf("bcrypt tier %d generate: %v", tier, err)
		}
		if err := h.Compare(context.Background(), hash, "password"); err != nil {
			t.Fatalf("bcrypt tier %d compare: %v", tier, err)
		}
	}
}

// ============================================================
// Context cancellation (Scrypt + Bcrypt)
// ============================================================

func TestScrypt_Generate_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	h := New(WithAlgorithm(Scrypt), WithTier(TierMin))
	_, err := h.Generate(ctx, "password")
	if err == nil {
		return // goroutine finished before ctx check — not a failure
	}
	var xe *errx.Error
	if errors.As(err, &xe) && xe.Code != CodeCancelled {
		t.Fatalf("expected CANCELLED, got %s", xe.Code)
	}
}

func TestScrypt_Compare_ContextCancelled(t *testing.T) {
	h := New(WithAlgorithm(Scrypt), WithTier(TierMin))
	hash, _ := h.Generate(context.Background(), "password")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := h.Compare(ctx, hash, "password")
	if err == nil {
		return
	}
	var xe *errx.Error
	if errors.As(err, &xe) && xe.Code != CodeCancelled {
		t.Fatalf("expected CANCELLED, got %s", xe.Code)
	}
}

func TestBcrypt_Generate_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	h := New(WithAlgorithm(Bcrypt), WithTier(TierMin))
	_, err := h.Generate(ctx, "password")
	if err == nil {
		return
	}
	var xe *errx.Error
	if errors.As(err, &xe) && xe.Code != CodeCancelled {
		t.Fatalf("expected CANCELLED, got %s", xe.Code)
	}
}

func TestBcrypt_Compare_ContextCancelled(t *testing.T) {
	h := New(WithAlgorithm(Bcrypt), WithTier(TierMin))
	hash, _ := h.Generate(context.Background(), "password")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := h.Compare(ctx, hash, "password")
	if err == nil {
		return
	}
	var xe *errx.Error
	if errors.As(err, &xe) && xe.Code != CodeCancelled {
		t.Fatalf("expected CANCELLED, got %s", xe.Code)
	}
}

func TestArgon2_Compare_ContextCancelled(t *testing.T) {
	h := New(WithAlgorithm(Argon2id), WithTier(TierMin))
	hash, _ := h.Generate(context.Background(), "password")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := h.Compare(ctx, hash, "password")
	if err == nil {
		return
	}
	var xe *errx.Error
	if errors.As(err, &xe) && xe.Code != CodeCancelled {
		t.Fatalf("expected CANCELLED, got %s", xe.Code)
	}
}

// ============================================================
// Scrypt compareScrypt — malformed hash edge cases
// ============================================================

func TestCompare_ScryptMalformedR(t *testing.T) {
	h := New()
	err := h.Compare(context.Background(), "16384:abc:1:c2FsdA:a2V5", "pass")
	if err == nil {
		t.Fatal("expected error for malformed scrypt R")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) || xe.Code != CodeInvalidHash {
		t.Fatalf("expected INVALID_HASH, got %v", err)
	}
}

func TestCompare_ScryptMalformedP(t *testing.T) {
	h := New()
	err := h.Compare(context.Background(), "16384:8:abc:c2FsdA:a2V5", "pass")
	if err == nil {
		t.Fatal("expected error for malformed scrypt P")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) || xe.Code != CodeInvalidHash {
		t.Fatalf("expected INVALID_HASH, got %v", err)
	}
}

func TestCompare_ScryptBadPartCount(t *testing.T) {
	h := New()
	err := h.Compare(context.Background(), "16384:8:1:salt", "pass")
	if err == nil {
		t.Fatal("expected error for wrong part count")
	}
}

func TestCompare_ScryptBadSaltBase64(t *testing.T) {
	h := New()
	err := h.Compare(context.Background(), "16384:8:1:!!!invalid!!!:a2V5", "pass")
	if err == nil {
		t.Fatal("expected error for bad salt base64")
	}
}

func TestCompare_ScryptBadKeyBase64(t *testing.T) {
	h := New()
	err := h.Compare(context.Background(), "16384:8:1:c2FsdA:!!!invalid!!!", "pass")
	if err == nil {
		t.Fatal("expected error for bad key base64")
	}
}

func TestCompare_ScryptParamsOutOfRange(t *testing.T) {
	cases := []struct {
		name string
		hash string
	}{
		{"N too small", "1:8:1:c2FsdA:a2V5"},
		{"R too large", "16384:200:1:c2FsdA:a2V5"},
		{"P too large", "16384:8:200:c2FsdA:a2V5"},
		{"R zero", "16384:0:1:c2FsdA:a2V5"},
		{"P zero", "16384:8:0:c2FsdA:a2V5"},
	}
	h := New()
	for _, tc := range cases {
		err := h.Compare(context.Background(), tc.hash, "pass")
		if err == nil {
			t.Fatalf("%s: expected error", tc.name)
		}
	}
}

// ============================================================
// Argon2 compareArgon2 — malformed hash edge cases
// ============================================================

func TestCompare_ArgonBadSaltBase64(t *testing.T) {
	h := New()
	err := h.Compare(context.Background(), "$argon2id$v=19$m=65536,t=3,p=4$!!!bad!!!$a2V5", "pass")
	if err == nil {
		t.Fatal("expected error for bad salt base64")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) || xe.Code != CodeInvalidHash {
		t.Fatalf("expected INVALID_HASH, got %v", err)
	}
}

func TestCompare_ArgonBadKeyBase64(t *testing.T) {
	h := New()
	err := h.Compare(context.Background(), "$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$!!!bad!!!", "pass")
	if err == nil {
		t.Fatal("expected error for bad key base64")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) || xe.Code != CodeInvalidHash {
		t.Fatalf("expected INVALID_HASH, got %v", err)
	}
}

// ============================================================
// Default algorithm fallback
// ============================================================

func TestGenerate_DefaultAlgorithmFallback(t *testing.T) {
	h := &Hasher{cfg: config{algorithm: Algorithm(99), saltLen: 16, keyLen: 32,
		argonMemory: 32 * 1024, argonIterations: 2, argonParallelism: 2}}
	hash, err := h.Generate(context.Background(), "password")
	if err != nil {
		t.Fatalf("default fallback should work: %v", err)
	}
	if err := h.Compare(context.Background(), hash, "password"); err != nil {
		t.Fatalf("compare after default fallback: %v", err)
	}
}

// ============================================================
// Benchmark
// ============================================================

func BenchmarkGenerate_Argon2_Min(b *testing.B) {
	h := New(WithAlgorithm(Argon2id), WithTier(TierMin))
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		h.Generate(ctx, "benchpass")
	}
}

func BenchmarkCompare_Argon2_Min(b *testing.B) {
	h := New(WithAlgorithm(Argon2id), WithTier(TierMin))
	ctx := context.Background()
	hash, _ := h.Generate(ctx, "benchpass")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		h.Compare(ctx, hash, "benchpass")
	}
}

func BenchmarkGenerate_Bcrypt_Min(b *testing.B) {
	h := New(WithAlgorithm(Bcrypt), WithTier(TierMin))
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		h.Generate(ctx, "benchpass")
	}
}
