# FlowForge Phase 8 — React / TypeScript Dashboard + WebSocket

## Handoff metadata
- Repository: `Daniel-Cpz/FlowForge`
- Shared entry/work branch: `codex/phase1-api-correctness`
- Prompt source: `automation`
- Prompt path: `automation/prompts/phase-8.md`
- Previous phase: Phase 7, COMPLETED
- Reviewed branch revision: `f4d8f3dd414d32660c7565a7d2106d0f11552167`
- Phase 7 implementation checkpoint: `54daf19da8009c2e35a6f9bff1ca5caea4be20cf`
- Phase 7 annotated tag: `phase7-scheduling-capabilities`, verified to target that checkpoint
- Phase 7 metadata revision GitHub Actions `checks`: completed / success
- Review time: `2026-10-05T07:32:00Z`

This is a development instruction, NOT a Phase 8 completion report. Execute only Phase 8 after successfully claiming it through the existing Phase Automation Protocol.

## 1. 当前背景

FlowForge 当前已经真实实现一个较完整的分布式任务执行控制面：
- PostgreSQL authoritative job/schedule/worker state
- Redis Streams notification transport + durable DB dispatch intent
- multi-worker + bounded concurrency
- heartbeat / lease / renewal / fencing / crash recovery
- retry budget / RETRYING / backoff / jitter / DEAD_LETTER
- exact-key submission idempotency
- priority / timeout / cancellation / manual redrive
- delayed jobs / capability-aware Claim
- fixed-interval recurring schedules
- REST API for Jobs / cancellation / DLQ / attempts / schedules

目前仓库根目录没有 React/TypeScript frontend。Phase 8 的目标是增加一个**只作为展示与操作控制面的 Dashboard**，并用 WebSocket 实时推送状态变化。Dashboard 不是 correctness authority；PostgreSQL/现有服务仍然是真实来源。

本阶段必须保护已有分布式系统正确性。不要为了 UI 重写核心调度逻辑，也不要把 WebSocket delivery 描述成可靠业务消息。

## 2. 本阶段目标

实现：

1. React + TypeScript Dashboard；
2. Jobs / Workers / Schedules / Dead Letter 的可视化；
3. Job Detail + Attempts；
4. 常用已有控制操作（cancel / dead-letter redrive / schedule cancel）；
5. WebSocket 实时状态事件；
6. reconnect 后可靠回到 PostgreSQL 当前事实状态；
7. 有界、可测试的 WebSocket client/server backpressure；
8. Docker Compose 中可本地演示。

最终 Demo 应能够：
- 打开 Dashboard；
- 创建/观察 Job（创建可以保留 REST/curl，也可提供简单表单，但不要扩大业务）；
- 同时启动多个 Worker；
- 看到 Job QUEUED/RUNNING/RETRYING/SUCCEEDED/DEAD_LETTER 等变化近实时反映；
- Kill Worker 后看到 Worker/Job 状态通过已有 lease/recovery 机制变化；
- WebSocket 断开后自动重连并重新获取 authoritative snapshot；
- 不依赖高频浏览器 HTTP polling。

## 3. Architecture Principle

WebSocket **不是业务事实源，也不是 task delivery mechanism**。

推荐：
- PostgreSQL：authoritative current state；
- REST：initial snapshot / explicit detail fetch / control commands；
- Redis Pub/Sub（或一个最小、明确的非权威 event channel）：跨 API/Worker process fan-out UI invalidation/event hints；
- WebSocket：API server 到 browser 的实时 transient event stream；
- browser reconnect：重新 GET snapshot，不能假设错过的 WebSocket event 一定能 replay。

如果选择 Redis Pub/Sub：
- 明确其 transient / at-most-once-ish UI notification 特性；
- event publish 失败不得回滚已经成功的业务事务；
- client/server 必须通过 REST snapshot 修复丢 event；
- 不要把现有 Job Redis Stream consumer group复用成浏览器 event transport。

如果你认为单独的 Redis Stream UI event log更简单/更可靠，可以采用，但必须说明 retention/replay policy，且不得让 UI event publishing 成为业务事务成功的必要条件。优先选择简单、可解释方案。

## 4. Backend Scope

### 4.1 Dashboard read APIs

复用已有 API，补齐 Dashboard 最小只读数据：

推荐新增：
- `GET /api/v1/dashboard/summary`
- `GET /api/v1/workers?limit=&cursor=` 或 bounded list
- 如现有 schedule 无 list，则 `GET /api/v1/schedules?limit=&cursor=`

Summary 至少包含：
- job counts by status：
  - QUEUED
  - RUNNING
  - SUCCEEDED
  - FAILED（如果当前业务通常只作为事务中间态，按真实 persisted state统计）
  - RETRYING
  - DEAD_LETTER
  - CANCELLED
  - TIMED_OUT（按当前 schema/语义真实呈现）
- queue_depth：定义为当前 eligible/queued backlog，必须文档化口径；
- worker counts by status；
- total active_jobs；
- schedule ACTIVE/CANCELLED counts（若成本低）。

不要在 Phase 8 伪造：
- throughput
- failure rate
- P50/P95/P99 latency

这些应由 Phase 9 metrics/observability 基于真实指标实现。Dashboard 可以明确显示“Metrics planned for Phase 9”，但不要画随机/近似图表并称为真实性能指标。

### 4.2 Worker read model

Worker list 最少返回：
- worker_id
- status
- capabilities
- concurrency
- active_jobs
- last_heartbeat

不要返回 secrets / connection string。

排序与分页必须稳定、bounded。可以按 last_heartbeat DESC + worker_id tie-break。

### 4.3 Job detail

前端 Job Detail 应复用：
- GET Job
- GET Attempts

并展示：
- Job ID
- type
- status
- priority
- payload（格式化 JSON；大 payload UI 需要折叠/限制渲染）
- result
- attempt_count / max_attempts
- timeout
- idempotency key（注意 UI 不要假设它是 secret，但也不要把它写入 logs）
- required capabilities
- scheduled_at / schedule occurrence identity（如有）
- assigned worker
- lease expiry
- retry_at
- cancellation requested state（如果 API 已暴露）
- timestamps
- Attempts history

不要添加 persistent application log store。本阶段 Job Detail 的“Logs”可以明确显示为“not available until observability/logging phase”，或者只展示结构化 attempt error/result；不得伪造日志。

### 4.4 Control actions

Dashboard 可调用已有：
- cancel Job
- dead-letter redrive
- cancel Schedule

要求：
- UI 必须显示后端真实成功/冲突/失败；
- 不做 optimistic authoritative state mutation；
- 操作成功后重新 fetch 对应 resource / 依赖 WebSocket hint 刷新；
- destructive/semantic action需要简单确认，例如 redrive/cancel；
- disable 仅作为 UX，不替代 server-side validation。

## 5. WebSocket Contract

新增明确 endpoint，例如：

`GET /api/v1/ws`

使用标准 WebSocket upgrade。

Go 标准库不内置完整 WebSocket 实现，因此允许加入**一个小型、维护良好的 WebSocket dependency**。不要引入完整 realtime framework。选择后在 ADR 说明原因。

### 5.1 Event envelope

推荐：

```json
{
  "version": 1,
  "event": "job.changed",
  "resource_id": "<uuid>",
  "occurred_at": "RFC3339",
  "hint": {
    "status": "RUNNING"
  }
}
```

事件至少：
- `job.changed`
- `worker.changed`
- `schedule.changed`
- 可选 `system.changed`

WebSocket event 是 **invalidation hint**，不是完整 authoritative entity snapshot。

Browser 收到：
- job.changed → 更新局部缓存/重新 GET detail或刷新当前 jobs page；
- worker.changed → 刷新 workers/summary；
- schedule.changed → 刷新 schedule；
- reconnect → 全量刷新当前可见 snapshot。

不要要求客户端从 event 顺序重建 Job FSM。

### 5.2 Event source

必须覆盖主要 durable state changes：
- Job create
- Claim / running
- Finalize success/failure/timeout/cancel
- retry schedule / promote
- lease recovery
- dead-letter redrive
- delayed/scheduled materialization
- schedule cancel
- worker register / heartbeat-derived status change / draining/offline

不要求每个 heartbeat 都推一条浏览器 event；否则会制造 event storm。
推荐：
- worker status / active_jobs/capability identity变化时推；
- dashboard last_heartbeat 可以通过较低频 server summary refresh或 client display aging，但不要 browser 每秒 REST poll。

### 5.3 Multi-process fan-out

多个 Worker 与 API 进程产生变化时，连接在任意 API instance 的 WebSocket client都应该最终获得 invalidation。

如果使用 Redis Pub/Sub：
- 每个进程在 durable commit 后 publish小事件；
- API WebSocket hub订阅 channel并广播；
- 发布失败只记录 sanitized warning；不改变业务事务结果；
- snapshot reconciliation修复遗漏。

### 5.4 Connection lifecycle

必须实现：
- max connection limit（合理默认，例如 100/500，按项目本地 demo 定位选择）；
- per-client bounded outbound buffer；
- slow client policy：不要让一个客户端阻塞 worker/API。
  推荐：buffer 满则断开该 slow client，让其 reconnect + snapshot；
- read limit；
- ping/pong 或 idle timeout；
- context cancellation / server graceful shutdown；
- no unbounded goroutine；
- unregister/join/close cleanly；
- same client repeated reconnect不泄漏资源。

不要做浏览器到服务端的业务 command over WebSocket。控制操作继续使用 REST。

## 6. Frontend Scope

新建例如：

`web/`

技术栈：
- React
- TypeScript
- Vite（或同等轻量 build tool）
- 原生 fetch / WebSocket 优先

除非有明确必要，不要引入 Redux、大型 UI framework、GraphQL、Next.js、复杂状态管理库。
轻量 router 可以用 React Router，也可以自建简单 routing；选择最清晰可测试方案。

### 6.1 Pages

至少：

#### Overview
- Job counts
- queue depth
- Worker status counts
- active jobs
- schedule counts（若 summary 有）
- 明确 Phase 9 性能 metrics 尚未实现

#### Jobs
- paginated Jobs table
- status / type / priority / attempt count / worker / created time
- 可按现有 API 能力做最小 status filter；如果现有 List 没有 filter，不要为 UI 写巨型 query framework。允许增加必要 bounded status filter，但测试契约。

#### Job Detail
- full fields
- Attempt timeline/table
- cancel action（合法时）
- dead-letter redrive（合法时）
- WebSocket change后刷新

#### Workers
- status
- capabilities
- concurrency
- active jobs
- last heartbeat
- ONLINE/IDLE/BUSY/DRAINING/OFFLINE真实值

#### Schedules
- ACTIVE/CANCELLED
- type/priority/capabilities
- interval
- next_run_at
- cancel action

#### Dead Letter
- existing DLQ list
- last result/error summary
- inspect Job/Attempts
- redrive

### 6.2 UX / correctness

- 不用复杂动画；
- desktop-first但响应式，基本支持窄屏；
- loading / empty / error / reconnecting state必须清晰；
- WebSocket disconnected时显示状态；
- reconnect成功后明确 resync；
- 所有时间展示浏览器本地时区，但保留完整 timestamp tooltip/可读 UTC；
- Job status使用统一视觉 badge；
- 不因 WebSocket event直接伪造状态；
- API 409/400/500按稳定 error envelope展示，不显示 raw stack。

## 7. API / CORS / Dev integration

开发环境：
- Vite dev server例如 5173
- Go API 8080
- 优先通过 Vite proxy把 `/api` 和 `/api/v1/ws`代理到 Go，减少随意开放 CORS。

如果为了独立前端部署增加 CORS：
- 默认 restrictive；
- 不允许 production `*` + credentials；
- 文档化 allowed origin配置；
- 不扩大 Phase 8 到 auth系统。

Compose 可增加 dashboard/frontend profile/service，或清晰 npm dev流程。
不要提前做正式 Nginx/cloud deployment，Phase 10再处理生产发布。

## 8. Backpressure / Failure / Edge Cases

至少覆盖：

### WebSocket
- Redis UI-event channel unavailable
- business commit成功但 event publish失败
- API订阅断线/reconnect
- browser disconnect/reconnect
- slow client outbound buffer full
- 100+ event burst（不要求benchmark，但必须证明bounded memory）
- server shutdown with connected clients
- malformed client frames / oversized frames
- client不读取数据
- duplicate/out-of-order events
- client reconnect后 snapshot修复

### Dashboard consistency
- Job在列表打开后瞬间状态变化
- Job detail event在页面尚未加载时到达
- redrive/cancel与WS event交叉
- schedule cancel与materialization交叉
- Worker offline/recovery变化
- capability Job无匹配Worker时保持QUEUED
- retained legacy development DB仍schema 4时Dashboard不要被误部署到其上

### API
- empty worker list
- many workers bounded pagination
- summary with zero data
- DB unavailable → generic error
- Redis unavailable：authoritative REST仍以DB状态为准；WebSocket realtime可降级并明确连接状态

## 9. Testing

### Backend
至少：
- summary repository/service/API tests
- worker list pagination/order tests
- schedule list（如果新增）tests
- WebSocket hub unit tests
- slow client/backpressure tests
- graceful shutdown tests
- event serialization/version tests
- event publish failure不得回滚业务状态的 integration tests
- Redis Pub/Sub（若采用）real integration
- multi-process event fanout integration
- existing worker/scheduler/retry regression targeted tests
- race detector覆盖 hub/broadcast code

### Frontend
建立实际测试基础，不要只做 build：
- TypeScript typecheck
- unit/component tests（推荐 Vitest + React Testing Library，保持依赖小）
- API client error handling
- WebSocket reconnect/resync hook tests
- Jobs table/Job detail status update tests
- control action success/409/error tests
- no fake optimistic authority
- build

不要求 Phase 8 引入 Playwright，如果增加 E2E成本明显。若项目已有/轻易可用，可做一个真实浏览器 smoke；否则用构建 + frontend tests + HTTP/WebSocket integration + 手工/脚本 smoke即可，并如实记录。

### Final affected verification
建议：
- `go test -race -count=1 ./internal/service/... ./internal/infrastructure/... ./internal/transport/... ./tests/integration`
- `go test -count=1 ./...`
- `go vet ./...`
- `go build ./...`
- frontend install using lockfile
- `npm run typecheck`
- `npm test -- --run`（按实际脚本）
- `npm run build`
- phase-state validator
- `git diff --check`

NOT RUN / SKIP 必须如实记录。

## 10. Real Demo / Smoke

使用 isolated compatible DB + Redis namespace；retained development DB 的 legacy duplicate-key group仍不得自动修复/升级。

最少做：
1. 启动 API + Redis/Postgres + 2 workers + dashboard；
2. Browser/WS client取得 initial summary；
3. 提交 SLEEP Job；
4. 观察 QUEUED -> RUNNING -> SUCCEEDED UI更新；
5. 提交长SLEEP并Kill一个worker；
6. lease recovery后UI最终反映新Attempt和完成状态；
7. WebSocket连接中断，再恢复；
8. reconnect后通过snapshot拿到正确最终状态；
9. 制造DEAD_LETTER，Dashboard inspect attempts并redrive；
10. 验证Workers页面显示capabilities/status/active_jobs。

如果无法自动浏览器测试，至少写一个真实WebSocket integration/smoke client验证消息与reconnect，前端build/test PASS，并在report明确“browser E2E NOT RUN”而不是伪造。

## 11. Security / Privacy

当前项目无auth，这一点必须在Dashboard README明确：
- Dashboard是local/demo control plane；
- 不声称适合公网开放；
- Phase 10部署前必须重新评估auth/TLS/origin policy。

WebSocket：
- 限制Origin（至少dev allowlist / same-origin）；
- size/time/connection limits；
- event中不要包含完整payload/result，优先resource_id + status hint；
- 不broadcast credentials/raw DB errors。

Frontend：
- React默认escaping；
- JSON payload用text/pre展示，不用危险HTML；
- 不把secrets存入localStorage。

## 12. Retained development DB limitation

Phase 5–7已经记录 retained development DB：
- 有一个 legacy duplicate non-null idempotency-key group；
- schema停在4；
- migration 000005正确阻止升级。

Phase 8不得：
- 自动删除/合并/修改key；
- 绕过migration；
- 声称该DB已升级到Phase 8；
- 使用该DB作为Phase 8最终acceptance环境。

继续使用isolated schema/DB做后端和Dashboard demo。
用户若以后要修复该数据，必须单独明确授权。

## 13. Acceptance Criteria

Phase 8只有以下全部满足才可COMPLETED：

- [ ] React + TypeScript frontend真实存在并可build
- [ ] Overview / Jobs / Job Detail / Workers / Schedules / Dead Letter页面
- [ ] Dashboard数据来自真实REST/PostgreSQL state
- [ ] WebSocket endpoint + versioned event envelope
- [ ] multi-process状态变化可fan-out到WebSocket client
- [ ] WebSocket event不作为business authority
- [ ] reconnect后snapshot resync
- [ ] bounded per-client buffer / slow-client policy
- [ ] connection/read/idle/graceful-shutdown limits
- [ ] control actions继续走REST且server authoritative
- [ ] Job Attempt history真实展示
- [ ] 不伪造Phase 9 throughput/P95等metrics
- [ ] frontend tests/typecheck/build PASS
- [ ] backend realtime/race/integration tests PASS
- [ ] existing Phase 7 scheduling/capabilities regression无破坏
- [ ] retained legacy DB未被修改
- [ ] README/docs/ADR同步
- [ ] `docs/reports/phase-8-report.md`完整
- [ ] Git push/tag evidence完整
- [ ] completed state只在全部gates通过后发布
- [ ] 未提前实现Phase 9 Prometheus/Grafana/OTel/benchmark

## 14. Documentation

至少更新：
- README.md
- docs/architecture.md
- 新 `docs/dashboard.md` 或等价
- docs/worker-operations.md（只补realtime/操作相关必要内容）
- docs/development-roadmap.md
- docs/reports/index.md
- ADR：Dashboard realtime event semantics / WebSocket / Redis fanout / resync
- web/README.md
- docs/reports/phase-8-report.md

明确：
- PostgreSQL仍是truth；
- WebSocket仅realtime hint；
- reconnect/resync contract；
- slow client/backpressure policy；
- no auth / local-demo boundary；
- performance metrics属于Phase 9；
- retained DB limitation；
- no exactly-once claim。

## 15. Phase Automation Protocol

领取本Prompt时必须先发布ownership claim：
- `current_phase = 8`
- `status = in_progress`
- `prompt_source = automation`
- `prompt_path = automation/prompts/phase-8.md`
- `last_processed_phase = 7`
- `report = null`
- `commit = null`
- `tag = null`
- `next_prompt = null`

先push + reread，再开发。

Codex不得：
- 自行生成Phase 9 Prompt
- 自行推进`last_processed_phase = 8`
- 自动merge main
- 自动修复retained legacy DB

完成后生成`docs/reports/phase-8-report.md`，只记录真实tests/smoke/Git evidence。

## 16. Git / GitHub Safety

- 继续使用协议指定shared branch，除非明确迁移。
- 禁止force push / force-with-lease / reset --hard / history rewrite。
- 历史migrations/reports/prompts/tags不改写。
- frontend必须提交lockfile；不要提交`node_modules`、dist、coverage、.env或secrets。
- 不提交binaries、temp DB dumps、smoke artifacts。
- main不自动merge。
- 若沿用阶段tag，创建annotated tag，例如`phase8-dashboard-websocket`并验证target checkpoint。

## 17. Codex对话日志

开始：
`FlowForge Phase Start Log`
包含Phase 8、prompt path、execution id、branch、previous checkpoint、status IN_PROGRESS。

完成：
`FlowForge Phase Completion Log`
包含backend realtime + frontend摘要、真实tests/smoke、report、checkpoint/tag/push、state、Known Limitations、Waiting For external review。

阻塞：
`FlowForge Phase Blocked Log`
明确失败步骤、原因、测试/Git/state、partial commit和最小恢复动作。

核心原则：
**Phase 8的价值不是“做一个漂亮网页”，而是建立一个不会破坏核心correctness、能在多进程状态变化下实时演示FlowForge并在丢事件/断线后恢复一致视图的操作控制面。**
