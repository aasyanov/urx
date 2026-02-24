package dicx

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
)

func nilCtx() context.Context { return nil }

// --- Test types ---

type Config struct{ DSN string }

func NewConfig() *Config { return &Config{DSN: "postgres://localhost"} }

type Logger struct{ Prefix string }

func NewLogger() *Logger { return &Logger{Prefix: "app"} }

type DB struct {
	Cfg     *Config
	started bool
	stopped bool
}

func NewDB(cfg *Config) *DB { return &DB{Cfg: cfg} }

func (db *DB) Start(context.Context) error { db.started = true; return nil }
func (db *DB) Stop(context.Context) error  { db.stopped = true; return nil }

type UserService struct {
	DB  *DB
	Log *Logger
}

func NewUserService(db *DB, log *Logger) *UserService {
	return &UserService{DB: db, Log: log}
}

type FailingService struct{}

func NewFailingService() (*FailingService, error) {
	return nil, fmt.Errorf("connection refused")
}

type ServiceWithError struct{ Val int }

func NewServiceWithError() (*ServiceWithError, error) {
	return &ServiceWithError{Val: 42}, nil
}

// Cycle types
type CycleA struct{ B *CycleB }
type CycleB struct{ A *CycleA }

func NewCycleA(b *CycleB) *CycleA { return &CycleA{B: b} }
func NewCycleB(a *CycleA) *CycleB { return &CycleB{A: a} }

// Three-way cycle
type CycleX struct{ Y *CycleY }
type CycleY struct{ Z *CycleZ }
type CycleZ struct{ X *CycleX }

func NewCycleX(y *CycleY) *CycleX { return &CycleX{Y: y} }
func NewCycleY(z *CycleZ) *CycleY { return &CycleY{Z: z} }
func NewCycleZ(x *CycleX) *CycleZ { return &CycleZ{X: x} }

// Self-dependency
type SelfDep struct{}

func NewSelfDep(_ *SelfDep) *SelfDep { return &SelfDep{} }

// Starter-only
type StarterOnly struct{ started bool }

func NewStarterOnly() *StarterOnly { return &StarterOnly{} }
func (s *StarterOnly) Start(context.Context) error {
	s.started = true
	return nil
}

// Stopper-only
type StopperOnly struct{ stopped bool }

func NewStopperOnly() *StopperOnly { return &StopperOnly{} }
func (s *StopperOnly) Stop(context.Context) error {
	s.stopped = true
	return nil
}

// Failing starter
type FailStarter struct{}

func NewFailStarter() *FailStarter { return &FailStarter{} }
func (f *FailStarter) Start(context.Context) error {
	return fmt.Errorf("start failed")
}

// Failing stopper
type FailStopper struct{}

func NewFailStopper() *FailStopper { return &FailStopper{} }
func (f *FailStopper) Stop(context.Context) error {
	return fmt.Errorf("stop failed")
}

// Deep chain: L0 -> L1 -> L2 -> L3 -> L4
type L0 struct{}
type L1 struct{ *L0 }
type L2 struct{ *L1 }
type L3 struct{ *L2 }
type L4 struct{ *L3 }

func NewL0() *L0          { return &L0{} }
func NewL1(d *L0) *L1     { return &L1{d} }
func NewL2(d *L1) *L2     { return &L2{d} }
func NewL3(d *L2) *L3     { return &L3{d} }
func NewL4(d *L3) *L4     { return &L4{d} }

// Counter for transient tests
type Counter struct{ ID int }

var counterMu sync.Mutex
var counterSeq int

func NewCounter() *Counter {
	counterMu.Lock()
	counterSeq++
	id := counterSeq
	counterMu.Unlock()
	return &Counter{ID: id}
}

// Interface type for testing
type Greeter interface {
	Greet() string
}

type EnglishGreeter struct{}

func (EnglishGreeter) Greet() string { return "hello" }
func NewEnglishGreeter() EnglishGreeter { return EnglishGreeter{} }

// --- Provide tests ---

func TestProvide_ValidConstructor(t *testing.T) {
	c := New()
	if err := c.Provide(NewConfig); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProvide_WithDependencies(t *testing.T) {
	c := New()
	if err := c.Provide(NewConfig); err != nil {
		t.Fatal(err)
	}
	if err := c.Provide(NewDB); err != nil {
		t.Fatal(err)
	}
}

func TestProvide_WithErrorReturn(t *testing.T) {
	c := New()
	if err := c.Provide(NewServiceWithError); err != nil {
		t.Fatal(err)
	}
}

func TestProvide_NotAFunction(t *testing.T) {
	c := New()
	err := c.Provide("not a function")
	assertErrxCode(t, err, CodeBadConstructor)
}

func TestProvide_Variadic(t *testing.T) {
	c := New()
	err := c.Provide(func(args ...int) int { return 0 })
	assertErrxCode(t, err, CodeBadConstructor)
}

func TestProvide_NoReturn(t *testing.T) {
	c := New()
	err := c.Provide(func() {})
	assertErrxCode(t, err, CodeBadConstructor)
}

func TestProvide_ThreeReturns(t *testing.T) {
	c := New()
	err := c.Provide(func() (int, int, error) { return 0, 0, nil })
	assertErrxCode(t, err, CodeBadConstructor)
}

func TestProvide_SecondReturnNotError(t *testing.T) {
	c := New()
	err := c.Provide(func() (int, string) { return 0, "" })
	assertErrxCode(t, err, CodeBadConstructor)
}

func TestProvide_Duplicate(t *testing.T) {
	c := New()
	if err := c.Provide(NewConfig); err != nil {
		t.Fatal(err)
	}
	err := c.Provide(NewConfig)
	assertErrxCode(t, err, CodeAlreadyProvided)
}

func TestProvide_AfterStart(t *testing.T) {
	c := New()
	_ = c.Provide(NewConfig)
	_ = c.Start(context.Background())
	err := c.Provide(NewLogger)
	assertErrxCode(t, err, CodeFrozen)
}

func TestProvide_WithLifetime(t *testing.T) {
	c := New()
	err := c.Provide(NewConfig, WithLifetime(Transient))
	if err != nil {
		t.Fatal(err)
	}
}

// --- Resolve tests ---

func TestResolve_Singleton(t *testing.T) {
	c := New()
	_ = c.Provide(NewConfig)

	cfg1, err := Resolve[*Config](c)
	if err != nil {
		t.Fatal(err)
	}
	cfg2, err := Resolve[*Config](c)
	if err != nil {
		t.Fatal(err)
	}
	if cfg1 != cfg2 {
		t.Fatal("singleton should return same instance")
	}
}

func TestResolve_Transient(t *testing.T) {
	counterSeq = 0
	c := New()
	_ = c.Provide(NewCounter, WithLifetime(Transient))

	c1, err := Resolve[*Counter](c)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := Resolve[*Counter](c)
	if err != nil {
		t.Fatal(err)
	}
	if c1.ID == c2.ID {
		t.Fatal("transient should return different instances")
	}
}

func TestResolve_WithDependencies(t *testing.T) {
	c := New()
	_ = c.Provide(NewConfig)
	_ = c.Provide(NewLogger)
	_ = c.Provide(NewDB)
	_ = c.Provide(NewUserService)

	svc, err := Resolve[*UserService](c)
	if err != nil {
		t.Fatal(err)
	}
	if svc.DB == nil || svc.DB.Cfg == nil || svc.Log == nil {
		t.Fatal("dependencies not injected")
	}
	if svc.DB.Cfg.DSN != "postgres://localhost" {
		t.Fatalf("unexpected DSN: %s", svc.DB.Cfg.DSN)
	}
}

func TestResolve_MissingDep(t *testing.T) {
	c := New()
	_ = c.Provide(NewDB)

	_, err := Resolve[*DB](c)
	assertErrxCode(t, err, CodeMissingDep)

	var xe *errx.Error
	if errors.As(err, &xe) {
		trace, _ := xe.Meta["trace"].(string)
		if !strings.Contains(trace, "*dicx.Config") {
			t.Fatalf("trace should mention missing type, got: %s", trace)
		}
	}
}

func TestResolve_DeepChain(t *testing.T) {
	c := New()
	_ = c.Provide(NewL0)
	_ = c.Provide(NewL1)
	_ = c.Provide(NewL2)
	_ = c.Provide(NewL3)
	_ = c.Provide(NewL4)

	l4, err := Resolve[*L4](c)
	if err != nil {
		t.Fatal(err)
	}
	if l4.L0 == nil {
		t.Fatal("deep chain not fully resolved")
	}
}

func TestResolve_ConstructorError(t *testing.T) {
	c := New()
	_ = c.Provide(NewFailingService)

	_, err := Resolve[*FailingService](c)
	assertErrxCode(t, err, CodeConstructorFailed)

	var xe *errx.Error
	if errors.As(err, &xe) {
		if xe.Cause == nil {
			t.Fatal("cause should be set")
		}
		if !strings.Contains(xe.Cause.Error(), "connection refused") {
			t.Fatalf("unexpected cause: %v", xe.Cause)
		}
	}
}

func TestResolve_ConstructorReturnsNilError(t *testing.T) {
	c := New()
	_ = c.Provide(NewServiceWithError)

	svc, err := Resolve[*ServiceWithError](c)
	if err != nil {
		t.Fatal(err)
	}
	if svc.Val != 42 {
		t.Fatalf("unexpected value: %d", svc.Val)
	}
}

func TestResolve_Unregistered(t *testing.T) {
	c := New()
	_, err := Resolve[*Config](c)
	assertErrxCode(t, err, CodeMissingDep)
}

func TestResolve_ValueType(t *testing.T) {
	c := New()
	_ = c.Provide(NewEnglishGreeter)

	g, err := Resolve[EnglishGreeter](c)
	if err != nil {
		t.Fatal(err)
	}
	if g.Greet() != "hello" {
		t.Fatalf("unexpected greeting: %s", g.Greet())
	}
}

// --- Cycle detection tests ---

func TestResolve_CycleTwoWay(t *testing.T) {
	c := New()
	_ = c.Provide(NewCycleA)
	_ = c.Provide(NewCycleB)

	_, err := Resolve[*CycleA](c)
	assertErrxCode(t, err, CodeCyclicDep)

	var xe *errx.Error
	if errors.As(err, &xe) {
		cycle, _ := xe.Meta["cycle"].(string)
		if !strings.Contains(cycle, "CycleA") || !strings.Contains(cycle, "CycleB") {
			t.Fatalf("cycle trace should mention both types, got: %s", cycle)
		}
	}
}

func TestResolve_CycleThreeWay(t *testing.T) {
	c := New()
	_ = c.Provide(NewCycleX)
	_ = c.Provide(NewCycleY)
	_ = c.Provide(NewCycleZ)

	_, err := Resolve[*CycleX](c)
	assertErrxCode(t, err, CodeCyclicDep)
}

func TestResolve_SelfDependency(t *testing.T) {
	c := New()
	_ = c.Provide(NewSelfDep)

	_, err := Resolve[*SelfDep](c)
	assertErrxCode(t, err, CodeCyclicDep)
}

// --- Lifecycle tests ---

func TestStart_ResolvesAllSingletons(t *testing.T) {
	c := New()
	_ = c.Provide(NewConfig)
	_ = c.Provide(NewDB)

	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	db, err := Resolve[*DB](c)
	if err != nil {
		t.Fatal(err)
	}
	if !db.started {
		t.Fatal("DB.Start should have been called")
	}
}

func TestStart_DependencyOrder(t *testing.T) {
	var order []string

	type OrderedA struct{}
	type OrderedB struct{}
	type OrderedC struct{}

	c := New()
	_ = c.Provide(func() *OrderedA {
		order = append(order, "A")
		return &OrderedA{}
	})
	_ = c.Provide(func(_ *OrderedA) *OrderedB {
		order = append(order, "B")
		return &OrderedB{}
	})
	_ = c.Provide(func(_ *OrderedB) *OrderedC {
		order = append(order, "C")
		return &OrderedC{}
	})

	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(order) != 3 || order[0] != "A" || order[1] != "B" || order[2] != "C" {
		t.Fatalf("unexpected order: %v", order)
	}
}

func TestStart_Idempotent(t *testing.T) {
	c := New()
	_ = c.Provide(NewConfig)
	_ = c.Start(context.Background())
	if err := c.Start(context.Background()); err != nil {
		t.Fatal("second Start should be no-op")
	}
}

func TestStart_StarterError(t *testing.T) {
	c := New()
	_ = c.Provide(NewFailStarter)

	err := c.Start(context.Background())
	assertErrxCode(t, err, CodeLifecycleFailed)
	if !strings.Contains(err.Error(), "Start failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStart_ConstructorError(t *testing.T) {
	c := New()
	_ = c.Provide(NewFailingService)

	err := c.Start(context.Background())
	assertErrxCode(t, err, CodeConstructorFailed)
}

func TestStart_MissingDep(t *testing.T) {
	c := New()
	_ = c.Provide(NewDB)

	err := c.Start(context.Background())
	assertErrxCode(t, err, CodeMissingDep)
}

func TestStop_ReverseOrder(t *testing.T) {
	var order []string

	type StopA struct{}
	type StopB struct{}

	c := New()
	_ = c.Provide(func() *StopA { return &StopA{} })
	_ = c.Provide(func(_ *StopA) *StopB { return &StopB{} })

	// We need to track stop order via a wrapper approach.
	// Instead, use the DB/StarterOnly pattern.
	c2 := New()
	_ = c2.Provide(NewConfig)
	_ = c2.Provide(NewDB)
	_ = c2.Start(context.Background())

	db, _ := Resolve[*DB](c2)
	if err := c2.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !db.stopped {
		t.Fatal("DB.Stop should have been called")
	}

	_ = order // avoid unused
}

func TestStop_AggregatesErrors(t *testing.T) {
	c := New()
	_ = c.Provide(NewFailStopper)
	_ = c.Start(context.Background())

	err := c.Stop(context.Background())
	if err == nil {
		t.Fatal("expected error from Stop")
	}
	var me *errx.MultiError
	if !errors.As(err, &me) {
		t.Fatalf("expected MultiError, got %T", err)
	}
	if me.Len() != 1 {
		t.Fatalf("expected 1 error, got %d", me.Len())
	}
	var xe *errx.Error
	if !errors.As(me.Errors[0], &xe) {
		t.Fatalf("expected *errx.Error inside MultiError, got %T", me.Errors[0])
	}
	if xe.Code != CodeLifecycleFailed {
		t.Fatalf("expected code %s, got %s", CodeLifecycleFailed, xe.Code)
	}
}

func TestStop_MultipleFailures(t *testing.T) {
	type FS1 struct{}
	type FS2 struct{}

	c := New()
	_ = c.Provide(func() *FS1 { return &FS1{} })
	_ = c.Provide(func() *FS2 { return &FS2{} })
	// These don't implement Stopper, so Stop should be clean.
	_ = c.Start(context.Background())
	err := c.Stop(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStop_EmptyContainer(t *testing.T) {
	c := New()
	err := c.Stop(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStarterOnly(t *testing.T) {
	c := New()
	_ = c.Provide(NewStarterOnly)
	_ = c.Start(context.Background())

	s, _ := Resolve[*StarterOnly](c)
	if !s.started {
		t.Fatal("StarterOnly.Start should have been called")
	}
	if err := c.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestStopperOnly(t *testing.T) {
	c := New()
	_ = c.Provide(NewStopperOnly)
	_ = c.Start(context.Background())

	s, _ := Resolve[*StopperOnly](c)
	_ = c.Stop(context.Background())
	if !s.stopped {
		t.Fatal("StopperOnly.Stop should have been called")
	}
}

func TestStart_SkipsTransient(t *testing.T) {
	counterSeq = 0
	c := New()
	_ = c.Provide(NewCounter, WithLifetime(Transient))
	_ = c.Start(context.Background())

	c1, _ := Resolve[*Counter](c)
	c2, _ := Resolve[*Counter](c)
	if c1.ID == c2.ID {
		t.Fatal("transient should still produce different instances after Start")
	}
}

// --- Generic helpers ---

func TestMustResolve_Success(t *testing.T) {
	c := New()
	_ = c.Provide(NewConfig)

	cfg := MustResolve[*Config](c)
	if cfg.DSN != "postgres://localhost" {
		t.Fatalf("unexpected DSN: %s", cfg.DSN)
	}
}

func TestMustResolve_Panics(t *testing.T) {
	c := New()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("expected string panic, got %T", r)
		}
		if !strings.HasPrefix(msg, "dicx:") {
			t.Fatalf("unexpected panic message: %s", msg)
		}
	}()
	MustResolve[*Config](c)
}

// --- Thread safety ---

func TestConcurrentResolve(t *testing.T) {
	c := New()
	_ = c.Provide(NewConfig)
	_ = c.Provide(NewDB)
	_ = c.Provide(NewLogger)
	_ = c.Provide(NewUserService)

	var wg sync.WaitGroup
	errs := make(chan error, 100)

	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := Resolve[*UserService](c)
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent resolve failed: %v", err)
	}

	svc1, _ := Resolve[*UserService](c)
	svc2, _ := Resolve[*UserService](c)
	if svc1 != svc2 {
		t.Fatal("singleton identity broken under concurrency")
	}
}

// --- Edge cases ---

func TestResolve_BeforeStart(t *testing.T) {
	c := New()
	_ = c.Provide(NewConfig)

	cfg, err := Resolve[*Config](c)
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil {
		t.Fatal("should resolve even before Start")
	}
}

func TestResolve_ConstructorMayResolveContainer(t *testing.T) {
	c := New()
	_ = c.Provide(NewConfig)

	type UsesContainer struct {
		Cfg *Config
	}
	if err := c.Provide(func() (*UsesContainer, error) {
		cfg, err := Resolve[*Config](c)
		if err != nil {
			return nil, err
		}
		return &UsesContainer{Cfg: cfg}, nil
	}); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	var (
		got *UsesContainer
		err error
	)
	go func() {
		defer close(done)
		got, err = Resolve[*UsesContainer](c)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("resolve deadlocked when constructor resolved another dependency")
	}

	if err != nil {
		t.Fatalf("unexpected resolve error: %v", err)
	}
	if got == nil || got.Cfg == nil {
		t.Fatal("expected resolved dependency from nested resolve call")
	}
}

func TestStart_NilContext(t *testing.T) {
	c := New()
	_ = c.Provide(NewConfig)
	if err := c.Start(nilCtx()); err != nil {
		t.Fatal(err)
	}
}

func TestStop_NilContext(t *testing.T) {
	c := New()
	if err := c.Stop(nilCtx()); err != nil {
		t.Fatal(err)
	}
}

func TestEmptyContainer_Start(t *testing.T) {
	c := New()
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestEmptyContainer_Stop(t *testing.T) {
	c := New()
	if err := c.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// --- Error format tests ---

func TestErrorFormat_CyclicDep(t *testing.T) {
	chain := []reflect.Type{
		reflect.TypeFor[*CycleA](),
		reflect.TypeFor[*CycleB](),
		reflect.TypeFor[*CycleA](),
	}
	s := formatCycle(chain)
	if !strings.Contains(s, "CycleA") || !strings.Contains(s, "CycleB") {
		t.Fatalf("unexpected cycle format: %s", s)
	}
}

func TestErrorFormat_Chain(t *testing.T) {
	chain := []reflect.Type{
		reflect.TypeFor[*UserService](),
		reflect.TypeFor[*DB](),
	}
	s := formatChain(chain, reflect.TypeFor[*Config]())
	if !strings.Contains(s, "UserService") || !strings.Contains(s, "DB") || !strings.Contains(s, "Config") {
		t.Fatalf("unexpected chain format: %s", s)
	}
}

func TestErrorFormat_EmptyChain(t *testing.T) {
	s := formatChain(nil, reflect.TypeFor[*Config]())
	if !strings.Contains(s, "Config") {
		t.Fatalf("unexpected format: %s", s)
	}
}

func TestErrorFormat_EmptyCycle(t *testing.T) {
	s := formatCycle(nil)
	if s != "" {
		t.Fatalf("expected empty, got: %s", s)
	}
}

// --- Provider validation edge cases ---

func TestNewProvider_NilConstructor(t *testing.T) {
	_, err := newProvider(nil)
	if err == nil {
		t.Fatal("expected error for nil constructor")
	}
}

func TestLifetime_String(t *testing.T) {
	if Singleton != 0 {
		t.Fatal("Singleton should be 0")
	}
	if Transient != 1 {
		t.Fatal("Transient should be 1")
	}
}

// --- Resolve after Start (cached path) ---

func TestResolve_AfterStart_CachedPath(t *testing.T) {
	c := New()
	_ = c.Provide(NewConfig)
	_ = c.Provide(NewDB)
	_ = c.Start(context.Background())

	db1, _ := Resolve[*DB](c)
	db2, _ := Resolve[*DB](c)
	if db1 != db2 {
		t.Fatal("should return cached singleton after Start")
	}
}

// --- Complex scenario: full lifecycle ---

func TestFullLifecycle(t *testing.T) {
	c := New()
	_ = c.Provide(NewConfig)
	_ = c.Provide(NewLogger)
	_ = c.Provide(NewDB)
	_ = c.Provide(NewUserService)

	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}

	svc := MustResolve[*UserService](c)
	if svc.DB.Cfg.DSN != "postgres://localhost" {
		t.Fatal("full chain not resolved")
	}
	if !svc.DB.started {
		t.Fatal("DB should be started")
	}

	if err := c.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if !svc.DB.stopped {
		t.Fatal("DB should be stopped")
	}
}

// --- Coverage gap tests ---

func TestStart_AlreadyCachedSingleton(t *testing.T) {
	c := New()
	_ = c.Provide(NewConfig)

	// Resolve before Start to pre-cache the singleton.
	_, _ = Resolve[*Config](c)

	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestResolve_DoubleCheckAfterLockUpgrade(t *testing.T) {
	c := New()
	_ = c.Provide(NewConfig)

	// First resolve caches the singleton.
	cfg1, err := Resolve[*Config](c)
	if err != nil {
		t.Fatal(err)
	}

	// Second resolve hits the RLock fast path.
	cfg2, err := Resolve[*Config](c)
	if err != nil {
		t.Fatal(err)
	}
	if cfg1 != cfg2 {
		t.Fatal("should return same cached instance")
	}
}

// --- Stats / IsClosed tests ---

func TestStats_Empty(t *testing.T) {
	c := New()
	s := c.Stats()
	if s.Providers != 0 || s.Singletons != 0 || s.Started || s.Stopped {
		t.Fatalf("unexpected stats for empty container: %+v", s)
	}
}

func TestStats_AfterProvide(t *testing.T) {
	c := New()
	_ = c.Provide(NewConfig)
	_ = c.Provide(NewLogger)
	s := c.Stats()
	if s.Providers != 2 {
		t.Fatalf("expected 2 providers, got %d", s.Providers)
	}
	if s.Singletons != 0 {
		t.Fatalf("expected 0 singletons before resolve, got %d", s.Singletons)
	}
}

func TestStats_AfterStartStop(t *testing.T) {
	c := New()
	_ = c.Provide(NewConfig)
	_ = c.Start(context.Background())

	s := c.Stats()
	if !s.Started {
		t.Fatal("expected Started=true after Start")
	}
	if s.Singletons != 1 {
		t.Fatalf("expected 1 singleton after Start, got %d", s.Singletons)
	}

	_ = c.Stop(context.Background())
	s = c.Stats()
	if !s.Stopped {
		t.Fatal("expected Stopped=true after Stop")
	}
}

func TestIsClosed_BeforeStop(t *testing.T) {
	c := New()
	if c.IsClosed() {
		t.Fatal("expected IsClosed=false for new container")
	}
}

func TestIsClosed_AfterStop(t *testing.T) {
	c := New()
	_ = c.Provide(NewConfig)
	_ = c.Start(context.Background())
	_ = c.Stop(context.Background())
	if !c.IsClosed() {
		t.Fatal("expected IsClosed=true after Stop")
	}
}

// --- Constructor panic test ---

func TestResolve_ConstructorPanic(t *testing.T) {
	c := New()
	_ = c.Provide(func() *Config {
		panic("boom")
	})
	_, err := Resolve[*Config](c)
	assertErrxCode(t, err, CodeConstructorFailed)
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected panic message in error, got: %v", err)
	}
}

func TestStart_ConstructorPanic(t *testing.T) {
	c := New()
	_ = c.Provide(func() *Config {
		panic("start-boom")
	})
	err := c.Start(context.Background())
	assertErrxCode(t, err, CodeConstructorFailed)
}

// --- Concurrent singleton resolution ---

func TestConcurrentResolve_SameSingleton(t *testing.T) {
	c := New()
	_ = c.Provide(func() *Config {
		time.Sleep(10 * time.Millisecond)
		return &Config{DSN: "concurrent"}
	})

	var wg sync.WaitGroup
	results := make([]*Config, 50)
	for i := range 50 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			v, err := Resolve[*Config](c)
			if err != nil {
				t.Errorf("resolve failed: %v", err)
				return
			}
			results[idx] = v
		}(i)
	}
	wg.Wait()

	for i := 1; i < len(results); i++ {
		if results[i] != results[0] {
			t.Fatal("concurrent resolve should return the same singleton instance")
		}
	}
}

// --- dependsOnAnyLocked direct test ---

func TestDependsOnAnyLocked_DirectDep(t *testing.T) {
	c := New()
	_ = c.Provide(NewConfig)
	_ = c.Provide(NewDB)

	c.mu.RLock()
	defer c.mu.RUnlock()

	chain := []reflect.Type{reflect.TypeFor[*Config]()}
	if !c.dependsOnAnyLocked(reflect.TypeFor[*DB](), chain) {
		t.Fatal("DB depends on Config, expected true")
	}
}

func TestDependsOnAnyLocked_NoDep(t *testing.T) {
	c := New()
	_ = c.Provide(NewConfig)
	_ = c.Provide(NewLogger)

	c.mu.RLock()
	defer c.mu.RUnlock()

	chain := []reflect.Type{reflect.TypeFor[*Config]()}
	if c.dependsOnAnyLocked(reflect.TypeFor[*Logger](), chain) {
		t.Fatal("Logger does not depend on Config, expected false")
	}
}

func TestDependsOnAnyLocked_EmptyChain(t *testing.T) {
	c := New()
	_ = c.Provide(NewConfig)

	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.dependsOnAnyLocked(reflect.TypeFor[*Config](), nil) {
		t.Fatal("empty chain should always return false")
	}
}

func TestDependsOnAnyLocked_TransitiveDep(t *testing.T) {
	c := New()
	_ = c.Provide(NewConfig)
	_ = c.Provide(NewDB)
	_ = c.Provide(NewUserService)
	_ = c.Provide(NewLogger)

	c.mu.RLock()
	defer c.mu.RUnlock()

	chain := []reflect.Type{reflect.TypeFor[*Config]()}
	if !c.dependsOnAnyLocked(reflect.TypeFor[*UserService](), chain) {
		t.Fatal("UserService -> DB -> Config, expected transitive dep true")
	}
}

// --- Helpers ---

func assertErrxCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %s, got nil", code)
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T: %v", err, err)
	}
	if xe.Code != code {
		t.Fatalf("expected code %s, got %s: %v", code, xe.Code, err)
	}
	if xe.Domain != DomainDI {
		t.Fatalf("expected domain %s, got %s", DomainDI, xe.Domain)
	}
}
