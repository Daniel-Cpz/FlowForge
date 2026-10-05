# FlowForge Phase 5 — Retry + Backoff + Jitter + Idempotency

## Handoff metadata
- Repository: `Daniel-Cpz/FlowForge`
- Shared entry/work branch: `codex/phase1-api-correctness`
- Prompt source: `automation`
- Prompt path: `automation/prompts/phase-5.md`
- Previous phase: Phase 4, COMPLETED
- Reviewed branch revision: `d0aa5c2b1959f3d791f70236cf57997c5f471049`
- Phase 4 implementation checkpoint: `40f1250ac64f12bcb51e799a8cbdce8cc45d6f00`
- Phase 4 annotated tag: `phase4-lease-recovery`, verified to target that checkpoint
- Phase 4 metadata revision GitHub Actions `checks`: completed / success
- Review time: `2026-10-05T04:48:29Z`

This is a development instruction, NOT a Phase 5 completion report. Execute only Phase 5 after successfully claiming it through the existing Phase Automation Protocol.

## 1. 当前背景

FlowForge 已真实实现：
- PostgreSQL authoritative state
- durable dispatch intent + Redis Streams notification
- multiple worker processes + bounded per-process concurrency
- atomic QUEUED -> RUNNING claim and independent Job Attempts
- DB-time heartbeat / worker liveness
- DB-time execution leases + renew
- stale-owner fencing
- bounded concurrent lease reapers
- RUNNING crash recovery / requeue
- abrupt worker crash -> another worker takeover
- at-least-once delivery with explicit non-Exactly-Once semantics

Phase 4 completion evidence shows SIGKILL, paused stale owner, network loss and graceful SIGTERM cases; old expired attempts remain in history and a new owner can complete a later attempt.

当前核心缺口是：
1. business/operational failures 没有统一 retry policy；
2. `max_attempts` 尚未成为所有 execution attempts 的明确总预算；
3. retry 没有 durable backoff/jitter schedule；
4. 已存在的 `idempotency_key` 只是 metadata，POST 重复提交仍会创建多个 Job；
5. submission idempotency 仍未解决业务副作用的 exactly-once 问题。

本阶段只解决 **Retry + exponential backoff + jitter + submission idempotency**。不要顺手进入 Phase 6 priority/timeout/cancellation/DLQ management。

## 2. 本阶段目标

建立一套持久化、可并发安全恢复的 retry 机制，并把 `max_attempts` 定义为真实的执行尝试上限。

目标生命周期：

`RUNNING -> FAILED -> RETRYING -> QUEUED -> RUNNING`

如果当前失败不可重试，或总 attempt 预算已耗尽：

`RUNNING -> FAILED -> DEAD_LETTER`

重要：可以在一个数据库事务中依次应用 domain transition 并最终持久化为 RETRYING 或 DEAD_LETTER；不要通过非法直接跳转绕过状态机语义。

同时为创建 API 加入真正的 submission idempotency：

相同非空 idempotency key + 相同 canonical request
→ 返回同一个 Job，不创建第二个 Job / dispatch intent。

相同 key + 不同 request
→ 明确冲突，不静默返回错误 Job，也不创建新 Job。

无 idempotency key
→ 保持现有“每次请求创建一个新 Job”的行为。

## 3. Scope

### 3.1 统一 attempt budget
- 明确定义：`max_attempts` 是一个 Job 允许创建的 **总 execution attempt 数**。
- Claim 成功创建 Attempt 时才递增 `attempt_count`。
- duplicate delivery / failed pre-claim / queue receive 不创建 Attempt、不消耗预算。
- lease_expired crash attempt、graceful execution_cancelled attempt、retryable business attempt 都已经是真实 execution attempt，因此计入总预算。
- Phase 4 “crash recovery 可以临时超过 max_attempts”的过渡行为必须在本阶段收口：达到预算后不得无限 crash/retry。
- Attempt number 必须继续单调且历史不可覆盖。

### 3.2 Failure classification
引入最小、清晰的 failure classification：
- retryable/transient
- permanent/non-retryable

不要把 raw error string 当 policy。

已有 deterministic failures：
- unsupported job type
- invalid SLEEP payload
应保持 permanent/non-retryable。

worker process shutdown 产生的 `execution_cancelled` 是 operational interruption，不是用户 cancellation；在本阶段可作为 retryable failure 处理，但必须遵守 max_attempts。

lease_expired recovery 也必须经过相同 attempt budget 判定。

Executor panic/uncertain lease-renew path 仍由 lease recovery 收口；不要用本阶段 retry 逻辑绕过 fencing。

### 3.3 Durable retry schedule
为 Job 增加最小必要的 durable retry schedule，例如 `retry_at TIMESTAMPTZ NULL`。

建议：
- RETRYING 必须有 retry_at。
- QUEUED/RUNNING/terminal 状态 retry_at 必须为 NULL。
- 权威时间使用 PostgreSQL time；不要使用 worker 本机时间判断“是否到期”。
- retry scheduler/promoter 扫描 due RETRYING rows 时使用 bounded batch + `FOR UPDATE SKIP LOCKED` 或等价条件更新。
- due transition 必须把 RETRYING -> QUEUED 与 dispatch intent reset/insert 放在同一事务。
- 多个 worker/process 同时运行 retry promoter 时只能有一个有效 transition。

### 3.4 Exponential backoff + jitter
实现显式、可测试的 backoff policy。

推荐默认：
- base delay: 1s
- max delay: 30s
- equal jitter：先计算 `cap = min(maxDelay, base * 2^(attempt_number-1))`，实际 delay 在 `[cap/2, cap]` 内均匀分布。
- 使用 saturating arithmetic，防止大 attempt number 溢出。
- jitter source 必须可注入/可确定测试，生产实现并发安全。
- 不允许 tight retry loop。
- 配置必须有合理范围与启动时验证。

若你选择 full jitter 或其他成熟公式，必须在 ADR 中说明理由，并满足：
- 有非零有效最小等待；
- 有上限；
- 可测试；
- 不造成 retry storm；
- 不因 attempt number 溢出。

### 3.5 Retry state persistence
retryable failure 且仍有预算：
- 当前 Attempt -> FAILED，记录稳定 error/reason/result
- Job 通过合法 transition 进入 RETRYING
- 清除当前 execution ownership / lease
- 设置 retry_at
- 不立即重新 publish
- 当前 delivery 只在 retry 状态事务成功后 ACK

due retry promoter：
- RETRYING -> QUEUED
- retry_at -> NULL
- durable dispatch intent reset/insert
- 后续 dispatcher 负责 Redis publication

如果 retry scheduling transaction 失败：
- 不 ACK 当前 delivery
- 不伪造 RETRYING 成功
- 维持数据库事实并走现有 fail-fast/recovery boundary

### 3.6 Exhaustion / DEAD_LETTER
当：
- failure permanent；或
- 当前 attempt_count 已达到 max_attempts

则当前 Attempt 记录 FAILED，Job 通过：
`RUNNING -> FAILED -> DEAD_LETTER`

进入 DEAD_LETTER。

Phase 5 只实现可靠状态与证据，不实现 Phase 6 的 DLQ UI/inspect/manual retry management。

对 lease_expired：
- 若仍有预算：recovery 最终进入 RETRYING，并由 retry schedule 重新 QUEUE；
- 若预算耗尽：旧 attempt 记录 lease_expired，Job 进入 DEAD_LETTER，不再 requeue。

### 3.7 Submission idempotency
把现有 `idempotency_key` 从 metadata 升级为真实创建幂等键。

当前系统没有 tenant/user namespace，因此本阶段明确：
- non-null idempotency key 在整个 FlowForge API namespace 内唯一。
- key 按现有精确字符串语义比较；不要偷偷 trim/case-fold 已被 API 保留的内容。
- null key 不参与 dedup。

推荐数据库方案：
- 在 jobs.idempotency_key 上建立 partial unique index：
  `WHERE idempotency_key IS NOT NULL`
- 因 Phase 1 明确允许历史重复 key，migration 必须先检查 legacy duplicate keys。
- 如果存在 legacy duplicate non-null key：migration 必须原子失败并给出清晰诊断；禁止自动删除、合并或任意选择一个历史 Job。
- 不得修改历史 migration；使用 append-only 新 migration。

创建事务建议：
- keyed insert 使用数据库唯一性解决并发，不用“先 SELECT 再 INSERT”的竞态实现。
- first writer：创建 Job + dispatch intent，返回 created。
- conflict：读取已存在 key 对应 Job，并比较 canonical request fields。
- canonical request identity 至少包括：
  - trimmed/validated type
  - JSONB semantic payload
  - priority
  - max_attempts
  - timeout
- created_at / generated id 不属于 request identity。
- same key + same canonical request：返回原 Job，绝不新建 dispatch intent/Attempt。
- same key + different request：返回 stable idempotency conflict。
- 并发相同 key 请求必须只有一个 durable Job。

### 3.8 HTTP idempotency contract
明确并测试：
- 首次 keyed create：201 Created
- same key + same request replay：200 OK，body/Location 指向原 Job
- same key + different canonical request：409 Conflict，例如稳定 code `IDEMPOTENCY_CONFLICT`
- 无 key：继续 201，重复请求仍各自创建
- replay terminal/running/retrying Job：仍返回原 Job，不重新 dispatch

如果项目当前 handler abstraction 更适合用明确 `CreateDisposition`/equivalent 表示 created vs replayed，优先这样做，不要通过比对时间或错误字符串猜测。

### 3.9 Exactly-once boundary
README/ADR 必须明确：
**submission idempotency != exactly-once execution != exactly-once business side effect**。

本阶段 idempotency 只保证同一个 idempotency key 的 create request 不重复创建逻辑 Job。
lease/retry 仍是 at-least-once execution environment；未来带外部副作用的 Job 必须自己使用业务 idempotency/fencing。

## 4. Out of Scope
不要实现：
- priority scheduling / priority aging
- generic Job timeout enforcement
- user cancellation API
- DLQ inspect/manual retry/admin UI
- scheduled/cron jobs
- capability-aware scheduling
- WebSocket/dashboard
- Prometheus/Grafana/OpenTelemetry
- admission control/quota
- Kafka/RabbitMQ
- arbitrary shell/Docker execution
- Kubernetes/Terraform/cloud deployment

仍只需要现有 SLEEP demonstration。不要为了制造 transient business error 而加入无意义生产 Job type。
如需验证 retryable executor failure，使用受控测试 executor/fault injection，不把测试类型暴露成生产 API。

## 5. Data / Migration 要求
优先新增 append-only migration（例如 000005），根据实际 schema 最小修改。

至少考虑：
- `jobs.retry_at TIMESTAMPTZ NULL`
- partial index for due RETRYING scan
- partial unique index on non-null idempotency_key
- retry/status consistency constraints（在不重复 domain graph 的前提下可加入数据不变量）
- legacy duplicate-key preflight

不要重写 000001-000004。

更新 repository scan/validation/API serialization 时确保 old rows 兼容。

## 6. Correctness invariants
必须保持：
- only Claim creates a new Attempt / increments attempt_count
- `attempt_count <= max_attempts` after Phase 5 normalization
- no retry promoter creates an Attempt
- RETRYING must not have assigned_worker / active lease
- retry_at only belongs to RETRYING
- DEAD_LETTER is terminal
- stale owner cannot finalize after retry/recovery
- retry scheduling and attempt finalization are atomic
- RETRYING -> QUEUED + dispatch intent is atomic
- terminal Job ACK occurs only after terminal DB commit
- retryable Job ACK occurs only after durable RETRYING schedule commit
- idempotent replay never creates another Job/outbox row
- idempotency conflict never mutates existing Job
- database remains authoritative; Redis loss remains reconstructible

## 7. Failure / Edge Cases
至少覆盖：
- retryable failure on attempt 1 -> RETRYING -> due -> attempt 2 success
- max_attempts=1 failure -> DEAD_LETTER, no retry
- max_attempts=N exact exhaustion, no N+1 attempt
- lease_expired with budget remaining -> RETRYING
- lease_expired at budget exhausted -> DEAD_LETTER
- graceful execution_cancelled with budget remaining -> retry policy
- retry_at not due must not queue early
- multiple retry promoters race on same row
- PostgreSQL failure while scheduling retry
- dispatcher/Redis unavailable after retry becomes QUEUED
- process restart while Job RETRYING
- jitter lower/upper bounds and overflow saturation
- concurrent same idempotency key + same request
- same key + JSON semantically equal but textually different payload
- same key + different payload/type/options -> 409
- no key concurrent duplicate submissions -> distinct Jobs
- replay after original Job RUNNING/SUCCEEDED/DEAD_LETTER -> same Job
- migration with legacy duplicate keys fails atomically without deleting data
- idempotent create Job insert succeeds but dispatch-intent insert fails -> whole first-create transaction rolls back
- DB error on replay/conflict returns generic safe server error, not raw driver text

## 8. 最小必要测试范围
优先 affected modules；Phase 5 跨 repository / API / worker lifecycle，测试应足够覆盖核心路径。

至少：
- backoff/jitter pure unit tests
- retry classification tests
- state-transition tests
- repository retry schedule/promoter tests
- lease recovery + retry-budget integration tests
- idempotent create repository/service/HTTP contract tests
- concurrent keyed POST real PostgreSQL test
- real PostgreSQL + Redis retry flow
- race detector on affected concurrent worker/retry code
- migration up/down + legacy duplicate preflight
- phase-state validator
- `go vet`, `go build`, `git diff --check`

建议最终 affected race：
`go test -race -count=1 ./internal/service/... ./internal/infrastructure/... ./tests/integration`

若成本合理，Phase 5 最终验收运行 `go test -count=1 ./...`，但不要把未运行项目写成 PASS。

至少提供一个真实服务 smoke：
1. 创建 max_attempts >= 2 的 Job
2. 通过受控 fault injection 使第一次执行产生 retryable failure
3. 观察 RETRYING + retry_at
4. retry_at 前无新 attempt
5. due 后重新 QUEUED/dispatch
6. 新 Attempt 成功
7. Attempt 1 failure / Attempt 2 success 历史完整

另做 idempotency smoke：
- 同 key 同请求多次/并发提交 → 同一 Job ID
- 同 key 不同请求 → 409
- 无 key → 不 dedup

## 9. 验收标准
Phase 5 只有以下全部满足才能 COMPLETED：
- [ ] max_attempts 成为统一 execution attempt 总预算
- [ ] retryable/permanent 分类显式且测试
- [ ] durable RETRYING + retry_at
- [ ] bounded exponential backoff + jitter
- [ ] concurrent retry promoter 安全
- [ ] lease recovery 尊重 max_attempts
- [ ] exhaustion -> DEAD_LETTER
- [ ] first create / replay / conflict HTTP contract 明确
- [ ] concurrent idempotent create 只产生一个 Job/outbox
- [ ] legacy duplicate migration 不静默破坏数据
- [ ] at-least-once 与 submission idempotency 边界文档清晰
- [ ] README/architecture/lifecycle/worker operations/roadmap/ADR 同步
- [ ] `docs/reports/phase-5-report.md` 完整
- [ ] Git 安全检查和真实 push/tag evidence
- [ ] Automation completed state 只在所有 gates 通过后发布
- [ ] 未提前实现 Phase 6

## 10. README / 文档
至少更新：
- README
- docs/architecture.md
- docs/job-lifecycle.md
- docs/worker-operations.md
- docs/development-roadmap.md
- docs/reports/index.md
- 独立 Phase 5 ADR：retry budget、failure classification、backoff/jitter、idempotency contract/migration
- docs/reports/phase-5-report.md

明确：
- max_attempts 的新权威语义
- FAILED/RETRYING/DEAD_LETTER 的状态含义
- retry_at/backoff 配置
- idempotency replay/conflict HTTP contract
- no-key behavior
- submission idempotency 不保证业务副作用 exactly-once
- DLQ management 仍属于 Phase 6

## 11. Phase Automation Protocol
领取本 Prompt 时先发布 ownership claim：
- `current_phase = 5`
- `status = in_progress`
- `prompt_source = automation`
- `prompt_path = automation/prompts/phase-5.md`
- `last_processed_phase = 4`
- `report = null`
- `commit = null`
- `tag = null`
- `next_prompt = null`

必须先 push 并回读 claim，再开始业务开发。

Codex 不得：
- 自己生成 Phase 6 Prompt
- 自己把 last_processed_phase 推到 5
- 自动 merge main

Phase 完成后生成 `docs/reports/phase-5-report.md`，只记录真实执行的测试和 Git evidence。

## 12. Git / GitHub 安全
- 继续使用协议指定的共享入口分支，除非用户/协议明确迁移。
- 禁止 force push / force-with-lease / reset --hard / history rewrite。
- 历史 migration/report/prompt 不改写。
- 不提交 .env、credentials、tokens、private keys、临时 DB dump、生成二进制和 smoke artifacts。
- main 不自动 merge。
- 若沿用阶段 tag，使用 annotated tag（例如 `phase5-retry-idempotency`）并验证它指向 implementation/report checkpoint。

## 13. Codex 对话日志
开始：
`FlowForge Phase Start Log`
包含 Phase 5、prompt、execution id、branch、previous checkpoint、status IN_PROGRESS。

完成：
`FlowForge Phase Completion Log`
包含 retry/idempotency 实现摘要、测试/真实 smoke、report、checkpoint/tag/push、state、Known Limitations、Waiting For external review。

阻塞：
`FlowForge Phase Blocked Log`
明确失败步骤、原因、测试/Git/state、部分提交及最小恢复动作。

核心原则：
**Phase 5 的价值不是简单“失败后再跑一次”，而是把 attempt budget、durable retry schedule、storm-safe backoff/jitter 和并发安全的 submission idempotency 建成可证明、可恢复、可解释的协议。**
