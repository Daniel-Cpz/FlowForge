# FlowForge Phase 7 — Scheduled Jobs + Capability-Aware Scheduling

## Handoff metadata
- Repository: `Daniel-Cpz/FlowForge`
- Shared entry/work branch: `codex/phase1-api-correctness`
- Prompt source: `automation`
- Prompt path: `automation/prompts/phase-7.md`
- Previous phase: Phase 6, COMPLETED
- Reviewed branch revision: `e4624eebda504061c383a08ef1f86f1050b4470b`
- Phase 6 validated implementation/report checkpoint: `3673d56dbf466241225ebe383d47c10d8b60d246`
- Phase 6 annotated completion tag: `phase6-control-dlq-v2`, verified to target the checkpoint
- Phase 6 metadata revision GitHub Actions `checks`: completed / success
- Review time: `2026-10-05T06:37:26Z`

This is a development instruction, NOT a Phase 7 completion report. Execute only Phase 7 after successfully claiming it through the existing Phase Automation Protocol.

## 1. 当前背景

FlowForge 已真实实现：
- PostgreSQL authoritative state
- durable dispatch intent + Redis Streams notification
- multiple workers + bounded concurrency
- DB-time heartbeat / leases / stale-owner fencing / crash recovery
- retry budget + RETRYING schedule + exponential backoff/jitter
- submission idempotency
- PostgreSQL-authoritative non-preemptive priority scheduling
- per-attempt execution timeout
- durable user cancellation
- DEAD_LETTER list / attempt inspection / manual redrive

Phase 6 明确保留的下一阶段能力是：
1. delayed / scheduled jobs；
2. recurring jobs；
3. capability-aware scheduling。

本阶段只实现这些调度能力。不要提前做 Phase 8 React/WebSocket，也不要为了“分布式调度”引入 Kafka、RabbitMQ、Kubernetes 或独立微服务。

## 2. 本阶段目标

实现两个互相兼容的调度维度：

### 时间资格
Job 在指定时间之前不可被发布/Claim；到期后进入现有 priority + retry + lease 执行路径。

### 能力资格
Job 可声明所需 capability；只有声明拥有全部需求 capability 的 Worker 才能 Claim。

并实现最小、可靠的 recurring schedule：
- 一个 schedule 定义 Job template + fixed interval；
- 多个 scheduler loop 并发时，同一个逻辑 occurrence 最多只创建一个 Job；
- scheduler crash / DB retry 不得重复 materialize occurrence；
- 不声称 recurring execution exactly once。

## 3. Scope

### 3.1 One-shot delayed jobs

扩展现有 `POST /api/v1/jobs`，新增可选字段，例如：

`scheduled_at`: RFC3339 timestamp / null

语义：
- null / missing：与当前行为一致，可立即参与调度。
- future：Job 可以创建为 QUEUED，但在 PostgreSQL 权威时间达到 scheduled_at 前：
  - dispatcher 不发布；
  - Claim 不成功；
  - 不创建 Attempt；
  - 不消耗 attempt budget。
- past / now：立即 eligible；不要因客户端轻微时钟差返回错误。
- 存储统一 UTC/TIMESTAMPTZ。
- schedule eligibility 由 PostgreSQL time 判定，不能以 API/Worker 本机墙钟作为 authoritative truth。
- delayed Job cancellation / idempotent replay / DLQ redrive / retry 必须保持现有语义。

现有 keyed submission idempotency 的 canonical request identity 必须加入 scheduled_at 的规范化值；相同 key 但不同 scheduled_at 应 conflict。

### 3.2 Job required capabilities

扩展 Job create envelope，新增可选：

`required_capabilities`: string[]

要求：
- missing/null 或 []：无 capability 限制。
- canonical set：去重、稳定排序后持久化；API contract 明确 normalization。
- 推荐 token 规则：trim + lowercase ASCII，允许 `[a-z0-9][a-z0-9._-]*`，单项/数量有明确上限（例如每项 1..32，最多 16）；实现前根据现有 validator 风格定稿并文档化。
- duplicate values 可以 canonical dedup 或直接 400，二选一并保持测试/文档一致；推荐 canonical dedup。
- 空字符串、控制字符、超长 token 拒绝。
- keyed submission idempotency identity 必须包含 canonical required_capabilities；相同 key + 不同 requirement 必须 409。

SLEEP 默认不要求任何 capability。

### 3.3 Worker capability declaration

增加显式 Worker 配置，例如：

`FLOWFORGE_WORKER_CAPABILITIES=cpu,ffmpeg`

要求：
- 使用与 Job requirement 相同的 normalization/token rules。
- 空配置代表空 capability set。
- 一个 worker_id 生命周期内 capability 集合固定；heartbeat 不应悄悄改变它。
- 配置改变通过进程重启 / 新 worker UUID 生效，符合现有 worker identity 模型。
- worker registry 持久化 capabilities。
- 日志/operations 文档展示 worker capability set，但不要泄露其他敏感配置。

### 3.4 Capability-aware Claim

PostgreSQL Claim 必须同时满足：
- Job 时间已 eligible；
- Worker live / lease policy 有效；
- Worker capability set 是 Job required_capabilities 的 superset；
- Job 仍满足 Phase 6 priority policy；
- attempt budget 尚有剩余。

**优先级比较必须只在“对当前 Worker 实际 eligible 的 QUEUED Jobs”中进行。**
例如：
- 高 priority GPU Job；
- 低 priority CPU-only Job；
- 当前 Worker 只有 CPU。

CPU Worker 不得因为无法运行的 GPU Job 而永久阻塞低 priority CPU Job。

反过来，GPU Worker 同时满足两者时仍按 priority policy 选择。

不要仅在 application memory 做 capability check；PostgreSQL Claim 是最终 authoritative fence。

### 3.5 Dispatcher eligibility

现有 durable dispatch intent 保留。

PendingDispatch 至少过滤：
- `status=QUEUED`
- scheduled_at is null or due

并继续 priority-first deterministic ordering。

全局 Redis stream 可以继续使用；本阶段不要求 capability-specific streams。

若 incapable Worker 收到某个 delivery：
- Claim 必须拒绝且不创建 Attempt；
- 不得 tight-loop；
- 应采用现有 notification/defer/reconciliation 机制，使 capable Worker 后续仍能收到；
- 不得让 Redis consumer ownership 把 Job 永久困在 incapable Worker。
- 若没有任何 capable Worker 在线，Job 保持 durable QUEUED，而不是 FAILED/DEAD_LETTER。

如现有 ACK/defer 策略需要最小调整以避免 30s 能力错配延迟，可调整，但不要引入复杂 routing topology。

### 3.6 Recurring fixed-interval schedules

本阶段实现 **fixed interval recurring schedule**，不要求 cron parser。

建议最小 API：

`POST /api/v1/schedules`
`GET /api/v1/schedules/{id}`
`POST /api/v1/schedules/{id}/cancel`

可选增加 bounded list endpoint，但不要为了 UI 需求扩 scope。

Schedule template 至少包含：
- id
- status: ACTIVE / CANCELLED
- type
- payload
- priority
- max_attempts
- timeout
- required_capabilities
- interval_seconds
- next_run_at
- created_at / updated_at

建议 interval 有明确范围，例如 1 second .. 7 days；可根据实际测试/演示需求选择合理上限并文档化。

不要把 Job submission `idempotency_key` 直接复用为 schedule occurrence 去重机制。

### 3.7 Recurring occurrence identity

必须有数据库级 duplicate prevention。

推荐：
- 新表 `job_schedules`
- Job 增加 nullable `schedule_id` / `scheduled_for`
- unique constraint / unique index on `(schedule_id, scheduled_for)` for schedule-generated Jobs

或者等价的独立 occurrence 表。

目标：
多个 scheduler loop 同时看到同一个 due schedule 时，数据库最多只创建一个该 scheduled_for 的逻辑 Job。

schedule occurrence Job 后续仍走普通：
Job -> dispatch intent -> Redis -> Claim -> Attempt -> retry/lease/DLQ。

不要建立第二套 execution engine。

### 3.8 Distributed scheduler coordination

每个 Worker process 可以运行一个 bounded schedule promoter/materializer loop，类似现有 recovery/retry maintenance loop；不需要增加独立 Scheduler 微服务。

要求：
- PostgreSQL DB-time authoritative。
- bounded batch（例如 <=100）。
- `FOR UPDATE SKIP LOCKED` 或等价 transactional coordination。
- materialize Job + dispatch intent + occurrence uniqueness + advance schedule next_run_at 必须有明确事务边界。
- scheduler crash before commit：全部 rollback。
- commit 成功后 crash：next_run_at/occurrence 已 durable，后续不得重复同 occurrence。
- 多进程同时 scheduler：只有一个 transaction 对某 schedule occurrence 生效。

### 3.9 Missed interval policy

必须明确 downtime 后 missed recurring occurrences 的语义，避免 catch-up storm。

推荐本阶段采用：

**coalesced catch-up / skip missed intervals**
- 一个 schedule 发现 next_run_at 已落后多个 interval 时，本轮最多 materialize 一个 oldest due occurrence；
- 随后把 next_run_at 推进到第一个严格晚于当前 DB time 的 interval boundary；
- 中间错过的 interval 不补发；
- 文档明确“不会补齐所有 missed runs”。

这比无界补发更符合当前项目的 backpressure/correctness优先原则。

如果选择其他策略，必须：
- 有严格每轮上限；
- 多 scheduler 并发安全；
- 不产生 unbounded catch-up storm；
- 在 ADR 中解释。

### 3.10 Schedule cancellation

cancel schedule：
- 只禁止未来尚未 materialize 的 occurrences；
- 已创建 Job 不自动取消；
- 不修改已存在 Job 的 idempotency / retry / DLQ 状态。
- repeated cancel idempotent。
- ACTIVE -> CANCELLED 用条件更新。
- canceled schedule 不再被 materializer 扫描。

本阶段不要求 pause/resume/edit schedule；避免扩大状态机。

## 4. Data / Migration

使用 append-only migration（例如 000007），不得修改 000001–000006。

根据最终设计至少评估：

### jobs
- `scheduled_at TIMESTAMPTZ NULL`
- `required_capabilities TEXT[] NOT NULL DEFAULT '{}'`
- nullable `schedule_id UUID`
- nullable `scheduled_for TIMESTAMPTZ`
- due/priority/capability eligibility 所需 indexes
- occurrence uniqueness

### workers
- `capabilities TEXT[] NOT NULL DEFAULT '{}'`

### job_schedules
新 schedule template 表及：
- status membership
- interval bounds
- next_run_at
- capability/template data
- indexes for ACTIVE + next_run_at

如果 PostgreSQL array containment 用于 capability fence，要求：
- canonical storage；
- null/empty semantics 明确；
- SQL tests 验证 subset/superset；
- 不因 GIN/array trick 放松 correctness。

历史 SQL 不改写。

### Retained development DB limitation

Phase 5/6 已记录 retained development DB 仍有一个 legacy duplicate idempotency-key group，schema 仍停在 Phase 4，因为 000005 正确拒绝升级。

Phase 7 必须继续：
- 不自动删除/合并/改 key；
- 不绕过 000005；
- 不声称 retained development DB 已升级；
- 使用 isolated compatible DB/schema 做 Phase 7 implementation/integration/smoke；
- 只有用户另行明确授权数据修复时才能处理 retained historical data。

## 5. Existing semantics integration

### Priority
scheduled/capability eligibility 必须进入 priority candidate set。
不可执行/未到期 Job 不应挡住当前 Worker 的 lower priority eligible Job。

### Retry
retry promotion 后 Job 重新进入普通 QUEUED eligibility。
initial scheduled_at 已在过去，不应阻挡 retry。
不要用 scheduled_at 代替 retry_at。

### Timeout
scheduled wait 不计入 attempt execution timeout。

### Cancellation
future delayed Job 在 due 前可正常 cancel。
schedule cancellation 与 Job cancellation 是不同概念。

### Idempotency
one-shot Job canonical request identity 加入 scheduled_at + required_capabilities。
schedule occurrence uniqueness 使用 schedule occurrence identity，不依赖用户 idempotency_key。

### Manual redrive
redriven Job 保持原 capabilities / priority；scheduled_at 如果已过去可立即 eligible。
不要改变 schedule template。

## 6. Failure / Edge Cases

至少覆盖：

### Delayed Job
- scheduled_at future，dispatcher/Claim before due
- exactly around due boundary using PostgreSQL time
- scheduled_at past
- cancel before due
- same idempotency key same normalized scheduled_at replay
- same key different scheduled_at conflict

### Capability
- CPU worker vs GPU-only Job
- GPU+CPU worker satisfies CPU-only and GPU Job
- high-priority incompatible Job must not block lower-priority compatible Job
- worker with duplicate/malformed capability config
- Job duplicate/malformed required capabilities
- no capable workers online: remains QUEUED
- capable worker appears later and successfully executes
- stale/OFFLINE capable worker cannot Claim
- worker restart with changed capabilities gets new identity
- multiple workers with overlapping capability sets race safely

### Recurring
- two scheduler loops race same due schedule
- process crash before scheduler transaction commit
- process crash after commit
- schedule cancel races materialization
- schedule due while Redis unavailable
- duplicate notification still one occurrence Job / normal Attempt rules
- downtime spanning many intervals respects chosen missed-run policy
- repeated cancel
- canceled schedule never produces future occurrences
- already materialized Job remains unaffected by schedule cancel
- recurring Job retry does not create another occurrence
- same schedule occurrence cannot materialize twice after process restart

### Interactions
- scheduled high-priority GPU Job becomes due while CPU jobs queued
- capability Job times out/retries and remains capability constrained
- capability Job DEAD_LETTER + manual redrive remains constrained
- schedule-generated Job user cancellation does not cancel parent schedule

## 7. Minimal Necessary Tests

优先 affected modules。至少：

- capability parser/canonicalization unit tests
- scheduled_at HTTP/parser/canonical idempotency tests
- PostgreSQL Claim capability + due-time + priority tests
- PendingDispatch due-time ordering tests
- worker registration capability tests
- multi-worker overlapping capability integration tests
- no-capable-worker then capable-worker-arrives test
- schedule repository/materializer transaction tests
- concurrent scheduler duplicate-prevention tests
- missed interval policy tests
- schedule cancel race tests
- migration up/down + historical compatibility tests
- existing priority/retry/lease/cancel/idempotency regression
- race detector on new scheduler/concurrency code
- phase-state validator
- `go vet ./...`
- `go build ./...`
- `git diff --check`

建议最终：

`go test -race -count=1 ./internal/service/... ./internal/infrastructure/... ./tests/integration`

若成本合理：

`go test -count=1 ./...`

NOT RUN / SKIP 必须如实记录。

## 8. Real-service smoke

使用 disposable PostgreSQL DB + Redis namespace，不修改 retained development DB。

至少演示：

### Delayed
1. 创建 scheduled_at = DB/real future ~3–5s 的 SLEEP；
2. due 前无 Attempt；
3. due 后执行成功。

### Capability
1. 启动 Worker A capabilities=cpu；
2. 提交 GPU-only Job，保持 QUEUED/no Attempt；
3. 同时提交较低 priority CPU Job，CPU Worker 可以执行，不被 incompatible GPU Job阻塞；
4. 启动 Worker B capabilities=gpu；
5. GPU Job 被 Worker B Claim 并成功。

### Recurring
1. 创建 interval schedule；
2. 至少观察两个 materialized occurrences；
3. 多 worker/scheduler 并发下每个 scheduled_for 只有一个 Job；
4. cancel schedule；
5. 确认之后不再产生 occurrence；
6. 已 materialize Jobs 状态不被 cancel schedule 改写。

Smoke 结束必须：
- 停止生成进程；
- 删除仅本次生成 DB/key/container；
- 恢复正常服务；
- readonly audit 确认 retained legacy duplicate-key data 未改变。

## 9. Acceptance Criteria

Phase 7 只有全部满足才可 COMPLETED：

- [ ] one-shot delayed Job 使用 PostgreSQL time eligibility
- [ ] due 前 dispatcher/Claim 不执行
- [ ] Job required_capabilities 有 canonical contract
- [ ] Worker capabilities 持久化并固定于 worker identity
- [ ] PostgreSQL Claim 执行 capability superset fence
- [ ] incompatible higher-priority Job 不阻塞 compatible lower-priority Job
- [ ] no capable worker 时 durable QUEUED，不误失败
- [ ] recurring fixed-interval schedule API 与 durable model
- [ ] multi-scheduler duplicate occurrence prevention
- [ ] missed interval policy bounded 且文档化
- [ ] schedule cancellation 不影响已 materialize Job
- [ ] schedule occurrence 复用普通 Job/dispatch/retry/lease pipeline
- [ ] Phase 6 priority/timeout/cancel/DLQ regression 无破坏
- [ ] Phase 5 idempotency identity 正确扩展 scheduled_at/capabilities
- [ ] retained legacy DB 未被静默修复/升级
- [ ] README/architecture/lifecycle/worker operations/roadmap/ADR 同步
- [ ] `docs/reports/phase-7-report.md` 完整
- [ ] Git 安全检查、push/tag evidence 完整
- [ ] completed state 只在全部 gates 通过后发布
- [ ] 未提前实现 Phase 8 dashboard/WebSocket

## 10. README / Documentation

至少更新：
- README.md
- docs/architecture.md
- docs/job-lifecycle.md
- docs/worker-operations.md
- docs/development-roadmap.md
- docs/reports/index.md
- 新 ADR：time eligibility + recurring scheduler coordination + capability matching
- docs/reports/phase-7-report.md

明确：
- PostgreSQL 是时间/能力调度 truth；
- Redis 不是 scheduling authority；
- recurring 是 fixed interval，不是 cron（本阶段）；
- missed runs 的具体策略；
- schedule cancel 不 cancel existing Jobs；
- capability token normalization；
- no capable worker = backlog，不是 failure；
- priority 只比较当前 Worker eligible Jobs；
- delayed wait 不算 execution timeout；
- at-least-once / idempotency / external side-effect boundary 不变；
- retained development DB upgrade limitation；
- Phase 8 Dashboard/WebSocket 仍 Planned。

## 11. Phase Automation Protocol

领取本 Prompt 时必须先发布 ownership claim：
- `current_phase = 7`
- `status = in_progress`
- `prompt_source = automation`
- `prompt_path = automation/prompts/phase-7.md`
- `last_processed_phase = 6`
- `report = null`
- `commit = null`
- `tag = null`
- `next_prompt = null`

先 push + reread，再开始业务开发。

Codex 不得：
- 自行生成 Phase 8 Prompt
- 自行推进 `last_processed_phase = 7`
- 自动 merge main
- 自动修复 retained legacy duplicate-key data

完成后生成 `docs/reports/phase-7-report.md`，只记录真实执行的 tests/smoke/Git evidence。

## 12. Git / GitHub Safety

- 继续使用协议指定共享入口分支，除非用户/协议明确迁移。
- 禁止 force push / force-with-lease / reset --hard / history rewrite。
- 历史 migration/report/prompt/tag 不改写。
- 不提交 .env、credentials、tokens、private keys、binaries、temp DB dump、smoke artifact。
- main 不自动 merge。
- 若沿用阶段 tag，创建 annotated tag，例如 `phase7-scheduling-capabilities`，验证它指向 implementation/report checkpoint。
- retained development data 只有用户明确授权的数据修复任务才能修改。

## 13. Codex 对话日志

开始：
`FlowForge Phase Start Log`
包含 Phase 7、prompt path、execution id、branch、previous checkpoint、status IN_PROGRESS。

完成：
`FlowForge Phase Completion Log`
包含 delayed/capability/recurring 实现摘要、真实 tests/smoke、report、checkpoint/tag/push、state、Known Limitations、Waiting For external review。

阻塞：
`FlowForge Phase Blocked Log`
明确 failure step、原因、测试/Git/state、是否有 partial commit，以及最小恢复动作。

核心原则：
**Phase 7 不是“加一个时间字段和字符串数组”，而是要让 time eligibility、priority、capability matching、recurring occurrence dedupe 与现有 retry/lease/cancel/DLQ 在 PostgreSQL 权威状态下正确组合。**
