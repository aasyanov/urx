# Industrial Service Runtime — архитектурная спецификация

> **Статус:** design document (не часть urx API).  
> **Аудитория:** разработчики platform/industrial stack, которые собирают OT-сервисы из urx + steward.  
> **Цель:** оператор **не лезет в файлы конфигурации** — всё через web UI, с hot-reload и корректным lifecycle.

---

## 1. Что мы строим

Production-сервис для **Operational Technology** (SCADA gateway, NVR, IoT hub, Modbus fleet manager):

- десятки long-lived компонентов (logger, DB, HTTP, pools камер/PLC);
- конфиг меняется **из web UI**, не из YAML на диске;
- при смене конфига — **пересоздание только затронутых сущностей** (drain → stop → start);
- при первом запуске — конфиг по умолчанию, запись на диск, старт всех unit'ов;
- boot-time overrides через env и CLI flags (12-factor), но **не** через UI.

**Не строим:**

- distributed orchestrator / mini-Kubernetes;
- magic auto-cascade «изменил logger → steward сам перезапустит DB»;
- reflection-based config binding;
- единый mega-wrap в urx.

---

## 2. Стек и роли

```text
┌─────────────────────────────────────────────────────────────────────┐
│  L1  Composition Layer          (ваш код в каждом сервисе)          │
│      ConfigStore, HTTP Config API, ApplyConfig, Ref[T], DI wiring    │
│      «что пересоздавать при смене секции конфига»                   │
└───────────────────────────────┬─────────────────────────────────────┘
                                │
┌───────────────────────────────▼─────────────────────────────────────┐
│  L2  github.com/aasyanov/steward                                    │
│      Instance[C] — singleton (Logger, DB, HTTP, Metrics)              │
│      Set[K,C]    — homogeneous pool (cameras, PLCs, consumers)      │
│      Start / Reload / Reconcile / Stop / Drain / Policy / Events    │
└───────────────────────────────┬─────────────────────────────────────┘
                                │
┌───────────────────────────────▼─────────────────────────────────────┐
│  L3  urx + domain units                                             │
│      cfgx  — file ↔ struct (persistence)                            │
│      envx  — env overrides (boot only)                              │
│      clix  — CLI flags (boot / dev)                                 │
│      healthx, signalx, poolx, … — по необходимости                │
│      Camera, ModbusClient, Recorder, … — business logic             │
└─────────────────────────────────────────────────────────────────────┘
```

| Слой | Отвечает за | Не отвечает за |
|------|-------------|----------------|
| **urx (cfgx/envx/clix)** | Загрузка/сохранение/парсинг конфига, boot overrides | Lifecycle, hot-reload graph |
| **steward** | Start/Stop/Reload unit'а, reconcile set'ов, policy restart | DI graph, cascade deps, HTTP API |
| **L1 (ваш сервис)** | Web API, diff конфига, cascade rules, Ref handles | Парсинг YAML, supervisor goroutines |

---

## 3. Единый Config struct

Весь сервис описывается **одной** (или иерархией связанных) Go-структурой:

```go
type Config struct {
    Service  ServiceConfig  `yaml:"service"  json:"service"`
    Logger   LoggerConfig   `yaml:"logger"   json:"logger"`
    Database DatabaseConfig `yaml:"database" json:"database"`
    HTTP     HTTPConfig     `yaml:"http"     json:"http"`
    Metrics  MetricsConfig  `yaml:"metrics"  json:"metrics"`
    Cameras  map[string]CameraConfig `yaml:"cameras" json:"cameras"`
    // …
}

func (c *Config) Validate(fix bool) []error {
    // ручные проверки полей; fix=true: clamp port, repair obvious mistakes
}
```

**Правила:**

- struct tags для YAML и JSON (UI шлёт JSON, на диск — YAML или JSON по политике);
- `Validate(fix bool) []error` — seam для cfgx ([Validator]);
- секции конфига **1:1** с steward `Instance[C]` или `Set[K,C]`;
- UI показывает те же секции, что и struct (форма, не raw file).

---

## 4. Два режима конфигурации

### 4.1 Boot (старт процесса)

Полная цепочка precedence — **12-factor**:

```text
defaults (struct literal)
    ↓  самый низкий приоритет
cfgx.Load("config.yaml")     — файл на диске
    ↓
envx.BindTo(env, "X", &cfg.X) — APP_* из orchestrator
    ↓
clix.AddFlag(&cfg.X, ...)     — CLI flags
    ↓  самый высокий приоритет при boot
cfg.Validate(false)           — report only
    ↓
composition.Start(cfg)        — steward Instance/Set
```

```go
func LoadBootConfig(path string, args []string) (Config, *clix.Parser, error) {
    cfg := DefaultConfig()

    if err := cfgx.Load(path, &cfg, cfgx.WithCreateIfMissing()); err != nil {
        return cfg, nil, err
    }

    env := envx.New(envx.WithPrefix("APP"))
    envx.BindTo(env, "HTTP_PORT", &cfg.HTTP.Port)
    envx.BindTo(env, "DB_HOST", &cfg.Database.Host)
    // … явный список полей, которые env может переопределить

    p := clix.New(args, "myservice", "Industrial gateway",
        clix.AddFlag(&cfg.HTTP.Port, "http-port", "", cfg.HTTP.Port, "HTTP listen port"),
        clix.SubCommand("serve", "start", clix.Run(noop)), // или реальный action
    )

    if err := errors.Join(env.Validate(), p.Err()); err != nil {
        return cfg, p, err
    }
    if errs := cfg.Validate(false); len(errs) > 0 {
        return cfg, p, errors.Join(errs...)
    }
    return cfg, p, nil
}
```

**Env и flags после boot не участвуют** в hot-reload из UI. Это deployment/dev overrides.

### 4.2 Runtime (изменение через Web UI)

```text
Operator → Web UI → PUT /api/config
    ↓
merge/replace incoming JSON
    ↓
cfg.Validate(fix=true)     — ДО записи на диск
    ↓
cfgx.Save(path, &cfg)      — persistence (оператор не трогает файл)
    ↓
root.ApplyConfig(ctx, cfg) — L1: selective steward Reload/Reconcile
    ↓
Events / Snapshot → UI feedback
```

**Не вызываем** envx/clix повторно при UI-изменении.

### 4.3 Политика «кто побеждает при рестарте»

| Источник | Когда применяется | Виден оператору в UI |
|----------|-------------------|----------------------|
| Defaults | всегда (base) | да, как начальные значения |
| File (cfgx) | boot + после каждого UI save | да — это canonical state |
| Env (APP_*) | только boot | нет — infra override, документировать |
| CLI flags | boot / dev | нет |

Документируйте для OT: «поле Port можно менять в UI; при redeploy APP_HTTP_PORT переопределит его до следующего save из UI».

---

## 5. ConfigStore — хранилище конфига в процессе

```go
type Store struct {
    mu      sync.RWMutex
    path    string
    current Config
}

func (s *Store) Load() error {
    cfg := DefaultConfig()
    err := cfgx.Load(s.path, &cfg, cfgx.WithCreateIfMissing())
    if err != nil { return err }
    s.mu.Lock()
    s.current = cfg
    s.mu.Unlock()
    return nil
}

func (s *Store) Current() Config {
    s.mu.RLock()
    defer s.mu.RUnlock()
    return s.current // или deep copy если mutating
}

func (s *Store) ApplyValidated(next Config) error {
    if err := cfgx.Save(s.path, &next); err != nil {
        return err
    }
    s.mu.Lock()
    s.current = next
    s.mu.Unlock()
    return nil
}
```

**Контракты:**

- `Current()` — thread-safe read для handlers;
- запись на диск **только после** успешной `Validate`;
- при ошибке `ApplyConfig` (steward) — политика отката (см. §12);
- **`Current()` возвращает deep copy** — иначе UI/handler мутирует runtime state через общую struct;
- **backup перед save:** `config.yaml` → `config.yaml.bak` (atomic rename) — OT восстановление после сбоя питания;
- **revision/ETag:** monotonic `config_revision` (uint64) в struct или sidecar; UI шлёт `If-Match` на PUT — защита от «два оператора сохранили одновременно».

```go
type Store struct {
    mu       sync.RWMutex
    applyMu  sync.Mutex   // сериализация ApplyConfig (см. §22)
    path     string
    current  Config
    revision uint64
}

func (s *Store) ApplyValidated(next Config) error {
    // backup → save → bump revision (atomically по возможности)
}
```

---

## 6. Composition Root — сердце L1

```go
type Root struct {
    cfg Config

    loggerRef *LoggerRef          // stable dep — Ref pattern
    db        *steward.Instance[DatabaseConfig]
    http      *steward.Instance[HTTPConfig]
    metrics   *steward.Instance[MetricsConfig]
    cameras   *steward.Set[string, CameraConfig]
}

func NewRoot(cfg Config) (*Root, error) {
    r := &Root{cfg: cfg, loggerRef: NewLoggerRef()}

    r.db = steward.NewInstance(cfg.Database, buildDB(r.loggerRef), equalDB)
    r.http = steward.NewInstance(cfg.HTTP, buildHTTP(r.loggerRef, r.db), equalHTTP)
    r.metrics = steward.NewInstance(cfg.Metrics, buildMetrics(r.loggerRef), equalMetrics)
    r.cameras = steward.NewSet(buildCamera(r.loggerRef, r.db), equalCamera)

    return r, nil
}

func (r *Root) Start(ctx context.Context) error {
    // Порядок Start — ответственность L1 (нет DAG в steward)
    if err := r.db.Start(ctx); err != nil { return err }
    if err := r.metrics.Start(ctx); err != nil { return err }
    if err := r.http.Start(ctx); err != nil { return err }
    if err := r.cameras.Start(ctx); err != nil { return err }
    return r.cameras.Reconcile(r.cfg.Cameras)
}
```

### 6.1 ApplyConfig — diff и selective reload

```go
func (r *Root) ApplyConfig(ctx context.Context, next Config) error {
    old := r.cfg

    // 1. Logger — Ref swap (без рестарта consumers) ИЛИ Instance.Reload
    if !equalLogger(old.Logger, next.Logger) {
        if err := r.applyLogger(next.Logger); err != nil {
            return fmt.Errorf("logger: %w", err)
        }
    }

    // 2. Database — stateful, полный Reload
    if !equalDB(old.Database, next.Database) {
        if err := r.db.Reload(next.Database); err != nil {
            return fmt.Errorf("database: %w", err)
        }
        // L1 cascade: cache зависит от DB — явно
        if err := r.applyCacheAfterDB(next); err != nil {
            return err
        }
    }

    // 3. HTTP — см. §7 (можно менять через web!)
    if !equalHTTP(old.HTTP, next.HTTP) {
        if err := r.http.Reload(next.HTTP); err != nil {
            return fmt.Errorf("http: %w", err)
        }
    }

    // 4. Cameras — homogeneous set
    if !equalCameraMap(old.Cameras, next.Cameras) {
        if err := r.cameras.Reconcile(next.Cameras); err != nil {
            return fmt.Errorf("cameras: %w", err)
        }
    }

    r.cfg = next
    return nil
}
```

**Таблица cascade (заполнить для каждого сервиса):**

| Изменилась секция | Действие | Cascade (явно) |
|-------------------|----------|----------------|
| `Logger` | `Ref.Store(newLogger)` или `loggerInst.Reload` | none (Ref) / none |
| `Database` | `db.Reload` | cache Reload, возможно camera Set Replace |
| `HTTP` | `http.Reload` | none (если handlers через Ref) |
| `Metrics` | `metrics.Reload` | none |
| `Cameras` | `cameras.Reconcile` | per-key diff only |

Steward **не** вычисляет cascade — только вы.

### 6.2 Replace vs Reload vs Reconcile — когда что

| API steward | Когда вызывать |
|-------------|----------------|
| `Instance.Reload(cfg)` | Изменилась **секция конфига** `C`, Build/equal closure **те же** |
| `Instance.Replace(build, equal, cfg)` | Изменились **зависимости в Build closure** (новый DB pool, новый LoggerRef target) — пересоздаёт **все** unit'ы этого Instance даже при том же cfg |
| `Set.Reconcile(desired)` | Изменился `map[key]C` — diff по ключам |
| `Set.Replace(build, equal, desired)` | Build closure зависит от нового глобального ресурса — **весь** set пересоздаётся |

**Тонкость:** после `db.Reload` старый `*sql.DB` в closure камер **устарел**. Если camera `Build` захватывал DB по значению — нужен `dbRef.Get()` или `cameras.Replace(newBuild, …)` после смены DB.

```go
type DBRef struct { v atomic.Value } // *sql.DB
// Build camera: dbRef.Get().Query(...)
// После db.Reload: dbRef.Store(newPool) — cameras.Reconcile достаточно
// Если Build без Ref — cameras.Replace(...)
```

### 6.3 ApplyConfig — сериализация и порядок секций

- **Один ApplyConfig в момент времени** — `applyMu.Lock()` в L1; параллельные PUT из UI в очередь или 409 Conflict.
- **Порядок секций в ApplyConfig** имеет значение при cascade: logger (Ref) → DB → cache → HTTP → pools.
- **Не вызывать ApplyConfig из steward Event handler** — риск deadlock (scheduler ждёт API, API ждёт scheduler).
- **`ctx` с timeout** на ApplyConfig (например 2–5 min для drain 10k cameras).

### 6.4 Handoff для stateful pool units

При смене конфига камеры без полного restart буфера:

```go
steward.WithHandoff(func(old, new steward.Unit) error {
    return copyRingBuffer(old.(*Camera), new.(*Camera))
})
```

Handoff **в scheduler goroutine** — только O(1) копирование указателей, не I/O. Иначе блокируется весь Set.

---

## 7. HTTP-сервер: можно ли менять через Web?

**Да.** Конфиг HTTP (`HTTPConfig`) — обычная секция в общем `Config`:

```go
type HTTPConfig struct {
    Host         string        `yaml:"host" json:"host"`
    Port         int           `yaml:"port" json:"port"`
    ReadTimeout  time.Duration `yaml:"read_timeout" json:"read_timeout"`
    WriteTimeout time.Duration `yaml:"write_timeout" json:"write_timeout"`
    TLS          TLSConfig     `yaml:"tls" json:"tls"`
    CORS         CORSConfig    `yaml:"cors" json:"cors"`
}
```

### 7.1 Что меняется «горячо» без рестарта listener

| Поле | Hot без Reload | Комментарий |
|------|----------------|-------------|
| CORS allowed origins | да | atomic/config inside handler |
| request size limits | да | если читается per-request |
| log level middleware | да | через Ref[Logger] |
| **Listen address (host:port)** | **нет** | нужен Reload HTTP Instance |
| **TLS cert paths** | **нет** | reload listener + new tls.Config |
| Read/Write timeout на `http.Server` | частично | Go не меняет на лету — нужен Reload |

### 7.2 HTTP Unit через steward

```go
type HTTPServer struct {
    cfg    HTTPConfig
    server *http.Server
    ln     net.Listener
}

func (h *HTTPServer) Start(ctx context.Context) error {
    go h.serve() // net.Listen в goroutine или WaitReady
    return nil
}

func (h *HTTPServer) WaitReady(ctx context.Context) error {
    // TCP dial localhost:port или errgroup
    return waitUntilListening(ctx, h.cfg.Addr())
}

func (h *HTTPServer) Drain(ctx context.Context) error {
    return h.server.Shutdown(ctx) // graceful
}

func (h *HTTPServer) Stop(ctx context.Context) error {
    if h.ln != nil { return h.ln.Close() }
    return nil
}
```

При `httpInst.Reload(newCfg)` steward выполнит: **Build(new) → Drain(old) → Stop(old) → Start(new)**.

**Важно для OT UI:**

- смена порта = кратковременный downtime HTTP (seconds);
- UI должен показыть предупреждение: «изменение порта перезапустит web interface; переподключитесь по новому адресу»;
- можно validation rule: «port change requires confirm flag in request».

### 7.3 Config API живёт на том же HTTP Instance

Паттерн: handlers регистрируются через **Ref** или **callback**, чтобы не рестартовать routes при каждом мелком изменении:

```go
type RouteRegistry struct {
    mu sync.RWMutex
    mux *http.ServeMux
}

// Config handler всегда на /api/config — регистрируется один раз при Build
// Business handlers могут читать cfg через Ref[Config] или Store.Current()
```

При Reload HTTP — config API тоже перезапускается вместе с listener (это OK: reload редкий, drain короткий).

**Альтернатива (advanced):** два listener — admin (config API) на фиксированном порту + business HTTP. Для OT часто проще **один порт** + предупреждение в UI.

### 7.4 Chicken-and-egg: первый boot без UI

```text
1. DefaultConfig() + cfgx.Load(WithCreateIfMissing) → файл создан
2. root.Start → HTTP слушает с дефолтным port (8080)
3. Оператор открывает browser → UI → меняет конфиг
```

HTTP **не** ждёт «идеального» конфига — стартует с defaults. `WithCreateIfMissing` гарантирует persistence после первого boot.

### 7.5 Admin vs business split (optional)

| Порт | Назначение | Reload при смене business HTTP |
|------|------------|--------------------------------|
| `:8080` | Config API + UI (admin) | **нет** — фиксирован |
| `:8081` | Business API / metrics | **да** |

Снижает риск «перезагрузил business port — потерял доступ к UI». Стоимость — второй listener и документация портов для OT.

### 7.6 TLS и сертификаты через UI

- **Пути к файлам** (`tls.cert_file`) — UI меняет path → HTTP Reload.
- **Upload PEM** — API `POST /api/config/tls/cert` пишет файлы в `data/certs/` (вне config.yaml), затем PATCH `http.tls`.
- **Секреты не в JSON config** — см. §21.

---

## 8. Logger и stable dependencies

### 8.1 Ref pattern (рекомендуется для logger/metrics/tracer)

```go
type LoggerRef struct {
    v atomic.Value // Logger interface
}

func (r *LoggerRef) Get() Logger {
    return r.v.Load().(Logger)
}

func (r *LoggerRef) Replace(cfg LoggerConfig) error {
    log, err := buildLogger(cfg)
    if err != nil { return err }
    r.v.Store(log)
    return nil
}
```

DB, HTTP handlers, camera workers вызывают `loggerRef.Get().Info(...)` — при смене конфига logger **consumers не рестартуют**.

### 8.2 Instance.Reload для logger

Используйте, если logger:

- держит открытый файл с буфером → нужен Drain/flush;
- подключён к remote syslog с reconnect policy как Unit.

```go
loggerInst := steward.NewInstance(cfg.Logger, buildLoggerUnit, equalLogger)
// ApplyConfig: loggerInst.Reload(next.Logger)
```

---

## 9. Homogeneous pools (cameras, PLCs, workers)

```go
cameras := steward.NewSet[string, CameraConfig](
    func(_ context.Context, id string, cfg CameraConfig) (steward.Unit, error) {
        return NewCamera(id, cfg, loggerRef, dbRef), nil
    },
    func(a, b CameraConfig) bool { return a == b },
    steward.WithPolicy(steward.DefaultPolicy{...}),
    steward.WithHandoff(migrateCameraBuffers), // optional
)
```

UI редактирует `map[string]CameraConfig`:

- добавил камеру → reconcile create;
- удалил → reconcile remove (Drain → Stop);
- сменил URL → reconcile update (restart только этой камеры).

**Equal must be explicit** — steward не использует `reflect.DeepEqual`.

---

## 10. HTTP Config API (контракт для UI)

### 10.1 Endpoints

| Method | Path | Описание |
|--------|------|----------|
| `GET` | `/api/config` | Текущий конфиг (JSON) |
| `GET` | `/api/config/schema` | JSON Schema / field metadata для формы |
| `PUT` | `/api/config` | Полная замена конфига |
| `PATCH` | `/api/config/{section}` | Частичное изменение секции (`logger`, `http`, …) |
| `POST` | `/api/config/validate` | Dry-run validate без save/apply |
| `GET` | `/api/health` | Aggregate: steward HealthStatus + business |
| `GET` | `/api/units` | steward Snapshot — состояние unit'ов |
| `GET` | `/api/events` | SSE/WebSocket lifecycle events (optional) |
| `POST` | `/api/config/dry-run` | validate + diff + planned reloads **без** save/apply |
| `GET` | `/api/config/revision` | текущий revision / ETag |

### 10.2 PUT flow (normative)

```text
1. Authenticate / authorize (OT: RBAC, не в scope urx)
2. If-Match: revision (optional) — mismatch → 409 Conflict
3. Decode JSON body → Config
4. Deep copy → Validate(fix=true) на копии (не мутировать body клиента неожиданно)
5. Merge policy (PUT = full replace vs PATCH = section merge)
6. cfg.Validate(fix=true)
   └─ error → 422 + список всех проблем (как envx.Validate)
7. store.ApplyValidated(cfg)  → backup + cfgx.Save
8. root.ApplyConfig(ctx, cfg)  — под applyMu
   └─ error → 500 + partial state policy (§12)
9. 200 + applied config + revision + unit statuses
```

### 10.4 PATCH merge semantics

```go
// PATCH /api/config/http — merge только секции HTTP поверх Current()
next := store.Current()
mergeSection(&next.HTTP, incoming.HTTP)
```

- **Maps** (cameras): PATCH key = upsert одной камеры; `DELETE /api/config/cameras/{id}` — удаление.
- **Nil vs empty:** документировать JSON `null` — «не менять» vs «очистить».
- **Secrets:** PATCH не принимает `password` — только `POST /api/secrets/...` (§21).

### 10.5 Dry-run apply

```json
POST /api/config/dry-run
→ {
  "valid": true,
  "would_reload": ["http", "cameras.cam-7"],
  "would_restart_count": 1,
  "warnings": ["http.port change drops active sessions"]
}
```

Оператор видит impact **до** Apply — критично для OT.

### 10.3 Пример ответа validate

```json
{
  "valid": false,
  "errors": [
    {"path": "http.port", "message": "must be > 0"},
    {"path": "cameras.cam-3.url", "message": "invalid rtsp URL"}
  ],
  "fixed": [
    {"path": "logger.level", "from": "", "to": "info"}
  ]
}
```

---

## 11. Полный lifecycle процесса

```text
main()
  │
  ├─ signalx.Trap(SIGINT, SIGTERM)     // §27
  │
  ├─ cfg, p := LoadBootConfig("config.yaml", os.Args[1:])
  │
  ├─ store := config.NewStore("config.yaml", cfg)
  ├─ root := composition.NewRoot(cfg)
  ├─ root.Start(ctx)
  │
  ├─ healthx.New + Register steward/db checks  // §26
  ├─ configHandler := api.NewConfigHandler(store, root)
  ├─ register routes on HTTP unit (or Ref registry)
  │
  ├─ p.Run()  // если CLI subcommand (serve/migrate/…)
  │
  └─ signalx.Wait(ctx,
        func(ctx context.Context) { root.Stop(ctx) },  // ПЕРВЫМ — drain all units
        func(ctx context.Context) { /* flush audit */ },
     )
```

**Не** вызывать `root.Stop` и `ApplyConfig` параллельно — shutdown побеждает (reject new PUT with 503).

```text
                    ┌──────────────┐
                    │   Running    │
                    └──────┬───────┘
                           │
         ┌─────────────────┼─────────────────┐
         │                 │                 │
    UI PUT config    SIGTERM            unit failure
         │                 │                 │
         v                 v                 v
  ApplyConfig         root.Stop()      Policy restart
  (selective          (Drain→Stop      (same config,
   Reload)             all units)        backoff)
```

---

## 12. Ошибки, откат, partial apply

### 12.1 Validate failed

- **Не** писать на диск;
- **Не** вызывать steward;
- UI показывает все ошибки сразу.

### 12.2 Save OK, ApplyConfig failed

Steward может оставить **partial state** (особенно mid-Reconcile).

**Политики (выберите одну и документируйте):**

| Policy | Поведение |
|--------|-----------|
| **A: Fail loud** | HTTP 500, файл уже новый, runtime частично применён; оператор смотрит `/api/units`, retry или manual restart |
| **B: Rollback file** | cfgx.Save(oldConfig) + попытка ApplyConfig(old) |
| **C: Desired vs Actual** | Store хранит `desired` и `applied`; UI показывает drift |

Для OT рекомендуется **A + Snapshot UI** + audit log каждого изменения.

### 12.3 Config errors in steward

Используйте `steward.ClassifyError(steward.FailureConfigError, err)` в Unit — **policy не будет бесконечно рестартить** битый конфиг.

### 12.4 Validate(fix=true) и мутации

`Validate(true)` **может мутировать** struct (clamp port, default level). Правила:

- dry-run и validate endpoint — на **копии**, возвращать `fixed[]` клиенту;
- перед save — применять fix к canonical config;
- UI показывает: «мы исправили X→Y, сохранить?»

### 12.5 Policy restart vs manual Apply

Runtime failure → steward Policy restart (тот же config).  
UI Apply → manual Reload/Reconcile (новый config).

**Не смешивать:** во время `ApplyConfig` policy restart того же unit может гонять с Reload — steward сериализует на уровне scheduler, но L1 должен логировать источник изменения (audit: `user` vs `policy`).

---

## 13. Web UI — требования для OT

1. **Формы по секциям** — Service, Logger, HTTP, Database, Cameras (tabs).
2. **Validate before Apply** — кнопка «Проверить» → `POST /api/config/validate`.
3. **Предупреждения** — смена HTTP port/TLS → modal confirm.
4. **Unit status panel** — Snapshot: Running / Starting / Failed per camera, DB, HTTP.
5. **Не показывать** raw YAML по умолчанию (advanced mode optional).
6. **Audit trail** — кто, когда, что изменил (вне urx, но обязательно для OT).
7. **Reconnection** — после HTTP port change UI переподключается к новому URL.
8. **Impact preview** — dry-run перед Apply (§10.5).
9. **Conflict** — «конфиг изменился другим оператором, обновите страницу» (409).
10. **Read-only role** — просмотр без PUT; operator vs admin RBAC.

---

## 21. Secrets vs Config — жёсткое разделение

**В `Config` struct / UI / config.yaml на диске:**

| OK | NOT OK |
|----|--------|
| `database.host`, `database.user` | `database.password` plain text |
| `tls.cert_file` (path) | `tls.private_key_pem` inline |
| `secret_ref: "vault:db-password"` | API keys in YAML |

**Паттерн:**

```go
type DatabaseConfig struct {
    Host     string `json:"host"`
    User     string `json:"user"`
    Password string `json:"-" yaml:"-"` // never serialize to file/UI
}

// Boot: envx.BindRequired(env, "DB_PASSWORD") → заполняет Password только in-memory
// UI: показывает "••••••" + кнопка "rotate via env/deployment"
```

- Секреты через **env / vault / sealed secrets** при deploy;
- UI **redacts** при GET `/api/config`;
- audit log: **не писать** значения секретов, только field names.

Для OT: оператор меняет **поведение** (port, cameras, timeouts), не пароли — пароли меняет admin через процедуру deploy.

---

## 22. Конкурентность и блокировки

| Ресурс | Защита |
|--------|--------|
| `Store.current` | `sync.RWMutex` |
| `ApplyConfig` | `applyMu` — один apply |
| steward scheduler | уже serialized `cmdCh` |
| UI long-poll / SSE | отдельная goroutine, не держать applyMu |

**Два оператора:** без ETag второй затрёт первого silently — **обязателен** revision + 409.

**Apply во время Reconcile 10k cameras:** UI показывает progress через steward `Events()` (SSE), не блокирует HTTP read endpoints.

---

## 23. Версионирование и миграция схемы

```go
type Config struct {
    SchemaVersion int `yaml:"schema_version" json:"schema_version"`
    // ...
}
```

- `Load` → если `schema_version < current` → `Migrate(cfg)` in-memory → optional Save;
- UI/API отклоняет unknown fields или игнорирует (document policy);
- breaking change → bump schema, migration function, тесты миграции.

Без версии OT-апгрейды бинарника ломают старые `config.yaml` на сотнях площадок.

---

## 24. Редактирование файла «в обход» UI

Инженер всё равно может открыть `config.yaml` на диске. Политики:

| Policy | Поведение |
|--------|-----------|
| **UI-only (strict)** | fsnotify игнорируется; только restart подхватывает файл |
| **Watch reload (dev)** | fsnotify → Load → ApplyConfig (опасно в prod без audit) |
| **Hybrid (recommended)** | fsnotify → log warning + metric; Apply только после restart или explicit `POST /api/config/reload-from-disk` с confirm |

Для OT production: **UI canonical**, file edit = break-glass procedure с документированным restart.

---

## 25. Maintenance mode и health probes (healthx)

```go
hc := healthx.New()
hc.Register("steward", func(ctx context.Context) error {
    ok, views, err := root.HealthAggregate()
    // ...
})
hc.RegisterHandlers(mux) // /livez, /readyz, /healthz
```

| Probe | Во время ApplyConfig |
|-------|----------------------|
| **Liveness** (`/livez`) | остаётся UP — процесс жив |
| **Readiness** (`/readyz`) | **DOWN** на время apply (optional `hc.SetDown(true)`) — orchestrator не шлёт трафик |
| **Per-camera** | Snapshot: Starting/Failed видны в UI |

**Паттерн:**

```go
func (r *Root) ApplyConfig(ctx context.Context, next Config) error {
    r.hc.SetDown(true)
    defer r.hc.SetDown(false)
    // ... reloads ...
}
```

K8s/load balancer перестаёт слать запросы на business port, пока reload не завершён — без half-ready state.

---

## 26. Graceful shutdown (signalx)

```go
ctx, cancel := signalx.Trap(context.Background())
defer cancel()

signalx.OnShutdown(func(ctx context.Context) {
    root.Stop(ctx) // Drain → Stop всех units; BLOCKS
})

// Config API: reject new PUT when ctx.Done()
```

**Порядок hooks:** `root.Stop` **первым** (дольше всех — drain cameras). Audit flush, metrics — после.

**Timeout:** `signalx.Wait` с bounded timeout; если drain cameras не уложился — log + force (document OT procedure).

---

## 27. Edge, air-gap, multi-node

| Deployment | Config |
|------------|--------|
| **Single appliance** | один `config.yaml`, UI localhost |
| **Air-gap OT** | UI bundled static files, no CDN; всё on-box |
| **N replicas** | каждая нода — **свой** файл OR central config service (вне scope этого doc) |
| **Fleet** | export/import JSON bundle через UI (backup/restore), не ручной scp yaml |

**Не предполагать** cloud connectivity для UI — industrial software работает offline.

---

## 28. Observability

| Metric / log | Зачем |
|--------------|-------|
| `config_apply_duration_seconds` | SLA reload |
| `config_apply_errors_total` | alert |
| `steward_events_dropped` | потеря UI events |
| audit: `{user, revision, diff_summary, outcome}` | compliance |
| structured log на каждый Reload с `unit_id` | postmortem |

---

## 29. Антипatterns (избегать)

| Anti-pattern | Почему плохо |
|--------------|--------------|
| `ApplyConfig` без applyMu | гонки, partial corrupt state |
| `reflect` auto-bind struct ↔ env/flags | против urx, runtime panic |
| секреты в YAML «временно» | утечёт в backup/git |
| cascade «магия» в steward | не поддерживается, скрытые зависимости |
| blocking `Build`/`Equal` | заморозка всего Set |
| reload HTTP без confirm на port change | оператор теряет UI |
| `Current()` без deep copy | UI мутирует runtime |
| panic recover в steward callbacks | steward design: panic = crash |

---

## 14. Структура проекта сервиса

```text
myservice/
├── cmd/myservice/
│   └── main.go                 # boot chain, signal, cli dispatch
├── internal/
│   ├── config/
│   │   ├── config.go           # Config struct, DefaultConfig, Validate
│   │   ├── equal.go            # equalLogger, equalHTTP, … для steward
│   │   └── store.go            # ConfigStore (cfgx Load/Save)
│   ├── composition/
│   │   ├── root.go             # Root, NewRoot, Start, Stop
│   │   ├── apply.go            # ApplyConfig, cascade table
│   │   └── refs.go             # LoggerRef, ConfigRef, …
│   ├── units/
│   │   ├── logger.go
│   │   ├── database.go
│   │   ├── http.go             # HTTPServer as steward.Unit
│   │   ├── metrics.go
│   │   └── camera.go
│   └── api/
│       ├── config_handler.go   # GET/PUT/PATCH /api/config
│       ├── health_handler.go
│       └── schema.go             # JSON Schema generation (optional)
├── web/                        # frontend (optional in same repo)
│   └── …
├── config.yaml                 # created on first boot (WithCreateIfMissing)
└── README.md                   # OT operator guide (не file editing!)
```

**Не класть в urx:** `composition`, `ConfigStore`, HTTP handlers — это код каждого сервиса.

---

## 15. Тестирование

| Layer | Как тестировать |
|-------|-----------------|
| cfgx | `WithReader`/`WithWriter` inject, no disk |
| envx | `MapLookup` |
| clix | `New(fixedArgs, …)`, `Reset(args)` |
| Validate | table-driven invalid configs |
| ApplyConfig | mock steward или integration with short-lived Instance |
| HTTP API | httptest + Store mock |
| E2E | boot → PUT config → assert Snapshot state |

```go
func TestApplyConfig_HTTPPortChange_ReloadsServer(t *testing.T) {
    root := newTestRoot(defaultCfg)
    root.Start(testx.TimedCtx(t, 5*time.Second))

    next := defaultCfg
    next.HTTP.Port = 9999
    require.NoError(t, root.ApplyConfig(ctx, next))
    testx.Eventually(t, func() bool {
        ok, _, _ := root.http.HealthStatus()
        return ok
    }, 5*time.Second)
}
```

---

## 16. Интеграция с другими urx-пакетами

| Пакет | Роль в runtime |
|-------|----------------|
| **healthx** | `/livez`/`/readyz`/`/healthz`; `SetDown` на время Apply; Register checks на DB/steward (§25) |
| **signalx** | Trap + Wait; `root.Stop` первым hook; reject config PUT при shutdown (§26) |
| **poolx** | workers внутри camera/recorder Unit |
| **panix** | Safe() в HTTP handlers (не в steward callbacks!) |
| **syncx.Lazy** | ленивые тяжёлые клиенты внутри Unit Build |

---

## 17. Checklist перед production

- [ ] Cascade table задокументирована и покрыта тестами
- [ ] Validate собирает **все** ошибки (не fail-fast на первой)
- [ ] Save только после Validate; backup `.bak` перед save
- [ ] ETag/revision + 409 on conflict
- [ ] applyMu сериализует ApplyConfig
- [ ] Secrets **не** в file/UI (§21)
- [ ] GET config redacts secrets
- [ ] HTTP port/TLS change — confirm + dry-run warnings
- [ ] Drain timeouts настроены (Policy.DrainTimeout, StopTimeout)
- [ ] `Equal` функции O(1), без I/O
- [ ] `Build` не блокирует scheduler
- [ ] `Start` не блокирует (Listen в WaitReady/goroutine)
- [ ] Ref для DB/logger в Build closures камер (§6.2)
- [ ] Events / Snapshot для operator dashboard
- [ ] Readiness DOWN во время apply (optional)
- [ ] signalx: root.Stop первым
- [ ] schema_version + migration tests
- [ ] Boot env overrides документированы для deploy team
- [ ] Audit log изменений конфига (без secret values)
- [ ] Dry-run endpoint для OT operators

---

## 18. FAQ

### Нужен ли единый wrap в urx?

**Нет.** urx даёт кирпичи. L1 — в каждом сервисе. Дублирование `LoadBootConfig` можно вынести в **platform repo** (не urx), когда 5+ сервисов с одинаковым boot flow.

### Можно ли менять конфиг HTTP через web?

**Да** — `HTTPConfig` в общем struct, `httpInst.Reload` через ApplyConfig. Смена listen addr = restart listener с drain. Мелкие вещи (CORS) — через Ref без Reload.

### Пересоздавать logger в DI при каждом изменении?

**Зависит.** Ref swap — без рестарта consumers. Instance.Reload — когда нужен flush/drain файла.

### Что если оператор сломал конфиг?

Validate на API → 422, runtime не тронут. Если apply частично прошёл → Snapshot + manual fix или process restart из last good file backup.

### steward заменяет fx/wire?

**Нет.** fx/wire — boot wiring. steward — runtime lifecycle. Можно совместить: fx создаёт Root, steward управляет Unit'ами.

### Почему не fsnotify auto-reload?

См. §24 — в OT prod UI canonical; file watch без audit опасен.

---

## 19. Ссылки

- urx cfgx/envx/clix — configuration I/O and boot overrides
- [steward](https://github.com/aasyanov/steward) — L2 lifecycle control plane
- 12-factor config: env overrides at boot; file as persistence; UI as operator interface

---

## 20. Следующие шаги (implementation)

1. Определить `Config` struct + `schema_version` для первого сервиса.
2. Заполнить cascade table (§6.1) + Ref vs Replace rules (§6.2).
3. Реализовать `Store` (backup, revision) + `LoadBootConfig`.
4. Реализовать units (HTTP, DB, logger Ref, один Set).
5. Реализовать `ApplyConfig` (applyMu, healthx SetDown).
6. HTTP Config API (validate, dry-run, ETag) + minimal UI.
7. Soak test: 1000x PATCH config, assert no goroutine leak, Snapshot stable.
8. Migration test: old schema_version → current.

---

## 30. Почему industry rarely does this — landscape «блокнот + YAML»

Этот раздел — не оправдание статус-кво и не маркетинг. Это честный разбор, **почему** правка конфигов в Notepad/WinSCP до сих пор норма — и **где** ваш подход (web UI + hot-reload + steward) не luxury, а requirement.

### 30.1 Короткий тезис

Web UI с validate, dry-run, audit и selective reload **не «все тупые, что не делают»** — это **дороже в разработке**, чем «файл + restart», и исторически не было стандартного стека (urx + steward + L1), который делает это дёшево. Для OT-appliance с сотнями камер/PLC и оператором без доступа к shell — **файл + блокнот становится anti-pattern**.

### 30.2 Историческая инерция: Unix и 12-factor

Конфиг как **файл на диске** — простейшая абстракция, понятная с 1990-х:

- diff в git;
- scp/ansible на площадку;
- «откат = вернуть старый файл»;
- `systemctl restart` как универсальный apply.

DevOps 2010-х закрепил: **config in repo / on disk, restart to apply**. Web UI для runtime config — отдельный продукт, не «бесплатное приложение» к бинарнику. urx/cfgx решают I/O файла; **не решают** UX оператора — это L1 + frontend.

### 30.3 Restart проще hot-reload

| Подход | Что нужно инженеру |
|--------|-------------------|
| **Файл + restart** | Save, restart, smoke test |
| **Hot-reload (этот doc)** | Equal, Ref, cascade table, ApplyConfig, drain, partial failure policy, audit, dry-run |

Для команды из 2–3 человек и downtime 30 секунд раз в месяц **restart rational**. Hot-reload окупается, когда:

- сущностей много (Set камер/PLC);
- restart = минуты простоя линии;
- оператор меняет конфиг **ежедневно**, не раз в квартал.

### 30.4 Разрыв vendor ↔ integrator ↔ operator

```text
Vendor (dev)     → поставляет binary + sample config.yaml + PDF стр. 47
Integrator       → Notepad++, WinSCP, «поправь port», restart службы
OT operator      → работает в SCADA/HMI вендора, не в вашем Go-сервисе
```

Если **нет bundled UI** — integrator **обязан** лезть в файл. Это не лень: **другого интерфейса нет**. Ваш target — **operator-facing appliance**: UI в комплекте, файл скрыт как persistence layer.

### 30.5 Kubernetes / GitOps «съели» in-process config UI

В cloud-native:

- ConfigMap + env + rolling restart pod;
- «правь YAML в Git, ArgoCD раскатает».

Это **их блокнот**, только в IDE и с pipeline. In-process Config API конкурирует с **GitOps**, не с Notepad. В edge OT (нет k8s, air-gap box) GitOps часто **отсутствует** — но привычка «config = файл» переносится integrator'ами.

### 30.6 Страх dynamic reconfiguration (OT compliance)

В OT цена ошибки: остановка линии, авария, регуляторика, ночной вызов.

| Файл + restart | Hot-reload |
|----------------|------------|
| Change window, explicit maintenance | «Ночью что-то перезагрузилось» |
| Rollback = старый файл на диске | Rollback = revision + ApplyConfig(old) или restore .bak |
| Расследование: «файл изменён в 02:14» | Нужны **audit + dry-run + unit snapshot** |

Без dry-run, ETag, audit log инженеры **не доверяют** UI и остаются на файле — **рационально**. §10.5, §21, §22 существуют именно чтобы снять этот страх.

### 30.7 Экономика: за config UX не платят

Типичный RFP платит за:

- подключить N Modbus/RTSP точек;
- retention N дней;
- интеграция с SCADA.

Редко платят за:

- forms + JSON Schema;
- optimistic locking (409);
- graceful HTTP reload;
- operator dashboard unit status.

**90% industrial software** = config file + manual restart + PDF. Ваш differentiation — **operator UX как часть продукта**, не appendix.

### 30.8 Нет готового «стандартного L1» в open source

| Есть | Нет (до urx + steward + этот doc) |
|------|-----------------------------------|
| Cobra/Viper, koanf | Canonical OT Config API |
| fx, wire, dig | ApplyConfig + cascade |
| k8s, Helm | In-process reload без orchestrator |
| controller-runtime (k8s) | In-process reconcile для **Go appliance** |

Каждый вендор изобретает с нуля или **не изобретает**. urx + steward — кирпичи; **L1 всё равно ваш** — поэтому мало кто доходит до web UI + hot-reload.

### 30.9 Когда «блокнот + YAML» — rational choice

Оставайтесь на файле, если **все** верны:

- сервис рестартует редко (раз в месяцы);
- конфиг < 20 полей, без pools;
- один integrator с SSH, не OT operator;
- downtime 1–2 мин OK;
- нет audit/compliance на runtime changes;
- нет требования «добавить камеру без restart».

**Не стыдно.** Over-engineering тоже anti-pattern.

### 30.10 Когда ваш подход обязан побеждать

| Сигнал | Почему файл не подходит |
|--------|-------------------------|
| N homogeneous entities (cameras, PLCs) | Reconcile по ключам; restart всего процесса = N× downtime |
| Operator без shell/YAML skills | HMI/appliance UX |
| Частые изменения (cameras, timeouts) | Restart fatigue, ошибки integrator'а |
| Controlled drain | steward Drain → Stop |
| Air-gap appliance | Bundled UI, no GitOps |
| Audit «ктo что менял» | API + revision, не vim на сервере |
| HTTP/config на одной box | §7 admin UI |

Это **appliance / edge gateway / SCADA-adjacent**, не «ещё один cloud microservice».

### 30.11 Кто уже делает «не блокнот» (но вы не видите как Go pattern)

| Домен | Как оператор конфигурирует |
|-------|------------------------------|
| PLC / DCS | Engineering station, не текстовый файл |
| Commercial SCADA | GUI tags, alarms, screens |
| NVR (Hikvision, etc.) | Web UI камер и storage |
| Network appliances | Web UI + apply + sometimes hot |
| Synology / NAS | Full UI, config DB behind |

Паттерн **«форма → validate → apply → restart subsystem»** ubiquitous — просто **не называется cfgx/steward** и часто closed-source. Вы делаете то же **explicitly в Go** с промышленной дисциплиной.

### 30.12 Почему **ваш** стек редок именно в Go open source

1. **Split packages** (cfgx/envx/clix + steward) — осознанный выбор; mega-framework проще продать, сложнее сопровождать.
2. **L1 не в библиотеке** — cascade зависит от домена; vendor не может generic L1 в npm/go module без reflection.
3. **Hot-reload тестируется годами** — soak, race, partial apply; MVP ship file-only.
4. **Integrator culture** — «мы так всегда делали на площадке».

Ваш moat: **с первого релиза** appliance с UI + dry-run + audit, а не «v2 добавим web».

### 30.13 Стратегия для product / platform team

| Аудитория | Message |
|-----------|---------|
| **Integrator** | «Файл есть для break-glass и automation; primary path — UI» (§24 hybrid) |
| **Operator** | «Не трогайте YAML» — training + RBAC |
| **Compliance** | audit log, revision, dry-run, .bak |
| **Dev** | L1 шаблон из §14, cascade table per service |

**Не конкурировать** с «просто yaml» на hello-world сервисах. **Конкурировать** на OT pain: 500 cameras, 3 AM add camera, no SSH.

### 30.14 Сводная таблица

| Утверждение | Вердикт |
|------------|---------|
| «Все тупо правят блокнотом» | **Нет** — rational при restart-OK и без UI budget |
| «Так никто не делает web config» | **Нет** — делают PLC/HMI/appliance; редко в Go OSS |
| «Файл проще» | **Да**, до масштаба pools + OT operators |
| «Наш путь overkill» | **Нет** для appliance; **да** для CLI tool |
| «urx + steward + L1 оправданы» | **Да**, когда цена ошибки оператора > cost разработки platform |

### 30.15 Связь с остальным документом

Каждая «лишняя» сложность здесь (§5 ETag, §10 dry-run, §21 secrets, §25 readiness DOWN) — **ответ на причину §30.6**: без них integrator вернётся к блокноту, потому что **не доверяет** dynamic apply. Industrial software для OT wins on **trust + visibility**, not on «у нас тоже yaml».

---

*Документ описывает target architecture. Код L1 живёт в репозитории каждого OT-сервиса, не в urx.*
