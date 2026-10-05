# FlowForge Phase 3 — Multiple Workers + Bounded Concurrency

## Handoff metadata

- Repository: `Daniel-Cpz/FlowForge`.
- Shared entry/work branch: `codex/phase1-api-correctness`. Its historical name does not restrict the current phase number. Do not assume `main` contains this work.
- Prompt source: `automation`.
- Prompt path: `automation/prompts/phase-3.md`.
- Previous phase: Phase 2, COMPLETED.
- Reviewed repository revision: `e1c8fb784e5db3cbbe4d3a052ff09d656eeaa385`.
- Phase 2 implementation checkpoint: `657f47b4c0767f9ae63adc110d522e998fa9d1c4`.
- Phase 2 annotated tag: `phase2-single-worker`, verified to target that checkpoint.
- Evidence: `docs/reports/phase-2-report.md`, `README.md`, `docs/phase-automation.md`, `docs/development-roadmap.md`, `docs/worker-operations.md`, and the current worker implementation.
- Review time: `2026-10-05T03:03:22Z`.
- The GitHub Actions `checks` run for the reviewed revision reports `completed / success` (run `37226285788`, job `111506495188`). The earlier Phase 2 report does not claim that subsequently observed remote result. Its local integration, race and smoke results remain report evidence; the reviewer did not rerun them.

This is a development instruction, NOT a Phase 3 completion report. Execute only Phase 3 after successfully claiming it through the existing protocol. User authorization to continue does not authorize bypassing safety checks, force pushing, publishing secrets, deploying production, or changing unrelated projects.

## 1. 当前背景与目标

FlowForge 是 Go + PostgreSQL + Redis 的通用分布式任务执行平台，优先 Correctness > Feature Count、Reliability > UI Complexity。

Phase 2 已实现：Job 与 dispatch intent 同 PostgreSQL 事务提交、bounded outbox dispatcher、Redis Streams consumer group、单串行 Worker、QUEUED -> RUNNING 的数据库条件领取和独立 Attempt、owner/attempt 条件约束的终态持久化、终态之后 ACK、SLEEP Demo、失败与优雅退出测试。数据库是事实源，Redis 只携带版本和 Job ID。

本阶段仅将这一条真实链路扩展为：多个独立 Worker 进程，每个进程拥有显式限制的执行并发；在重复投递、竞争领取、依赖故障和退出时仍保持 Phase 2 的安全边界。不得将队列通知重发解释为 RUNNING 崩溃恢复。

先阅读当前代码、README、Phase 2 report、协议和 ADR，再给出简短实施计划。实际仓库若已进入 Phase 3 或更高阶段，不得再次领取或覆盖人工工作。

## 2. Scope

1. 每个 Worker 进程的固定容量执行池和严格的并发配置。
2. 同一 PostgreSQL 存储、同一 Redis stream/group 下多个独立 Worker 进程协作。
3. 清晰区分进程 worker_id、池内执行 slot 和 Redis consumer identity。
4. 保留并验证 PostgreSQL 原子领取、Attempt 一致性、重复投递处理和 ACK 顺序。
5. 多 dispatcher 并行时的重复发布安全性、错误处理和局部背压。
6. 池级故障处理、所有 goroutine 的生命周期和并发优雅退出。
7. 最小必要单元/真实服务集成/独立进程 smoke 验证。
8. README、操作文档、必要 ADR、阶段报告和既有 Automation 协议履约。

## 3. Out of Scope

不要实现 heartbeat、worker liveness registry、lease、stale-owner fencing、RUNNING crash recovery、XPENDING/XCLAIM/XAUTOCLAIM 驱动的恢复、业务 retry/backoff/jitter、提交幂等、优先级、用户取消和通用任务 timeout、DLQ、capability scheduling、WebSocket/Dashboard、Prometheus、Kubernetes 或云部署。

仍只执行已有 SLEEP Demo，不增加 HTTP_REQUEST、任意 shell 或 Docker 作业执行。不要重构 Automation schema、引入新的工作流引擎、生成 Phase 4 Prompt，或为了本阶段改用其他消息中间件。历史 migration 保持不可变；本阶段优先不新增数据库表或迁移。

## 4. 配置、身份与固定容量池

- 增加明确的 worker concurrency 配置，例如 `FLOWFORGE_WORKER_CONCURRENCY`，默认 `1`，允许范围 `1..32`。这是本阶段的安全上限，不是性能最优值。非法字符串、0、负数、溢出、超上限必须在启动时明确拒绝；同步 `.env.example`、配置测试和文档。
- 不从队列深度动态无限增加 goroutine。推荐固定 C 个消费/执行 slot，每个 slot 一次最多持有一个 delivery；只有 slot 可用才 Receive/Claim。也可采用单 reader + 有界通道，但必须证明预取和在途数都有明确上限，不能将消息堆在无限本地队列。
- 每个进程只创建一个运行实例 worker_id，启动时生成唯一 UUID；进程重启使用新实例 ID。同一进程内所有 Attempt 的 worker_id 表示该进程，不要给每个 Job 随意生成新的 worker_id。
- Redis consumer 可采用 `<worker_id>:<slot>`，使每个阻塞接收循环的 identity 唯一。所有进程共享同一个 stream/group，而不是每进程创建不同 group 造成全量广播。保持现有 version 1 消息格式。
- 明确最大 active execution、最大持有 delivery 和连接需求。Redis 阻塞读不能耗尽连接池而饿死 ACK/dispatcher；PostgreSQL pool 应能承受 C 个短事务。检查实际客户端配置，保留足够非阻塞连接，不为每个 Job 建立新连接池。
- 保持单个进程一个 dispatcher，而不是每个 execution slot 一个 dispatcher。业务执行不能持有数据库事务或连接等待 SLEEP。
- 每个共享对象必须说明并发安全性：store、queue、executor、logger 和状态计数。测试 fake/stub 也必须无数据竞争。

## 5. 多进程与数据库安全边界

- 至少支持两个真实独立 Worker 进程同时工作；默认单进程 concurrency=1 的行为应继续兼容。
- 领取仍以数据库条件更新作为裁决点。不能依靠先 SELECT 再无条件 UPDATE，也不能依赖进程内 mutex 保证跨进程唯一领取。
- QUEUED -> RUNNING 与创建 Attempt 必须在同一个事务完成。竞争失败者不得运行 executor、不得增加 attempt_count。唯一获胜者使用相同的 worker_id/attempt_number 完成后续持久化。
- Job 和 Attempt 的终态必须同事务持久化，并匹配获胜 owner 与 attempt；不能让另一个 Worker 终结不属于自己的 Attempt。
- 同一 Job 的重复 Redis 消息可有多个不同 MessageID。只 ACK 本次已处理/明确可丢弃的 delivery；不能因为看到 RUNNING 重复消息而 ACK 另一个 Worker 正在执行的原始 delivery。
- 保留已存在的 malformed/missing/non-QUEUED 消息处理规则。RUNNING/terminal duplicate 不重新执行；unsupported type/invalid SLEEP 是可持久化的业务失败，不应停止整个执行池。
- 终态成功提交后才 ACK 执行对应消息。Finalize 失败不得 ACK。终态提交后 ACK 失败，数据库仍保持真实终态，不能因此再次执行业务逻辑或把状态回退到 QUEUED。
- 对可能已提交但响应不明的 Claim/Finalize，明确保留当前阶段限制；不得通过重新执行或擅自重置状态来假装恢复。

## 6. 多 dispatcher 与局部背压

多个 Worker 进程各自运行一个 dispatcher 是本阶段允许的结构，不需要新增 leader election。检查并测试它们扫描相同 outbox 时的行为。

保留 Job/outbox 原子写入和 Phase 2 的 publication eligibility 规则。多个 dispatcher 可能重复 XADD；保证重复发布只增加通知，不增加同一 Job 的业务执行。发布成功、marker 写入失败的窗口必须仍然安全。不要为了避免重复而让数据库事务跨越 Redis 网络调用，也不要先标记已发布再发送。

扫描批量、执行并发、重试等待都必须有界；依赖不可用时禁止紧密空转。队列为空或没有可用 slot 时不能无限预取。此处只保证执行侧局部背压，不声称已经实现 API admission control、全局队列容量限制或历史清理。

## 7. 池级故障与 Graceful Shutdown

保持 Phase 2 已公开的退出语义：SIGINT/SIGTERM 停止领取，取消当前 SLEEP，尝试有界地持久化 FAILED/execution_cancelled，然后 ACK。不要悄悄改成另一套用户取消/超时 API。

- 统一由进程 supervisor 管理 dispatcher 和全部 slots。退出时先停止接收新工作并记录 draining，再取消/收尾已领取工作，等待全部 goroutine 结束，最后关闭共享依赖。
- 定义清楚边界：取消信号传播前已在途的 Receive/Claim 可能返回结果，必须妥善处理；不要声称不存在网络调用和取消的竞态。已返回但未领取的消息不能误标业务完成。
- 一个 slot 的 post-claim Finalize/ACK 致命错误触发整个进程停止继续领取、取消其余 slots、完成有界 cleanup，然后返回非零/明确错误；不能只丢掉一个 slot 后无声地永久降低池容量，也不能其他 slot 永远继续导致 Run 不返回。
- pre-claim 可重试依赖错误使用有界等待，不创建无上限重试 goroutine。正常业务失败不触发池级 fail-fast。
- 多个 slot 的 cleanup 共享清晰的进程退出 deadline，或并行运行且受统一总上限控制；不得串行累计为 C * 5 秒。测试 fake 和真实依赖不可用的路径都不能死锁。
- 所有 channel send、WaitGroup/error collection 和 blocking read 应可终止。取消后无法写入数据库的 Job 可以留在 RUNNING/pending，但必须如实记录，不能清除现场或宣称恢复完成。
- 对意外 executor panic 给出明确、安全的进程级处理策略，不打印潜在 payload/secrets、不静默重启重复执行。不要为此搭建通用插件隔离框架。

## 8. 结构化日志与可演示性

增加足以解释并发行为的结构化日志：worker_started、slot_started、job_claimed、job_finished、worker_draining、worker_stopped、pool_failed，或遵循现有命名。包含真实 worker_id、slot/consumer、job_id、attempt_number、configured_concurrency，必要时附 active_jobs。

计数必须并发安全，active_jobs 不能为负或超过 C。不要暴露原始 payload、密码、连接串或原始 driver error。不要把本地 draining 日志描述成已实现的分布式 Worker 状态管理。

提供可重复的双进程启动方法，例如确认 Compose 无固定 container_name 冲突后使用 `docker compose up -d --scale worker=2`，或使用仓库内受控 smoke 脚本启动两个 binary。先检查实际配置再写命令，不能给未经验证的命令打 PASS。

## 9. Failure / Edge Cases 与测试范围

按受影响模块运行最小必要测试，保留现有覆盖，不为每次小修反复跑全仓库大型回归。本阶段不是生产发布；只有更改范围确实需要时才扩大回归并说明原因。

必须覆盖：

1. concurrency 默认/边界/非法输入；C=1 兼容；C=2、C=4 真正同时执行且最大 active_jobs <= C。
2. 饱和池不会无限 Receive/Claim；释放 slot 后继续消费。使用 barrier/channel 与有上限 context，不以短 sleep 和脆弱耗时阈值作为主要正确性断言。
3. 两个 Worker + 同一 Job 多条消息/同时 Claim：只允许一个 executor 调用、一个 Attempt，owner 一致；所有统计和 fake 通过 race 检查。
4. 多 Job 并行完成且每个 Job/Attempt 终态一致；失败 Job 不污染其他 Job；不要求 Redis 对进程严格均匀分配或 FIFO 完成。
5. 多 dispatcher、Redis 发布成功后 marker 失败、Redis outage 后 queued intent 重发：重复通知不产生重复执行。
6. Finalize 失败不 ACK；ACK 失败不重执行；一个 slot 发生致命错误能够停止整个池并等待其余 slot 完成有界退出。
7. idle/saturated/active 多任务情况下 SIGTERM；发送方/接收方/dispatcher 都退出；依赖关闭发生在 goroutine 收尾之后；不会遗留测试 goroutine。
8. poison/missing/RUNNING/terminal duplicate、invalid SLEEP、unsupported type 回归。
9. 真实 PostgreSQL + Redis 的多 Worker 集成，不以 mock 替代全部跨进程证据。沿用随机 schema 和 stream namespace，检查连接 schema 隔离，不使用 FLUSHDB/FLUSHALL。
10. 至少一次两个真正独立 Worker 进程的 smoke：有足够 SLEEP Jobs，使两个不同 worker_id 都实际执行；记录配置、进程数、Job/Attempt 数、观察到的并发上限和退出结果。单进程内实例测试不是多进程证据。
11. 停止一个 Worker 后存活进程仍能处理其他 QUEUED 工作；不要求且不得伪称接管被强杀进程的 RUNNING Jobs。若进行强杀演示，只在隔离环境展示并记录尚无恢复的限制。

建议从 `go test -count=1 ./internal/config ./internal/service/execution ./internal/service/dispatch` 开始，按实际修改增加 queue/store/app 的针对性测试；对受影响并发模块运行 `go test -race`；在现有 tools image 中运行筛选后的真实 integration tests、相关 `go vet` 和 worker build。测试名、包路径和命令必须以实际仓库为准。需要 Go 而本机未安装时沿用 Docker 工具，不擅自全局安装/升级环境。

报告必须逐项写命令、环境、PASS/FAIL/NOT RUN/SKIP；没有执行的测试不得写成通过。吞吐和扩展性不作无数据的性能声明；本阶段主要验证并发正确性而非大规模 benchmark。

## 10. README / 文档更新

更新 README、docs/worker-operations.md、architecture/lifecycle/roadmap 中受影响部分，必要时新增一个简短 ADR 说明固定池、process/consumer identity、多 dispatcher 与 fail-fast/退出策略。

明确 Implemented / Experimental / Planned：Phase 3 的多进程与有界并发只有经测试后才写 Implemented；Phase 4 heartbeat/lease/recovery 仍 Planned。修正 README 中过时的“当前 Phase 1 manual”等描述，但保留历史报告事实，不覆盖历史阶段报告。

写清配置取值、每进程 C 与总容量的关系、连接开销、启动两个 Worker 的实际方法、故障诊断和剩余 RUNNING crash limitation。不新增全局业务重试、吞吐 SLA、Exactly-Once 或高可用承诺。

## 11. Phase Automation Protocol 与领取

严格使用现有 schema_version=1，不添加 execution_id/lease 等 state 字段。运行 ID 可以写入报告和日志，不代表分布式锁。

自动领取前必须在共享分支最新远程状态确认：current_phase=2、status=completed、last_processed_phase=2、next_prompt=automation/prompts/phase-3.md，且没有另一个运行已领取 Phase 3。先安全 fetch；存在不明未提交人工修改或分支分歧则 BLOCKED，禁止 reset/stash 丢弃用户工作。

领取时按现有协议在同一共享分支提交并成功推送：current_phase=3、status=in_progress、prompt_source=automation、prompt_path=automation/prompts/phase-3.md、report/commit/tag/next_prompt=null、last_processed_phase 保持2、真实 branch 和 UTC updated_at。只有远程领取成功且重新读取确认后才开始开发。冲突时重新读取而不是 force push；若其他执行者已经领取，不重复执行。普通唯一运行 ID 不能代替条件更新。

当前 Phase 3 在此交接提交发布时尚未开始；GPT 不代替 Codex 提交领取。保留历史 prompts/reports。不得静默混入新自动提示词改变进行中的 scope；用户显式修改应记录。

## 12. Git / GitHub 安全提交、完成与报告

本次开发目标仅为用户的 `Daniel-Cpz/FlowForge`，沿用共享分支 `codex/phase1-api-correctness`。禁止 force push、force-with-lease、reset --hard、重写已发布历史、自动合并 main 或部署服务器。

使用明确文件列表提交，检查 git status、staged diff、git diff --check、secrets、.env、缓存、构建产物、大文件和无关人工变更。不要为了整理历史改写旧提交。任何审批/网络/分支保护阻塞均停止相应写入并保留真实状态，不更换通道绕过限制。

阶段完成时先完成必要测试，再生成独立 `docs/reports/phase-3-report.md` 并同步 README/report index。Report 遵循当前模板，包含 Prompt Source、Scope、Implemented/Not Implemented/Experimental/Planned、Commands/Results、真实多进程与并发证据、Failure Cases、Limitations、Git 和下一阶段建议。

先提交实现与报告得到真实 checkpoint；按现有 annotated tag 约定创建例如 `phase3-multi-worker` 的新标签，检查无同名冲突。使用后续 metadata commit 记录 checkpoint、报告 Git 引用和 completed state；文件不声称含有自己的提交 SHA。最终 current_phase=3、status=completed、report=docs/reports/phase-3-report.md、prompt_source=automation、prompt_path=automation/prompts/phase-3.md、last_processed_phase=2、next_prompt=null。

发布前运行现有 phase-state validator；push 后核验分支和标签。push 失败不声称远程同步。不得由 Codex 把 last_processed_phase 推为3，也不得生成 Phase 4 Prompt。等待外部审查。

## 13. 验收标准

- 两个独立 Worker 进程在同一存储/stream/group 下成功执行 Jobs。
- 每进程 concurrency 有效且严格有界，默认1；无无限 goroutine/无界预取。
- 进程身份与池 slot/Redis consumer identity 清楚，Attempt owner 正确。
- 同 Job 竞争领取只有一个实际 executor 调用/Attempt；终态与 ACK 顺序正确。
- 多 dispatcher 的重复发布安全，重试等待有界；业务失败和基础设施致命失败策略不同且有测试。
- pool fatal error、并发取消、依赖失败均能有界收尾，无静默槽位丢失和死锁。
- 必要 unit/race/真实服务/独立进程 smoke 有真实结果；未扩大无关 scope。
- README/ADR/worker operations/report/state 一致，明确未实现 RUNNING crash recovery。
- 规定 Git 安全检查、完成提交/标签/远程核验通过；未动 main/生产。

## 14. Codex 对话日志

领取成功后输出 `FlowForge Phase Start Log`：Phase 3、实际时间、Prompt 路径、共享分支、领取提交、进程外的本次开发运行 ID、IN_PROGRESS。

完成后输出 `FlowForge Phase Completion Log`：Phase、实现摘要、worker 数和 concurrency、测试命令/结果、报告路径、implementation checkpoint、metadata commit、tag、远程发布结果、state 字段、限制、Waiting for GPT review。不要用“Prompt 已读”代替领取成功，也不要用“代码写完”代替完成验收。

阻塞输出 `FlowForge Phase Blocked Log`：实际失败步骤、受影响文件、已跑测试、Git 状态、是否 commit/push、仍可保留的证据和恢复动作。保留原始错误的安全摘要，不泄露凭据。不要伪造 completed，不自主扩大到下一 Phase。
