# FlowForge Phase 2 — Redis Queue + Single Worker Execution

## 0. 执行身份与前置事实

你正在 GitHub 仓库 `Daniel-Cpz/FlowForge` 中执行 **Phase 2**。

Phase 1 已真实完成，权威完成证据来自：
- `automation/state.json`
- `docs/reports/phase-1-report.md`
- `README.md`
- `docs/phase-automation.md`
- `docs/development-roadmap.md`
- Phase 1 checkpoint: `6d197f25088bdfcb1aa8485590e839218eac3813`
- annotated tag: `phase1-api-correctness`

Phase 1 已实现严格 Job HTTP contract、PostgreSQL canonical persistence、cursor pagination、错误映射、真实 PostgreSQL integration tests，以及 manual / automation 两种 Phase Prompt 来源协议。

当前仍未实现：
- Redis-backed job queue
- 真正 worker execution
- 多 worker / bounded concurrency
- heartbeat / lease / crash recovery
- retry / backoff / jitter
- idempotency duplicate suppression
- priority execution policy
- timeout/cancellation policy
- DLQ / scheduling / dashboard / observability / benchmark / cloud deployment

不要把这些 Planned 功能描述成已实现。

核心原则：
**Correctness > Feature Count**
**Reliability > UI Complexity**
不得声称 Exactly-Once。
不要为了关键词引入 Kafka、RabbitMQ、Temporal、ORM、workflow engine 或其他不必要技术。

---

# 1. Phase 2 核心目标

实现第一条真实、可测试的异步执行链：

`POST Job -> PostgreSQL durable Job + durable dispatch intent -> Redis -> single worker -> SLEEP execution -> PostgreSQL terminal state/result`

Phase 2 的重点不是吞吐量，而是建立一个不会因为简单 PostgreSQL/Redis dual-write failure 而静默丢失已提交 Job 的可靠边界。

目标 delivery semantic：**at-least-once delivery**。

Redis 只负责 dispatch/delivery；PostgreSQL 始终是 Job authoritative source of truth。

---

# 2. 必须首先解决：PostgreSQL / Redis Dual-Write

禁止采用：

1. INSERT Job
2. COMMIT
3. RPUSH/XADD Redis
4. 假设二者都成功

因为 DB commit 成功而 Redis 写入失败会产生永久 stranded Job。

Phase 2 必须实现 durable database-backed dispatch/outbox，或提供具有同等 durability 的设计。

推荐：
- Job 与 dispatch/outbox row 在同一 PostgreSQL transaction 中创建；
- dispatcher 从 DB 读取 pending dispatch；
- 成功发布 Redis 后再记录 published 状态；
- Redis publish 失败时 outbox 必须仍存在；
- Redis publish 成功但 DB marker 更新失败时允许再次发布；
- duplicate Redis delivery 必须由 authoritative DB claim boundary 安全处理。

不得依赖纯内存 pending queue。

为该设计新增 ADR，并明确 failure window 与 future Phase 3/4 的扩展路径。

---

# 3. Scope

## 3.1 Durable Outbox

新增最小必要 migration 和 repository/service support。

要求：
- Job + dispatch intent atomic commit；
- 不修改已应用历史 migration；
- 新增 forward/down migration；
- dispatch record 至少能唯一关联 Job ID；
- publication 可以安全重试；
- 不宣称 exactly-once publication；
- 不构建通用 event bus abstraction。

Phase 1 的 Create validation、canonical readback 和 API error semantics 必须保持兼容。

## 3.2 Dispatcher

实现小型 dispatcher：
- bounded batch 读取 pending outbox；
- 发布到 Redis；
- Redis 成功后才标记 published；
- PostgreSQL/Redis 操作必须有 context/deadline；
- idle 时不得 busy-spin；
- 支持 cancellation/shutdown；
- structured logging 不泄露 credentials、payload secrets 或 raw driver details。

失败语义：
- DB unavailable -> 不伪造成功；
- Redis unavailable -> outbox 保持 pending；
- Redis success + marker failure -> 允许之后 duplicate publish；
- crash before publish -> pending 可恢复；
- crash after publish before marker -> duplicate 可接受。

## 3.3 Redis Queue Primitive

优先考虑 **Redis Streams + consumer group**，因为后续 Phase 3/4 需要多 worker 与 recovery。

但本 Phase 不实现 stale-message reclaim、heartbeat、lease recovery。

如果选择其他 primitive，必须在 ADR 中解释：
- message loss window；
- duplicate behavior；
- worker crash behavior；
- Phase 3/4 如何安全扩展。

Redis message 只携带重建 authoritative work 所需的最小信息，优先 Job ID + protocol version。

不得把完整 Job payload 复制进 Redis 并将其视为权威状态。

## 3.4 Single Worker

将现有 non-executing worker skeleton 改为真正执行器，但本 Phase 仅允许 **单 worker / 单次一个 Job**。

Worker：
- consume queue message；
- 解析 Job ID；
- 从 PostgreSQL reload Job；
- 使用 authoritative DB claim；
- 执行 SLEEP；
- persist terminal state/result；
- 在正确边界 ack Redis message；
- graceful shutdown。

duplicate delivery：
- terminal Job -> 不重复执行；
- RUNNING Job -> 不重复执行；
- missing Job -> worker 不崩溃，行为明确；
- malformed message -> worker 不崩溃。

禁止采用 SELECT state 后无条件 UPDATE 的 race-prone claim。
必须使用 atomic conditional transition / compare-and-set 或等价 DB boundary，为 Phase 3 保留正确基础。

## 3.5 SLEEP Demonstration Job

Phase 2 只新增一种 executable job：

`SLEEP`

建议 payload：

```json
{"duration_ms":250}
```

要求：
- duration_ms 为有界整数；
- 明确 min/max；
- malformed payload deterministic failure；
- unsupported job type deterministic behavior；
- context-aware timer；
- 不使用无法取消的裸 sleep；
- success 写入明确 result；
- status/timestamps 遵守现有 domain lifecycle。

禁止实现 shell command、arbitrary code、HTTP callback 或 plugin executor。

## 3.6 Job State / Attempts

复用现有 centralized state graph。

Phase 2 至少支持：
`QUEUED -> RUNNING -> SUCCEEDED | FAILED`

不得绕过 domain rules 任意写 status。

如使用 JobAttempt：
- 一次正常 execution 对应一次 attempt；
- duplicate delivery 在 execution 前被拒绝时不得制造额外 business attempt；
- 不实现 retry loop；
- retry/backoff 留给 Phase 5。

---

# 4. Explicit Crash Limitation

Phase 2 **不实现完整 worker crash recovery**。

若 worker 已成功 claim Job 为 RUNNING 后进程崩溃，Job 可以暂时永久停留 RUNNING，直到未来 recovery Phase 处理。

必须在 README、architecture/lifecycle docs、Phase 2 report 明确记录。

不得为了“顺手解决”而加入：
- heartbeat
- lease renewal
- stale-owner reclaim
- fencing
- distributed recovery

这些属于 Phase 4。

---

# 5. Out of Scope

Phase 2 禁止提前实现：
- multiple workers
- worker pool / bounded concurrency
- heartbeat / lease / fencing / crash recovery
- retry / exponential backoff / jitter
- idempotency_key duplicate suppression
- priority scheduler
- general execution timeout policy
- user cancellation API
- DLQ
- scheduled jobs
- capability-aware scheduling
- dashboard / WebSocket
- Prometheus / Grafana / tracing
- benchmark/throughput claims
- cloud deployment
- authentication
- arbitrary command/code execution

未来 Phase 的代码除非为当前正确抽象绝对必要，否则不要提前加入。

---

# 6. Failure / Edge Cases

至少验证：

1. Job insert failure -> 不产生 committed outbox。
2. Outbox insert failure -> Job transaction rollback。
3. DB commit 成功 + Redis unavailable -> Job/outbox 不丢失。
4. dispatcher restart -> pending outbox 可重新发布。
5. Redis publish success + DB marker failure -> duplicate publish 不造成二次执行。
6. duplicate Redis message -> DB claim boundary 阻止二次 SLEEP。
7. terminal Job duplicate -> 不执行。
8. RUNNING Job duplicate -> 不执行。
9. missing Job message -> worker 保持健康。
10. malformed Redis message -> worker 保持健康。
11. unsupported type -> deterministic outcome。
12. invalid SLEEP payload -> deterministic FAILED/non-execution outcome。
13. graceful shutdown while idle。
14. graceful shutdown during SLEEP。
15. PostgreSQL claim/finalize failure -> 不伪造 success。
16. temporary Redis failure -> bounded retry/poll behavior，不 tight loop。
17. migration up/down 在 isolated DB 正常。
18. logs/API 不暴露 password/token/raw DB error。
19. Phase 1 API/cursor behavior无回归。

---

# 7. 最小必要测试范围

先跑 focused tests，不要无理由扩大测试范围。

必须包含：
- outbox transaction unit/integration tests；
- dispatcher tests；
- worker/SLEEP validation tests；
- atomic claim + duplicate delivery tests；
- PostgreSQL real integration；
- Redis real integration；
- PostgreSQL + Redis E2E：
  `POST -> outbox -> Redis -> worker -> SUCCEEDED`
- Redis unavailable durability test；
- duplicate publish/delivery test；
- graceful cancellation test；
- migration up/down test。

随后运行现有 acceptance：
- `go test -count=1 ./...`
- `go test -race` 针对受影响的 worker/service/dispatcher 范围
- `go vet ./...`
- `go build ./...`
- gofmt / formatting checks
- phase-state validator
- `docker compose config --quiet`
- 环境允许时执行 controlled Docker Compose smoke test。

不得用 mocks 单独证明 DB/Redis reliability。
integration test 必须继续使用 isolated schema / disposable Redis namespace，不得修改真实应用数据。

性能结论必须有 benchmark；本 Phase 默认不要做性能宣传。

---

# 8. README / Documentation

根据真实实现更新：
- `README.md`
- `docs/architecture.md`
- `docs/job-lifecycle.md`
- `docs/development-roadmap.md`
- 新增 DB/Redis handoff + Redis primitive ADR
- 必要 migration/worker operation docs

README 必须清楚区分：
- Implemented
- Experimental
- Planned

只有实际测试通过的 Phase 2 能力才能进入 Implemented。

必须明确：
- PostgreSQL 是 source of truth；
- Redis queue 已实现到什么程度；
- delivery = at-least-once；
- duplicate delivery 可能存在；
- DB claim 防止重复执行；
- worker crash 后 RUNNING recovery 尚未实现；
- 不存在 exactly-once guarantee。

---

# 9. Phase Automation Protocol

本文件：
`automation/prompts/phase-2.md`

属于 Automation 生成的 Phase 2 Prompt。

正式开始 Phase 2 时，应按照 `docs/phase-automation.md` 更新 state：
- `current_phase = 2`
- `status = "in_progress"`
- `prompt_source = "automation"`
- `prompt_path = "automation/prompts/phase-2.md"`
- `report = null`
- `next_prompt = null`
- `last_processed_phase = 1`
- branch 写真实 Phase 2 branch
- commit/tag 在未完成前为 null
- updated_at 使用真实 UTC RFC3339

Codex 不得把 `last_processed_phase` 改成 2。
Phase 2 完成后该字段由外部 Automation 审查后推进。

Codex 不得自行生成 Phase 3 Prompt。

如果用户在 Phase 2 in_progress 后明确修改 Scope，以用户最新明确指令为准，并在最终 report 记录 scope change。

---

# 10. Phase 2 验收标准

只有以下全部成立才能标记 COMPLETED：

- Job + outbox atomic creation 已实现并验证；
- DB/Redis dual-write failure 不会静默永久丢失 committed Job；
- dispatcher 可恢复 pending publication；
- Redis queue 实际工作；
- single worker 实际 consume；
- SLEEP Job end-to-end 成功；
- authoritative DB atomic claim 防止 duplicate execution；
- terminal state/result 正确持久化；
- required focused + integration + regression tests PASS；
- migration evidence PASS；
- README/docs 与真实实现一致；
- 未把 Phase 3+ Planned 能力写成 Implemented；
- Git evidence 真实；
- Phase 2 completion report 存在并通过 validator。

创建：
`docs/reports/phase-2-report.md`

报告至少包含：
- Phase / Status
- Prompt Source: automation
- Prompt Path: automation/prompts/phase-2.md
- Summary
- Implemented
- Not Implemented
- Experimental
- Planned
- exact tests + results
- dual-write failure evidence
- duplicate-delivery evidence
- worker crash limitation
- Known Limitations
- Git branch / commit / annotated tag
- GitHub push result
- Documentation Updated
- Next Recommended Phase: Phase 3（仅推荐，不启动）

---

# 11. Git / GitHub Safety

开发过程中及时留印，但不要制造无意义 commit。

完成前：
- inspect `git status`
- inspect intended diff
- scan .env / credentials / tokens / private keys / DB passwords / Redis passwords
- scan generated/runtime/large files
- migration history append-only
- `git diff --check`

测试通过后：
- commit 到真实 Phase 2 work branch；
- 使用清晰 commit message；
- 如既有流程要求，创建 annotated phase tag；
- push branch/tag；
- 禁止 force push；
- 禁止 force-with-lease；
- 禁止为了美化历史而危险 rebase/reset；
- 未获明确授权不要自动 merge main。

若 GitHub push 失败，报告真实失败，不得声称 remote completion。

---

# 12. 最终输出

若成功，Codex 最终回复必须明确列出：
- Phase 2 status
- implemented execution path
- dual-write solution
- Redis primitive
- single worker behavior
- SLEEP payload contract
- duplicate-delivery handling
- crash limitation
- exact tests/results
- report path
- branch
- commit
- annotated tag（若使用）
- push result
- final automation state
- 明确确认 Phase 3 未被 Codex 启动或生成

若阻塞：
输出 **BLOCKED**，说明精确阻塞步骤、原因、已完成测试、Git 状态以及建议动作。

不要为了完成 Phase 而降低 correctness gate。
