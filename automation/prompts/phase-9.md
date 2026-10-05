# FlowForge Phase 9 — Prometheus + Grafana + OpenTelemetry + Failure Injection + Benchmarking

## Handoff metadata
- Repository: `Daniel-Cpz/FlowForge`
- Shared entry/work branch: `codex/phase1-api-correctness`
- Prompt source: `automation`
- Prompt path: `automation/prompts/phase-9.md`
- Previous phase: Phase 8, COMPLETED
- Reviewed branch revision: `45663d70e9c136e5486bcd66d2b240e4198fc06c`
- Phase 8 implementation checkpoint: `c349113f0f2423890fe199a81308e40cedd695d7`
- Phase 8 annotated tag: `phase8-dashboard-websocket`, verified to target the checkpoint
- Phase 8 metadata revision GitHub Actions `checks`: completed / success
- Review time: `2026-10-05T08:54:39Z`

This is a development instruction, NOT a Phase 9 completion report. Execute only Phase 9 after successfully claiming it through the existing Phase Automation Protocol.

## 1. 当前背景

FlowForge 现在已经拥有完整的核心可靠性路径：

- PostgreSQL authoritative state
- durable dispatch intent + Redis Streams notification
- multiple workers + bounded concurrency
- heartbeat / lease / fencing / crash recovery
- retry budget / RETRYING / exponential backoff / jitter
- submission idempotency
- priority / attempt timeout / cancellation / DLQ redrive
- delayed jobs / capability-aware scheduling / fixed-interval recurring schedules
- React + TypeScript Dashboard
- bounded WebSocket realtime hints + reconnect snapshot resync

Phase 8 明确没有伪造性能指标。当前 `internal/observability` 主要只有结构化日志，Phase 9 才正式建立：

1. Prometheus metrics；
2. Grafana dashboard；
3. OpenTelemetry tracing；
4. systematic failure injection；
5. repeatable 1 / 4 / 8 / 16 Worker benchmark。

本阶段的价值是**用证据解释系统行为**，不是增加更多业务功能。

## 2. 本阶段目标

最终必须可以真实回答：

- 当前有多少 Job 在 QUEUED / RUNNING / RETRYING / DEAD_LETTER？
- 当前 queue depth 是多少？
- Worker 数量、active_jobs、utilisation 是多少？
- Job attempt duration 的 P50 / P95 / P99 是多少？
- retry / recovery / timeout / cancel 发生了多少次？
- WebSocket client/backpressure 状态如何？
- Worker crash / Redis outage / dependency failure时系统指标如何变化？
- 1 / 4 / 8 / 16 Worker 在同一受控 workload 下 throughput、queue latency、P50/P95/P99、error rate 实际是多少？
- API -> durable Job -> dispatch -> Claim -> Attempt execution -> finalize 的关键路径能否通过 trace / job_id / attempt_number 关联？

任何“高性能”“线性扩展”“低延迟”“生产级”等结论都必须有 benchmark 证据；没有证据就明确不做该声称。

## 3. Prometheus Metrics

允许引入官方/主流、维护良好的 Prometheus Go client，例如：

`github.com/prometheus/client_golang`

不要自己实现 Prometheus exposition format。

### 3.1 Metrics endpoint

API：
- 暴露 `/metrics`，默认只绑定现有 API server 或明确的 metrics listener。
- 不要求 auth，但 README 必须明确这是 local/demo observability endpoint，不适合直接公网暴露。

Worker：
- 增加独立、轻量 metrics HTTP listener，例如默认内部 `:9091`。
- 必须支持 graceful shutdown。
- 不允许一个 scrape 阻塞 Worker business execution。
- metrics server failure不应伪造 Job execution成功。

配置示例：
- `FLOWFORGE_METRICS_ENABLED=true`
- `FLOWFORGE_METRICS_ADDR=:9091`

实际命名可根据现有 config 风格调整，但必须验证并文档化。

### 3.2 Required metrics

至少实现以下类别。最终具体 metric 名可以统一加 `flowforge_` prefix。

#### Job / attempt counters
- `jobs_submitted_total{disposition}`
  - created
  - replayed
  - conflict（如果统计）
- `job_attempts_total{outcome}`
  - succeeded
  - retryable_failed
  - permanent_failed
  - timed_out
  - cancelled
  - lease_expired
- `job_retries_total{reason}`
- `job_recoveries_total{result}`
- `job_redrives_total{result}`

#### Current global state gauges
从 PostgreSQL authoritative snapshot采集：
- `jobs_current{status}`
- `queue_depth`
- `workers_current{status}`
- `schedules_current{status}`

这些是 global current state，不应由 event counter猜测。

如果多 API replica暴露相同 global gauges：
- 文档明确 Grafana/PromQL 使用 `max` 而不是 `sum` 聚合重复的 global snapshot；
- 或实现一个明确、简单的单-authority collection策略。
不要为了这一点引入 leader election 微服务。

#### Worker process gauges
每个 Worker metrics target：
- `worker_active_jobs`
- `worker_concurrency`
- `worker_utilisation_ratio`（active / configured capacity，0..1）
- `worker_lease_renew_failures_total`
- `worker_heartbeat_failures_total`

不要把 worker_id 作为 Prometheus metric label；Prometheus target/instance labels已经提供实例区分。

#### Duration histograms
至少：
- `job_attempt_duration_seconds{outcome}`
- `http_request_duration_seconds{route,method,status_class}`
- 可选 `job_end_to_end_duration_seconds{terminal_status}`，但若实现必须明确这是从 Job created_at 到 terminal，不等同 queue latency。

Bucket 必须根据现有 SLEEP / API 规模做合理固定值，不要每个 Job动态创建。

#### Realtime
- `websocket_connections`
- `websocket_slow_client_disconnects_total`
- `realtime_events_published_total{result}`
- `realtime_events_dropped_total{reason}`

### 3.3 Cardinality rules

这是验收重点。

**禁止**将以下作为 Prometheus label：
- job_id
- worker_id
- idempotency_key
- schedule_id
- trace_id
- arbitrary Job type（除非明确 bounded/allowlisted；默认不要）
- raw error text
- capability list
- URL path中实际 UUID

HTTP metrics必须使用 route template，例如：
`/api/v1/jobs/{id}`
而不是实际 UUID path。

允许 label必须来自有限枚举：
- status
- outcome
- operation
- result
- method
- status_class

需要专门测试 cardinality / route normalization。

## 4. Grafana + Prometheus Local Observability Stack

使用 Docker Compose optional profile，例如：

`observability`

至少包含：
- Prometheus
- Grafana

允许加入 OpenTelemetry Collector（见 tracing section）。

不要加入 Loki / Tempo / Jaeger / ELK，除非有明确必要且能解释。Phase 9 默认不需要这些额外系统。

### 4.1 Prometheus discovery

Prometheus必须能 scrape：
- API metrics
- 多个 scaled Worker metrics endpoint

不要只 hardcode 一个 Worker 然后声称 multi-worker metrics。

优先尝试 Docker Compose DNS service discovery，例如 Prometheus `dns_sd_configs`，实际验证 scaled Worker可以被发现。
如果 Docker Compose DNS行为不稳定，可以采用小型 file-SD generation脚本，但：
- 必须可重复；
- 不挂 Docker socket给 Prometheus；
- 不需要 Kubernetes。

### 4.2 Grafana provisioning

提交：
- datasource provisioning
- dashboard JSON / provisioning

Dashboard至少包含：
- Jobs by status
- queue depth
- workers by status
- worker active/concurrency/utilisation
- job attempts by outcome
- retries / recoveries / redrives rate
- attempt duration P50/P95/P99
- API request rate / error rate / P95
- WebSocket connections / drops

不要展示“throughput scaling chart”除非数据来自实际 benchmark或Prometheus实验窗口并明确口径。

Grafana本地认证配置不得提交真实password。
使用 `.env.example` placeholder或local-only anonymous viewer，并明确非生产设置。

## 5. OpenTelemetry Tracing

允许引入：
- OpenTelemetry Go API / SDK
- OTLP HTTP exporter
- propagation packages

优先 OTLP/HTTP，避免为了 tracing 引入 gRPC stack，除非现有依赖已需要。

### 5.1 Trace scope

至少 instrument：
- incoming HTTP request
- Job create
- dispatcher publish
- queue receive
- Claim
- Attempt Execute
- Finalize
- retry promotion
- lease recovery
- recurring materialization
- cancel/redrive control operations

Span attributes可以包含：
- `job.id`
- `attempt.number`
- `worker.id`
- `job.status`
- bounded operation/result fields

不要包含：
- payload完整内容
- result完整内容
- idempotency key
- credentials
- raw DB error
- Redis password

### 5.2 Durable async trace correlation

必须解决 async boundary，而不是只给每个进程独立 root span。

推荐最小方案：
- append-only migration（预计 `000008`）为 Job增加内部 `traceparent` / equivalent W3C trace context字段；
- first Job create / schedule materialization 时持久化当前 valid trace context；
- idempotent replay不能改写原 Job trace context；
- Redis Job delivery protocol仍保持当前最小 Job ID协议，不要求塞完整 trace payload；
- Worker reload PostgreSQL Job后，从持久 context建立 linked/child span；
- retry / lease recovery / redrive沿用同一 Job observability context；
- 若 tracing disabled / 无 parent，则创建普通 root spans或no-op。

如果选择将 context存 job_dispatch而不是 jobs，也可以，但必须保证：
- durable Redis loss / republication后 trace correlation仍存在；
- retry / redrive / recurring semantics清楚；
- 不改变 idempotency canonical identity。

trace metadata是 observability metadata，不应返回给普通 Job API client，也不能参与 submission idempotency comparison。

### 5.3 Export config

例如：
- `FLOWFORGE_OTEL_ENABLED=false`
- `FLOWFORGE_OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318`
- `FLOWFORGE_OTEL_SAMPLE_RATIO=1.0` local demo

要求：
- disabled默认无网络export副作用；
- sample ratio验证为 0..1；
- exporter failure绝不能回滚业务transaction；
- shutdown时做bounded flush；
- exporter queue有界。

### 5.4 Local trace demo

Compose observability profile可以加入一个 OpenTelemetry Collector：
- OTLP HTTP receiver
- debug/log exporter即可

不要求 Tempo/Jaeger UI。

至少通过：
- in-memory exporter unit/integration tests
- real OTLP collector smoke

证明 span真的生成并可export。

## 6. Structured Logs + Trace Correlation

保留现有 `slog`。

关键 execution log在有 active span时加入：
- trace_id
- span_id
- job_id
- worker_id
- attempt_number
- event

不要为了给所有日志自动加trace_id做全项目大规模重构。
优先在：
- HTTP request lifecycle
- Claim
- execution start/finish
- recovery
- retry
- cancel
- schedule materialization

这些关键路径关联。

日志仍不能包含 secrets / raw driver detail / payload。

## 7. Failure Injection

Phase 9需要系统化 failure test，但不要实现可公网调用的“chaos API”。

新增例如：
- `scripts/phase9-failure.ps1`
或同等脚本集合。

所有 scenario必须使用：
- disposable database
- dedicated Redis namespace
- uniquely named containers/network
- exact cleanup
- retained development DB readonly audit

至少 scenario：

### A. Worker hard crash
1. 运行 >=2 Worker；
2. Job进入RUNNING；
3. kill -9 / Docker kill Worker A；
4. 观察 heartbeat/lease expiry；
5. Job recovery/retry/takeover；
6. final success；
7. metrics中 recovery / lease_expired变化；
8. trace/log可以关联旧Attempt和新Attempt。

### B. Redis outage
1. 提交/retain durable Job intent；
2. 临时断 Redis；
3. PostgreSQL state仍 authoritative；
4. 恢复 Redis；
5. dispatcher/republication恢复执行；
6. 指标体现 dispatch/realtime failure但不产生false Job success。

### C. Worker network partition
复用 Phase 4思路：
- 断 Worker到依赖网络；
- heartbeat/renew失败；
- lease recovery；
- stale owner fence；
- 指标/trace/log与真实状态一致。

### D. API/WebSocket realtime loss
- 暂停/断开 UI Pub/Sub；
- business state继续成功；
- WebSocket degraded/reconnect；
- snapshot resync；
- realtime drop指标变化。

PostgreSQL outage可以做短暂、受控的 smoke，但如果会让测试复杂/不稳定，可以放为 optional documented failure experiment；不要为凑场景破坏数据安全。

### Safety
- 禁止删除非本次生成 volume/database/key；
- 禁止 FLUSHDB / FLUSHALL；
- 禁止 production endpoints；
- 脚本失败也必须进入 cleanup/finally；
- failure script自身必须可重复。

## 8. Load Generator

新增一个真实、可复用 load generator。

推荐：
`cmd/loadgen`

黑盒使用 HTTP API，不直接修改数据库。

参数至少：
- API base URL
- job count
- request concurrency
- SLEEP duration_ms
- max wait
- optional idempotency behavior

它必须：
- submit Jobs
- bounded poll / read结果
- 收集 Job timestamps
- 等待 terminal outcome
- 输出 machine-readable JSON/CSV summary

至少计算：
- submit request count / error count
- completed count
- failed/dead-letter/cancelled/timed-out count
- throughput jobs/sec
- queue latency：
  - first `started_at - max(created_at, scheduled_at if later)`
  - 对本 benchmark只使用 immediate single-attempt SLEEP，避免 retry语义混淆
- end-to-end latency：
  - `finished_at - created_at`
- P50 / P95 / P99 queue latency
- P50 / P95 / P99 end-to-end latency
- wall-clock elapsed
- error rate

Percentile算法必须有unit tests，尤其：
- empty sample
- single
- small N
- sorted/unsorted
- exact quantile definition文档化

不要依赖Grafana截图作为唯一benchmark结果。

## 9. Benchmark Matrix

必须建立 repeatable benchmark harness，例如：

`scripts/phase9-benchmark.ps1`

固定变量：
- Worker concurrency `C=1`
- Worker process counts：**1 / 4 / 8 / 16**
- 相同 API / PostgreSQL / Redis configuration
- immediate SLEEP Job
- fixed duration，例如 25ms或50ms
- fixed total Job count，例如 500–2000，根据主机能力选择
- fixed request concurrency
- isolated fresh DB / stream per run
- warmup run不计结果
- 正式至少 3 repetitions / configuration（若总时间合理）

输出每个 worker count：
- throughput
- queue P50/P95/P99
- end-to-end P50/P95/P99
- API submit error rate
- terminal Job error rate
- optional CPU/memory notes if can reliably capture

### Benchmark report

提交小型文本结果：
- `docs/benchmarks/phase-9-baseline.md`
- 可附小型CSV/JSON summary

必须记录：
- OS
- CPU model / logical cores
- RAM
- Docker version（可得）
- Go version
- benchmark parameters
- exact commit
- date
- whether machine had background load
- each raw repetition或至少aggregated + variance

不要提交：
- 大型raw logs
- Prometheus TSDB
- Grafana DB
- trace dumps
- generated binaries

### Interpretation

必须基于数据写结论，例如：
- throughput从1→4提升多少
- 8/16是否继续提升
- queue P95变化
- CPU/core saturation推测必须标注为推测，若无CPU数据不能断言
- 不允许因为worker数增加就直接写“horizontal scalability proven”

可以写：
“在本机、该 workload 和参数下，从1到4 Worker throughput提升X%，8 Worker后收益下降。”

## 10. Dashboard Integration

Phase 8 Dashboard不需要重写。

可以增加一个简单 Observability区域或外链：
- Grafana URL
- Prometheus status
- tracing enabled/disabled

但不要让 React Dashboard直接成为Prometheus query client。
Grafana负责metrics visualization。

Phase 8中明确“Metrics planned for Phase 9”的placeholder应更新为真实：
- Grafana observability可用（当profile启动）
- Dashboard本身仍显示PostgreSQL current snapshot
- P50/P95/P99在Grafana，不伪造进原Summary API

## 11. Testing

### Metrics
至少：
- metric registration
- counter/histogram update
- bounded label set
- HTTP route template normalization
- global snapshot collector zero/nonzero/error
- scrape DB error不panic
- duplicate metric registration prevention
- Worker metrics shutdown

### Tracing
至少：
- disabled no-op
- sampler config
- propagator parse/reject malformed traceparent
- persisted trace context roundtrip
- idempotent replay不覆盖context
- async Worker span linked/parented to persisted context
- exporter failure不影响business state
- bounded shutdown flush
- in-memory exporter span assertions

### Failure injection
脚本parser / cleanup helper tests（能做则做）以及真实scenario smoke。

### Load generator
- percentile
- result classification
- timeout/cancel
- bounded polling
- JSON output
- HTTP error handling

### Regressions
至少：
- Phase 8 realtime tests
- Phase 7 schedule/capability
- retry/lease/cancel critical paths
- state validator

### Final commands
建议：
- `go test -count=1 ./...`
- `go test -race -count=1 ./internal/observability ./internal/service/... ./internal/infrastructure/... ./internal/transport/... ./tests/integration`
- `go vet ./...`
- `go build ./...`
- frontend `npm ci`, typecheck, tests, build（如果改到frontend）
- Prometheus config validation
- Grafana provisioning JSON validation
- OTel collector config validation
- phase-state validator
- `git diff --check`

Heavy benchmark不要放CI；CI只测试loadgen逻辑和configs。

## 12. Observability Smoke

至少启动：
- PostgreSQL
- Redis
- API
- >=2 Workers
- Prometheus
- Grafana
- OTel Collector（若enabled）

验证：
- Prometheus target API + 多Worker均UP
- `/metrics`无secret
- 实际Job执行后counter/histogram变化
- Grafana datasource健康 + dashboard可加载
- Worker crash后recovery metrics变化
- WebSocket connection metric变化
- OTLP Collector收到trace
- trace中有Job/Attempt correlation
- business transaction在OTLP/Prometheus不可用时仍保持原正确语义

不要把Grafana UI可打开等同所有metrics正确；必须读取Prometheus query或metrics endpoint验证。

## 13. Security / Reliability

- `/metrics`、Grafana、Prometheus、OTLP collector均为local/demo；
- 默认只绑定Compose/internal或loopback，避免公网暴露；
- 不提交Grafana真实admin密码；
- trace/log/metric禁止payload/secrets/idempotency key；
- metric label cardinality必须受控；
- exporter/scrape失败不能影响业务commit；
- telemetry queue/buffer必须有界；
- OTel shutdown有timeout；
- Prometheus/Grafana data volume不能进入Git。

## 14. Retained Development DB Limitation

继续遵守 Phase 5–8真实限制：
- retained development DB仍schema 4；
- 有一个legacy duplicate non-null idempotency-key group；
- 000005正确阻止升级。

Phase 9如果新增 `000008` trace metadata migration：
- 不得自动fix retained data；
- 不得绕过000005；
- 不声称retained DB升级到8；
- acceptance / benchmark / failure tests全部使用isolated compatible DB；
- readonly audit确认retained DB未改变。

## 15. Out of Scope

不要实现：
- Phase 10 cloud deployment
- Kubernetes
- Terraform
- production auth
- Loki / ELK
- Tempo / Jaeger（默认不需要）
- autoscaling
- alertmanager/paging系统（可以文档planned）
- quota/admission control
- new Job business types
- arbitrary Docker execution
- cron parser
- priority aging
- rewriting Phase 8 Dashboard architecture

## 16. Acceptance Criteria

Phase 9只有全部满足才可COMPLETED：

- [ ] Prometheus metrics endpoint存在
- [ ] API + scaled Worker metrics可被Prometheus scrape
- [ ] global DB-backed current state gauges口径正确
- [ ] metrics cardinality有明确限制和测试
- [ ] attempt duration histogram + P50/P95/P99 Grafana query
- [ ] worker utilisation真实可观测
- [ ] retry/recovery/timeout/cancel/realtime metrics存在
- [ ] Grafana datasource/dashboard自动provision
- [ ] OTel tracing真实生成
- [ ] async Job execution能与durable trace context关联
- [ ] telemetry exporter failure不影响业务correctness
- [ ] structured key logs有trace/job/attempt correlation
- [ ] failure injection至少Worker crash / Redis outage / network partition / realtime loss PASS
- [ ] load generator可重复运行
- [ ] 1/4/8/16 Worker benchmark全部有真实结果
- [ ] docs/benchmarks/phase-9-baseline.md包含环境/参数/结果/限制
- [ ] 没有无证据的scalability/performance声明
- [ ] Phase 8 Dashboard/realtime regression无破坏
- [ ] retained legacy DB未被修改
- [ ] README/docs/ADR同步
- [ ] `docs/reports/phase-9-report.md`完整
- [ ] Git push/tag evidence完整
- [ ] completed state只在全部gates通过后发布
- [ ] 未提前实现Phase 10 deployment

## 17. Documentation

至少更新：
- README.md
- docs/architecture.md
- docs/dashboard.md
- docs/worker-operations.md
- docs/development-roadmap.md
- docs/reports/index.md
- 新 `docs/observability.md`
- 新 `docs/benchmarks/phase-9-baseline.md`
- ADR：metrics cardinality / tracing context / telemetry failure isolation
- Prometheus/Grafana/OTel config README或注释
- `docs/reports/phase-9-report.md`

必须明确：
- metric definitions
- queue_depth口径
- percentile口径
- global gauge multi-API aggregation规则
- tracing propagation策略
- telemetry loss不影响business truth
- benchmark只适用于记录的机器/workload
- no production SLA
- retained DB limitation
- Phase 10仍Planned

## 18. Phase Automation Protocol

领取本Prompt前先发布ownership claim：
- `current_phase = 9`
- `status = in_progress`
- `prompt_source = automation`
- `prompt_path = automation/prompts/phase-9.md`
- `last_processed_phase = 8`
- `report = null`
- `commit = null`
- `tag = null`
- `next_prompt = null`

先push + reread，再开始业务开发。

Codex不得：
- 自行生成Phase 10 Prompt
- 自行推进`last_processed_phase = 9`
- 自动merge main
- 自动修复retained legacy DB

完成后生成`docs/reports/phase-9-report.md`，必须包含：
- metrics inventory
- tracing evidence
- failure injection结果
- benchmark matrix
- exact test commands
- NOT RUN项目
- Known Limitations
- Git evidence

## 19. Git / GitHub Safety

- 继续使用shared branch，除非明确迁移。
- 禁止force push / force-with-lease / reset --hard / history rewrite。
- 历史migrations/reports/prompts/tags不改写。
- 不提交.env、credentials、Grafana password、Prometheus TSDB、Grafana DB、trace dump、node_modules、dist、binaries、large benchmark logs。
- main不自动merge。
- 若沿用tag，创建annotated tag，例如`phase9-observability-benchmarks`并验证target checkpoint。

## 20. Codex 对话日志

开始：
`FlowForge Phase Start Log`
包含Phase 9、prompt path、execution id、branch、previous checkpoint、status IN_PROGRESS。

完成：
`FlowForge Phase Completion Log`
包含metrics/tracing/failure-injection/benchmark摘要、1/4/8/16关键数字、tests、report、checkpoint/tag/push、state、Known Limitations、Waiting For external review。

阻塞：
`FlowForge Phase Blocked Log`
明确failure step、原因、测试/Git/state、partial commit和最小恢复动作。

核心原则：
**Phase 9的价值是把“FlowForge看起来能工作”升级为“FlowForge在故障和负载下的行为可以被测量、追踪、复现，并用真实数据解释”。**
