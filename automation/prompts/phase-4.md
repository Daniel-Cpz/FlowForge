# FlowForge Phase 4 — Heartbeat + Lease + Crash Recovery

## Handoff metadata
- Repository: `Daniel-Cpz/FlowForge`
- Shared entry/work branch: `codex/phase1-api-correctness`
- Prompt source: `automation`
- Prompt path: `automation/prompts/phase-4.md`
- Previous phase: Phase 3, COMPLETED
- Reviewed branch revision: `dba54963606d22822b92fca06e387b4674387c39`
- Phase 3 implementation checkpoint: `cfe1988f3a6e5c3022464ee33fa18e4e1945aab3`
- Phase 3 annotated tag: `phase3-multi-worker`, verified to target the checkpoint
- Phase 3 completion CI at reviewed metadata revision: GitHub Actions `checks` completed successfully
- Review time: `2026-10-05T03:56:10Z`

This is a development instruction, not a completion report. Execute only Phase 4 after successfully claiming it through the existing Phase Automation Protocol.

## 1. 当前背景
FlowForge 目前已经真实实现：
- PostgreSQL 为 authoritative source of truth
- Job + durable dispatch intent 的原子写入
- Redis Streams 作为通知/投递层
- 多独立 Worker 进程
- 每进程固定、有界 concurrency slots
- PostgreSQL 条件 Claim、独立 Job Attempt、owner/attempt 约束终态写入
- at-least-once delivery 下的重复投递安全
- 并发 graceful shutdown 与进程级 fail-fast supervision

Phase 3 仍明确保留一个核心可靠性缺口：Worker 在 Job 已进入 RUNNING 后崩溃、断网或长期失联时，Job 可能永久停留在 RUNNING。QUEUED republication 不能解决 RUNNING work recovery。

本阶段只解决这个缺口：**Heartbeat + Lease + Crash Recovery + stale-owner fencing**。不要顺手实现 Phase 5 retry/idempotency。

## 2. 本阶段目标
实现可测试、可解释的 lease-based execution，使系统能够演示：

Worker A claim Job
→ Job RUNNING + lease
→ Worker A 被强制 kill / 无法续租
→ lease 到期
→ 系统判定旧 owner 失效
→ Job 安全重新变为可调度
→ Worker B claim 新 attempt
→ Job 最终完成

语义仍然是 **At-Least-Once Delivery**。不要声称 Exactly-Once Execution。必须区分“消息重复投递”和“业务副作用只发生一次”。

## 3. Scope
1. Worker heartbeat/liveness
   - Worker 有稳定 worker_id 与明确状态/last_heartbeat。
   - Heartbeat 周期和离线判定阈值必须显式配置、有限范围、文档化。
   - 多 worker 并发 heartbeat 不得造成无界 goroutine 或数据库压力。

2. Lease-based claim
   - RUNNING Job 在 claim 时记录 assigned_worker / lease_expiry。
   - lease 时长必须显式配置；claim 与 attempt 创建保持事务一致性。
   - 正在执行的 worker 周期性续租。
   - 续租只能由当前有效 owner/attempt 成功执行。

3. Stale-owner fencing
   - 旧 worker 在 lease 过期并被回收后，即使恢复网络或迟到完成，也不得覆盖新 owner 的结果。
   - Finalize/renew 必须携带足够的 owner + attempt + lease/fencing 条件。
   - 不允许“仅凭 status=RUNNING”完成更新。

4. Crash recovery / requeue
   - 增加一个有界 recovery/reaper loop，扫描 lease 已过期的 RUNNING jobs。
   - 回收必须是 PostgreSQL 条件更新，避免多个 reaper 重复恢复同一 Job。
   - 恢复后的 Job 应重新进入可调度状态并触发/恢复 dispatch intent。
   - 对应旧 attempt 必须留下明确结束原因，例如 lease_expired / worker_lost；不要静默覆盖历史。

5. Worker offline detection
   - heartbeat 超时后 worker 进入 OFFLINE 或等价可解释状态。
   - 不要求本阶段做复杂 capability scheduling；worker registry 只服务 liveness/recovery。

6. Graceful shutdown
   - SIGTERM 正常 draining 仍优先完成或按现有策略安全终止当前任务。
   - 正常 shutdown 与 crash recovery 必须语义区分：不要把正常退出误判为 crash。
   - 退出后停止 heartbeat/renew，且所有 goroutine 可 join。

7. Observability / logs
   - 结构化事件至少覆盖 heartbeat, lease_acquired, lease_renewed, lease_expired, worker_offline, recovery_requeued, stale_finalize_rejected。
   - 日志包含 job_id / worker_id / attempt_number 等可追踪字段，但不得泄露 payload/secrets/raw DB credentials。

8. Tests / demo
   - 必须有真实 PostgreSQL/Redis integration。
   - 必须有至少两个独立 worker 的 crash-recovery smoke。
   - 必须有 stale owner/finalize fencing 测试。
   - 必须验证 recovery 在并发 reaper 下仍只发生一次有效状态转换。

## 4. Out of Scope
本阶段不要实现：
- max_attempts retry policy、exponential backoff、jitter
- submission/business idempotency
- priority scheduling / starvation aging
- general user cancellation / generic execution timeout policy
- DLQ
- scheduled jobs / capability-aware scheduling
- dashboard / WebSocket
- Prometheus/Grafana/OpenTelemetry
- Kubernetes/Terraform/cloud deployment
- arbitrary shell/Docker job execution

如果 lease expiry 后是否“立即重新执行”与 max_attempts 的最终策略存在交叉，本阶段只实现 crash recovery 所需的最小语义，并在文档中明确 Phase 5 再统一 retry accounting。不要提前扩展成完整 retry subsystem。

## 5. 数据模型与 migration 要求
优先复用已有字段；根据仓库真实 schema 决定是否需要 append-only migration。禁止修改历史 migration。

至少保证可表达：
- workers: worker_id, status, last_heartbeat, concurrency, active_jobs（若现有模型/表结构适合）
- jobs: assigned_worker, lease_expiry
- attempts: worker_id, attempt_number, start/end, error/result/recovery reason

如果现有 domain 已有这些字段但数据库未持久化，补齐最小必要 persistence。

所有 lease/heartbeat 时间使用 PostgreSQL/UTC 一致语义。避免依赖各 worker 本机墙钟做权威判断；优先由数据库时间决定 lease validity，防止 clock skew。

## 6. 正确性要求
- Claim: 只允许合法 QUEUED → RUNNING。
- Renew: 仅当前 owner + 当前 attempt + 未被替换的 lease 能续租。
- Recovery: 仅 lease_expiry 已过期且仍是同一 RUNNING owner/attempt 才能回收。
- Reclaim 后旧 worker 的 renew/finalize 必须失败。
- 新 attempt number 必须单调且不覆盖旧 attempt。
- 恢复与 dispatch intent 的更新要有明确事务边界；不能形成“DB 已 requeue 但永远没有重新投递机会”的窗口。
- 多 recovery loop / 多 worker 同时观察过期 Job 时，只有一个 recovery transition 生效。
- 数据库操作必须 bounded/context-aware；不要长期事务跨越 SLEEP/Redis/network wait。

## 7. Failure / Edge Cases
至少覆盖：
- worker 在 claim 后、执行前 crash
- worker 在 SLEEP 中途 crash
- worker 失网但进程仍活着，lease 无法续租
- lease renewal DB failure
- heartbeat DB failure
- recovery loop DB failure
- recovery 成功但 Redis publish/mark 失败
- 两个 reaper 同时回收同一 Job
- 旧 worker 在 recovery 后迟到 finalize
- 旧 worker 在 recovery 后迟到 renew
- worker 恢复连接但已被标记 OFFLINE
- graceful shutdown 与 lease expiry 竞争
- PostgreSQL 短暂不可用时不应伪造 lease 成功
- Redis 丢失 notification 后，PostgreSQL durable intent 仍可恢复 delivery

对 ambiguous DB commit 必须明确记录限制；不能通过猜测返回值声称安全完成。

## 8. 最小必要测试范围
优先运行受影响模块，不要求无意义全量回归。

至少：
- domain/state transition / lease validity unit tests
- repository claim/renew/recover/finalize fencing tests
- worker heartbeat/renew lifecycle tests
- concurrent recovery race tests
- PostgreSQL + Redis integration tests
- 2-worker independent process crash smoke：
  1. 提交较长 SLEEP
  2. Worker A claim
  3. 强制 kill -9 / 等价 abrupt termination
  4. 确认没有 graceful finalize
  5. 等 lease expiry
  6. recovery requeue
  7. Worker B 新 attempt claim
  8. SUCCEEDED
  9. 旧 attempt 保留为 recovery/lease-expired 证据
- stale finalize simulation：A 的旧 owner/attempt 完成写入必须被拒绝
- race detector 覆盖新并发代码
- go vet/build
- phase-state validator
- git diff --check

若受影响范围已经跨核心 worker/repository，建议运行 `go test -race ./internal/service/... ./internal/infrastructure/... ./tests/integration`；完整 `go test ./...` 只有在成本合理或最终验收需要时运行。不要把 NOT RUN 写成 PASS。

## 9. 验收标准
Phase 4 只有在以下条件全部满足时才能标记 COMPLETED：
- [ ] heartbeat/liveness 有真实持久化和测试
- [ ] claim 建立 lease
- [ ] running worker 能续租
- [ ] lease expiry 可被并发安全地恢复
- [ ] stale owner renew/finalize 被 fencing 拒绝
- [ ] recovered job 创建新的独立 attempt
- [ ] abrupt worker crash → another worker takeover → final success 的 smoke PASS
- [ ] duplicate delivery / concurrent reaper 不产生重复有效 attempt
- [ ] graceful shutdown 与 crash recovery 语义清晰
- [ ] README/architecture/lifecycle/worker operations/roadmap 同步
- [ ] 独立 `docs/reports/phase-4-report.md` 完成
- [ ] Git 安全检查、正常 commit/push、必要 annotated tag 完成
- [ ] `automation/state.json` 只在完成 gates 全部通过后进入 completed
- [ ] 未实现 Phase 5 retry/idempotency

## 10. README / 文档
至少更新：
- README 当前能力与明确限制
- docs/architecture.md
- docs/job-lifecycle.md
- docs/worker-operations.md
- docs/development-roadmap.md
- 必要 ADR：说明 lease duration、heartbeat interval、DB-time authority、recovery transaction、fencing 选择
- docs/reports/index.md
- docs/reports/phase-4-report.md

README 必须继续明确：
- at-least-once delivery
- crash recovery 不等于 exactly-once side effects
- Phase 5 retry/idempotency 尚未实现

## 11. Phase Automation Protocol
领取本 Prompt 时：
- 先发布 ownership claim：
  - current_phase = 4
  - status = in_progress
  - prompt_source = automation
  - prompt_path = automation/prompts/phase-4.md
  - last_processed_phase = 3
  - 清空 report / commit / tag / next_prompt
- 必须先成功 push 并回读 claim 状态，再开始业务开发。
- 不得由 Codex 自行生成 Phase 5 Prompt 或推进 last_processed_phase=4。
- Phase 完成后生成 `docs/reports/phase-4-report.md`，记录真实测试、失败、限制和 Git evidence。
- 只有完成 gates 全部通过后才能发布 completed state。

## 12. Git / GitHub 安全要求
- 继续使用共享入口分支，除非协议/用户明确迁移。
- 禁止 force push / force-with-lease / reset --hard / history rewrite。
- 修改前后检查 git status / intended diff。
- 禁止提交 .env、token、credential、private key、生成二进制、大型临时 smoke artifact。
- 历史 migration/report/prompt 不删除、不改写。
- main 不自动 merge。
- tag 若沿用现有模式，使用 annotated tag，例如 `phase4-lease-recovery`，并验证目标 checkpoint。

## 13. Codex 对话日志
开始后输出：
`FlowForge Phase Start Log`
包含 Phase 4、prompt path、execution id、branch、previous checkpoint、status=IN_PROGRESS。

完成后输出：
`FlowForge Phase Completion Log`
包含实现摘要、测试命令/结果、crash smoke、report、branch/checkpoint/tag/push、state、Known Limitations、Waiting For external review。

若阻塞：
`FlowForge Phase Blocked Log`
必须说明失败步骤、原因、测试/Git/state、是否存在部分提交，以及最小恢复动作。

核心原则：Phase 4 的价值不是“加 heartbeat 字段”，而是用可证明的 lease + fencing + recovery 机制解决 Phase 3 已知的 RUNNING worker crash failure case。
