# FlowForge Phase 6 — Priority + Timeout + Cancellation + Dead Letter Management

## Handoff metadata
- Repository: `Daniel-Cpz/FlowForge`
- Shared entry/work branch: `codex/phase1-api-correctness`
- Prompt source: `automation`
- Prompt path: `automation/prompts/phase-6.md`
- Previous phase: Phase 5, COMPLETED
- Reviewed branch revision: `1b506022262da841495f1d154b1d75d65b7cc933`
- Phase 5 implementation checkpoint: `984014f7980c4205f60789974b39702dfc1770e6`
- Phase 5 annotated tag: `phase5-retry-idempotency`, verified to target the checkpoint
- Phase 5 metadata revision GitHub Actions `checks`: completed / success
- Review time: `2026-10-05T05:51:30Z`

This is a development instruction, NOT a Phase 6 completion report. Execute only Phase 6 after successfully claiming it through the existing Phase Automation Protocol.

## 1. 当前背景

FlowForge 已真实实现：
- PostgreSQL authoritative state
- durable dispatch intent + Redis Streams notification
- multiple workers + bounded per-process concurrency
- DB-time heartbeat / lease / renewal / stale-owner fencing / RUNNING crash recovery
- total execution attempt budget
- durable RETRYING schedules
- bounded exponential backoff + equal jitter
- DEAD_LETTER exhaustion
- exact-key submission idempotency
- at-least-once semantics with explicit non-Exactly-Once boundaries

Phase 5 仍明确保留以下缺口：
- priority 目前只是存储字段，没有真正调度语义；
- Job.timeout 目前只是 metadata，没有执行超时 enforcement；
- 没有用户 Job cancellation API；
- DEAD_LETTER 已有状态，但没有最小可用的 inspect/redrive 管理能力。

本阶段只解决这四项。不要提前实现 Phase 7 scheduled/capability-aware scheduling，也不要加入 Dashboard/WebSocket/Prometheus。

## 2. 本阶段目标

### 2.1 Priority Scheduling
高 priority Job 应在**非抢占式**调度边界上优先于低 priority Job。

要求：
- PostgreSQL 继续是 scheduling truth；Redis 仍是 notification transport。
- dispatcher pending selection 必须按 priority DESC，再用稳定 tie-breaker（例如 created_at ASC / id ASC）决定发布顺序。
- 仅调整 publish 顺序不足以构成完整优先级语义：Claim 必须防止低优先级 Job 在已有更高优先级可运行 QUEUED Job 时抢先获得执行权。
- 已经 RUNNING 的低优先级 Job不被高优先级 Job抢占；本阶段不做 preemption。
- 相同 priority 保持确定性顺序。
- 允许明确记录 starvation 风险；Priority Aging 可留待后续增强，不要本阶段过度设计。

推荐做法：
- 在 PostgreSQL Claim 条件中增加“当前 Job 是现时最高优先级 eligible QUEUED 候选之一”的约束，避免 Redis backlog 破坏优先级。
- 如果某个低优先级 delivery 因更高优先级 Job 存在而未能 Claim，它不创建 Attempt、不消耗 budget、不改变业务状态。处理方式必须有界，不形成 tight redispatch loop。
- 多 Worker 并发时允许同时 Claim 多个当前最高优先级/同级 Job，但不能让低优先级越过仍可运行的高优先级 backlog。

### 2.2 Execution Timeout
把现有 `jobs.timeout`（seconds）从 metadata 升级为真正 execution attempt timeout。

要求：
- timeout 从 Attempt 实际 execution 开始算，不包含排队/retry/backoff 时间。
- Worker 使用 context deadline 约束 executor；SLEEP 必须可被 timeout 中断。
- timeout 是 Attempt outcome，不是 worker crash。
- Attempt 应记录 TIMED_OUT。
- Job 必须遵守已有 retry budget：
  - 若 timeout 被定义为 retryable 且 budget 尚有剩余，走合法状态序列 `RUNNING -> TIMED_OUT -> RETRYING`，设置 retry_at；
  - 若预算耗尽，走 `RUNNING -> TIMED_OUT -> DEAD_LETTER`；
  - 不允许非法直接跳转。
- 因此需要明确更新 domain graph：TIMED_OUT 可作为 timeout outcome 的中间状态，并可进入 RETRYING / DEAD_LETTER；如果这样修改 `Terminal()` 语义，必须同步全部测试/文档，不能保留矛盾的“TIMED_OUT 永远终态”假设。
- timeout finalization 同样受 owner + attempt + valid lease fencing。
- timeout deadline 与 lease renewal 必须协调：正常长任务在 deadline 前持续 renew；deadline 到达后停止 renew，并按 timeout outcome 持久化。
- DB/Finalize 失败时不能伪造 timeout 成功；保持现有 lease recovery truth。

### 2.3 Job Cancellation
增加服务器权威的用户 cancellation command。

推荐 HTTP：
`POST /api/v1/jobs/{id}/cancel`

语义：
- QUEUED：原子转 CANCELLED，取消/失效 durable dispatch intent；后续旧 Redis delivery 必须 harmless。
- RETRYING：原子转 CANCELLED，清除 retry_at；不得被 promoter 再次排队。
- RUNNING：不要直接假装执行已经停止。增加 durable cancellation request（例如 `cancel_requested_at`）：
  - API 原子记录 cancel request；
  - 当前 owner 在 lease renewal / lightweight cancellation check 时观察该请求并 cancel executor context；
  - 随后使用 fenced Finalize 将 Job + 当前 Attempt 持久化为 CANCELLED；
  - cancel request 到 Finalize 之间 GET 可以继续看到 RUNNING + cancellation_requested metadata（若选择对 API 暴露）。
- 如果 RUNNING worker 在 cancel request 后 crash，lease recovery 必须识别 cancellation request，并最终 CANCELLED，而不是 RETRYING。
- cancellation 是用户意图，**不能消耗一个新的 retry attempt，也不能自动 retry**。
- 对 SUCCEEDED / CANCELLED / DEAD_LETTER 等不可取消终态，定义明确 idempotent/conflict 语义：
  - CANCELLED 再 cancel 可返回 200/204 idempotent；
  - SUCCEEDED/DEAD_LETTER 返回 409 或等价稳定错误；
  - 404 保持标准 Job not found。
- stale worker 在 cancellation 后迟到 Finalize 必须被 fencing 拒绝。

不要用轮询高频 HTTP 实现 Worker cancellation。优先复用 lease-renew 周期感知 cancel request；若增加额外 check，必须 bounded 且有清晰理由。

### 2.4 Dead Letter Management
Phase 5 已经能把 permanent/exhausted Job 放入 DEAD_LETTER。本阶段提供最小可操作管理能力，但不做 UI。

至少提供：
- `GET /api/v1/dead-letter?limit=&cursor=`：确定性分页，仅返回 DEAD_LETTER Jobs。
- `GET /api/v1/jobs/{id}/attempts`：按 attempt_number 稳定顺序返回 Attempts，包含 worker_id/status/start/end/error/result；避免暴露 raw driver/internal secret。
- `POST /api/v1/jobs/{id}/retry`：人工 redrive DEAD_LETTER Job。

人工 redrive 推荐作为**显式特殊管理操作**，不要放宽普通 domain transition：
- 保持普通 `DEAD_LETTER` 终态；
- repository/service 提供受控 RedriveDeadLetter operation；
- redrive 不删除历史、不重置 attempt_count；
- 因 Phase 5 规定 `attempt_count <= max_attempts`，人工 redrive 必须显式授予新的 attempt budget。建议每次 redrive 把 `max_attempts` 增加 1（不超过全局 100 上限），然后把 Job 原子恢复为可调度状态；
- redrive 本身不创建 Attempt；下一次 Claim 才创建；
- 如果 max_attempts 已 100，返回稳定冲突，不绕过约束；
- redrive 必须清理旧 terminal execution metadata/result，恢复 dispatch intent，并留下可解释日志；
- 并发两个 redrive 请求只能一个有效改变预算/状态，另一个看到最新状态后返回幂等/冲突，不能重复增加预算。

不要实现持久化 per-job application logs / full log store。本阶段 DLQ inspect 的“可观察证据”以 Job + Attempts + error/result + worker 为主；持久日志/Tracing 属于后续 observability phase。README 要明确这一限制。

## 3. Out of Scope
不要实现：
- Priority Aging / weighted fairness（可记录为后续增强）
- preemptive scheduling
- scheduled/cron jobs
- capability-aware scheduling
- user quotas / admission control
- dashboard / WebSocket
- Prometheus / Grafana / OpenTelemetry
- Kafka / RabbitMQ
- arbitrary shell / Docker execution
- DLQ UI
- persistent centralized log store
- Kubernetes / Terraform / cloud deployment

不要改变 submission idempotency 的 Phase 5 contract，除非为了新增 endpoint 的一致错误模型做最小兼容修改。

## 4. Data / Migration 要求

根据实际 schema 最小新增 append-only migration（例如 000006），不得改 000001–000005。

至少评估：
- `cancel_requested_at TIMESTAMPTZ NULL`
- priority scheduling 所需 partial/index，例如 QUEUED priority order
- dead-letter listing index，例如 status + finished_at/id
- cancellation/state consistency constraint
- 若 timeout/retry graph 需要数据 invariant，添加最小 constraint

不要为 DLQ 管理创建大而泛化的“admin subsystem”。

**重要：Phase 5 报告记录现有 development DB 有一个 legacy duplicate non-null idempotency key group，导致 000005 在该 DB 上有意阻塞。**
- 不得在 Phase 6 自动删除/合并/改写这些历史 Job。
- 不得为了跑 Phase 6 而绕过 000005 preflight。
- Phase 6 implementation/testing 应继续使用 isolated compatible DB/schema，或者等待用户另行明确授权处理 legacy data。
- 文档中继续说明 retained development DB 可能仍停留在 Phase 4 schema，直到 operator resolution。
- 这不是 Phase 6 可以静默修复的数据问题。

## 5. Priority Correctness Invariants

必须验证：
- high priority QUEUED 存在时，新 Claim 不应让 lower priority candidate 越过它；
- same priority 有稳定 tie-breaker；
- already RUNNING low priority 不被 preempt；
- duplicate Redis delivery 不改变 priority correctness；
- priority-deferred delivery 不创建 Attempt、不消耗 max_attempts；
- retry promotion 回到 QUEUED 后重新参与 priority scheduling；
- manual DLQ redrive 回到 QUEUED 后按正常 priority 参与调度；
- cancellation 后 Job 不再被 claim；
- 多 worker 并发不会产生同 Job 双 claim。

不要声称 strict global FIFO；优先级是 non-preemptive scheduling policy，网络/Redis notification 时序不构成业务 truth。

## 6. Timeout / Cancellation Correctness Invariants

- execution timeout 以 Attempt execution deadline 为准，不是 queue latency timeout。
- TIMED_OUT Attempt 必须有 finished_at / stable error/result。
- retryable timeout 只能在剩余 budget 时 RETRYING。
- no N+1 attempt。
- cancellation request 必须 durable。
- RUNNING cancellation 只有当前 fenced owner 可以完成当前 Attempt 的 CANCELLED finalize；若 owner 丢失，recovery 根据 cancel request 收敛到 CANCELLED。
- cancel/recover/finalize race 只能有一个权威结果。
- CANCELLED 不 retry。
- cancellation 不产生额外 Attempt。
- timeout context cancel 与 user cancel 必须使用不同 stable reason/code，不能混淆。
- executor/renew goroutine 在 timeout/cancel 后必须 join，无泄漏。

## 7. DLQ / Redrive Correctness Invariants

- DEAD_LETTER list 只包含 DEAD_LETTER。
- Attempts API 返回完整历史且顺序稳定。
- redrive 不删除/覆盖旧 Attempts。
- redrive 不重置 attempt_count。
- redrive 只增加明确的 attempt budget，且不超过 100。
- redrive 与 concurrent retry/cancel/other redrive 有条件更新/fencing。
- redrive 与 dispatch intent 原子。
- old Redis pending delivery / stale owner 不能绕过 redrive 后的新 execution ownership。
- manual redrive 是显式操作，不等同自动 retry。

## 8. Failure / Edge Cases

至少覆盖：

### Priority
- 低优先级 delivery 已在 Redis，随后高优先级 Job 入队
- 两个 worker 同时拿到 high/low candidates
- 多个相同 priority Job
- 高 priority Job retry 后重新 QUEUED
- 长时间 high backlog 导致 low priority starvation（验证不会错误执行，记录 starvation limitation，不要求 aging）

### Timeout
- SLEEP duration > timeout，Attempt TIMED_OUT
- timeout 后 budget remaining -> RETRYING -> later success（用测试 executor/调整 timeout；不要新增生产 Job type）
- timeout at budget exhaustion -> DEAD_LETTER
- timeout 与 lease renewal 同时发生
- timeout finalization DB failure
- stale timeout finalize after recovery

### Cancellation
- cancel QUEUED before dispatch
- cancel QUEUED with stale Redis delivery already present
- cancel RETRYING before retry_at
- cancel RUNNING and worker cooperatively stops
- cancel RUNNING then worker crash before finalize
- cancel vs lease expiry/reaper race
- cancel twice
- cancel SUCCEEDED/DEAD_LETTER
- cancel missing Job
- stale worker late success after cancel

### DLQ
- list pagination with same timestamps
- attempts retrieval for missing Job / no Attempts / multiple Attempts
- manual redrive normal case
- redrive when max_attempts=100
- two concurrent redrives
- redrive + cancellation race
- dispatch reset failure rolls back redrive
- replay keyed POST after DLQ/redrive still returns original Job, never creates duplicate logical Job

## 9. 最小必要测试范围

优先 affected modules；Phase 6 横跨 scheduler / execution / API / repository，因此至少：

- domain transition + TIMED_OUT semantics tests
- priority selection/claim tests
- concurrent multi-worker priority integration
- timeout executor context tests
- cancellation service/repository/HTTP tests
- cancel/recovery/finalize race integration
- DLQ list/cursor tests
- attempts API tests
- redrive concurrency/budget tests
- PostgreSQL + Redis real-service integration
- migration up/down tests
- existing retry/idempotency regression tests
- race detector on affected concurrent code
- phase-state validator
- `go vet ./...`
- `go build ./...`
- `git diff --check`

建议最终运行：
`go test -race -count=1 ./internal/service/... ./internal/infrastructure/... ./tests/integration`

若成本合理，本阶段最终验收运行：
`go test -count=1 ./...`

但所有 NOT RUN / SKIP 必须如实记录，不得写成 PASS。

## 10. Real-service smoke

至少做一个隔离 smoke，使用 disposable database + Redis namespace，不修改 retained development DB 的 legacy duplicate data。

场景至少覆盖：

1. Priority:
   - 创建 low priority 长 SLEEP 与 high priority Job；
   - 在明确 claim barrier 下证明 high priority 在后续非抢占式 claim boundary 先于尚未运行的 low priority。

2. Timeout:
   - 创建会超过 timeout 的 SLEEP；
   - 观察 Attempt TIMED_OUT；
   - 若有预算，进入 RETRYING；
   - 最终 exhaustion 或受控后续成功符合 policy。

3. Cancellation:
   - 创建长 SLEEP；
   - RUNNING 后调用 cancel endpoint；
   - worker 在 bounded 时间内停止；
   - Job/Attempt 最终 CANCELLED；
   - 无后续 retry。

4. DLQ:
   - 制造 permanent/exhausted DEAD_LETTER；
   - list + attempts inspect；
   - manual redrive；
   - 验证历史 Attempt 保留且新 Attempt number 增加。

Smoke cleanup 只能删除其生成资源，并恢复正常服务；不得处理 retained duplicate-key application data。

## 11. 验收标准

Phase 6 只有全部满足才可 COMPLETED：

- [ ] priority scheduling 有 PostgreSQL 权威 Claim 约束，不只是 dispatcher 排序
- [ ] non-preemptive semantics 文档明确
- [ ] timeout 真正约束 execution
- [ ] TIMED_OUT retry/dead-letter state graph 合法、测试完整
- [ ] queued/retrying/running cancellation 语义完整
- [ ] cancel + lease recovery race 安全
- [ ] DEAD_LETTER list API
- [ ] Job Attempts inspect API
- [ ] manual redrive，保留历史且显式增加 budget
- [ ] concurrent redrive/cancel 安全
- [ ] Phase 5 retry/idempotency regression 无破坏
- [ ] retained legacy duplicate-key DB 未被静默修改
- [ ] README / architecture / lifecycle / worker operations / roadmap / ADR 更新
- [ ] `docs/reports/phase-6-report.md` 完整
- [ ] Git 安全检查、正常 push/tag evidence
- [ ] Automation completed state 只在 gates 全部通过后发布
- [ ] 未提前实现 Phase 7

## 12. README / 文档要求

至少更新：
- README.md
- docs/architecture.md
- docs/job-lifecycle.md
- docs/worker-operations.md
- docs/development-roadmap.md
- docs/reports/index.md
- 新 ADR：Priority/Timeout/Cancellation/DLQ policy
- docs/reports/phase-6-report.md

必须明确：
- priority 是 non-preemptive
- starvation aging 尚未实现（如果本阶段不做）
- timeout 是 attempt execution timeout，不是 queue timeout
- user cancellation 与 execution_cancelled operational interruption 是不同概念
- DLQ manual redrive 如何影响 max_attempts
- persistent per-job logs / DLQ UI 未实现
- submission idempotency 仍不保证 business side-effect exactly-once
- Phase 7 scheduled/capability-aware scheduling 仍 Planned

## 13. Phase Automation Protocol

领取本 Prompt 时必须先发布 ownership claim：
- `current_phase = 6`
- `status = in_progress`
- `prompt_source = automation`
- `prompt_path = automation/prompts/phase-6.md`
- `last_processed_phase = 5`
- `report = null`
- `commit = null`
- `tag = null`
- `next_prompt = null`

先 push 并回读，再开始开发。

Codex 不得：
- 自行生成 Phase 7 Prompt
- 自行推进 `last_processed_phase = 6`
- 自动 merge main
- 自动处理 retained legacy duplicate-key data

完成后生成 `docs/reports/phase-6-report.md`，只记录真实测试、失败、Known Limitations 和 Git evidence。

## 14. Git / GitHub 安全

- 继续使用协议指定共享入口分支，除非用户/协议明确迁移。
- 禁止 force push / force-with-lease / reset --hard / history rewrite。
- 历史 migrations/reports/prompts 不改写。
- 不提交 .env / credentials / tokens / private keys / binaries / temp DB dumps / smoke artifacts。
- main 不自动 merge。
- 若沿用阶段 tag，创建 annotated tag（例如 `phase6-control-dlq`）并验证指向 implementation/report checkpoint。
- retained development DB 的 legacy duplicate keys 只能在用户明确授权的数据修复任务中处理。

## 15. Codex 对话日志

开始：
`FlowForge Phase Start Log`
包含 Phase 6、prompt、execution id、branch、previous checkpoint、status IN_PROGRESS。

完成：
`FlowForge Phase Completion Log`
包含 priority/timeout/cancellation/DLQ 实现摘要、真实测试/smoke、report、checkpoint/tag/push、state、Known Limitations、Waiting For external review。

阻塞：
`FlowForge Phase Blocked Log`
明确失败步骤、原因、测试/Git/state、部分提交和最小恢复动作。

核心原则：
**Phase 6 的价值不是堆四个 API，而是在已有 at-least-once + lease + retry 基础上，把调度优先级、attempt timeout、用户取消和 DEAD_LETTER 人工恢复做成互相不破坏的权威状态协议。**
