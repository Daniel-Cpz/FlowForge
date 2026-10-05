# FlowForge Phase 10 — Cloud Deployment + CI/CD + Release Hardening

## Handoff metadata
- Repository: `Daniel-Cpz/FlowForge`
- Shared entry/work branch: `codex/phase1-api-correctness`
- Prompt source: `automation`
- Prompt path: `automation/prompts/phase-10.md`
- Previous phase: Phase 9, COMPLETED
- Reviewed branch revision: `abc58f07ae70ae7ac0489473feeaef25a32e56fa`
- Phase 9 implementation/report checkpoint: `718e9c2a874c5ad6f4ee8ac9537174065c086646`
- Phase 9 annotated tag: `phase9-observability-benchmarks`, verified to target that checkpoint
- Phase 9 metadata revision GitHub Actions `checks`: completed / success
- Review time: `2026-10-05T10:37:43Z`

This is a development instruction, NOT a Phase 10 completion report. Execute only Phase 10 after successfully claiming it through the existing Phase Automation Protocol.

Phase 10 is the final numbered roadmap phase. Do not invent or start Phase 11. After Phase 10 completion, wait for external review and an explicit user decision about any post-roadmap work or final main-branch release.

## 1. 当前背景

FlowForge 已真实实现并测试：

- PostgreSQL authoritative job/schedule/worker state
- durable DB dispatch intent + Redis Streams notification
- bounded multi-worker execution
- heartbeat / lease / stale-owner fencing / crash recovery
- retry budget / exponential backoff / jitter / DEAD_LETTER
- submission idempotency
- non-preemptive priority / attempt timeout / durable cancellation / redrive
- delayed Jobs / capability-aware Claim / recurring fixed-interval schedules
- React + TypeScript Dashboard
- bounded WebSocket invalidation + REST snapshot repair
- Prometheus + Grafana local/demo observability
- bounded OTLP tracing with durable async correlation
- repeatable failure injection
- load generator + 1/4/8/16 Worker benchmark evidence

Phase 9 benchmark明确表明：
- 1 Worker mean ≈ 33.16 jobs/s
- 4 Workers mean ≈ 56.94 jobs/s，但波动很大
- 8 Workers mean ≈ 140.39 jobs/s，同样存在明显波动
- 16 Workers mean ≈ 37.98 jobs/s
- 不能声称线性或稳定水平扩展

因此 Phase 10 **不得根据 benchmark盲目把production worker数量设为16**。Cloud demo使用保守、显式配置，例如 1–2 Worker / C=1，根据实际VPS资源决定并记录。

当前本地 Compose 是 development-oriented：
- PostgreSQL / Redis / API / Dashboard host ports只绑定loopback
- Dashboard使用Vite dev server
- observability默认local/demo
- 应用目前无application-level auth
- retained development DB仍有一个legacy duplicate idempotency-key group且schema=4；migration 000005正确拒绝自动升级

Phase 10 的目标是把已经验证过的系统做成**真实可部署、可回滚、可恢复、可解释的单节点Cloud release**，而不是引入 Kubernetes/Terraform 或大规模基础设施。

## 2. 本阶段目标

交付并验证：

1. Ubuntu VPS / AWS EC2-compatible single-host production deployment；
2. production Docker Compose，与开发 Compose 清晰分离；
3. production-built React static frontend + reverse proxy；
4. HTTPS 或明确的private-tunnel模式；
5. public mode下必须有最小访问控制；
6. PostgreSQL / Redis / metrics / Grafana / OTLP 等内部端口不直接公网暴露；
7. immutable GitHub Container Registry images；
8. GitHub Actions CI / image release / controlled deploy workflow；
9. migration + backup + health gate + safe rollback policy；
10. restart/reboot persistence；
11. real cloud end-to-end smoke；
12. README / deployment runbook / final Phase 10 report。

**如果没有明确授权的VPS/EC2目标或必要GitHub Environment secrets，禁止伪造cloud deployment成功。**
这种情况下：
- 完成代码、production Compose、CI/CD、local release smoke；
- state保持 `in_progress` 或根据现有协议记录 `blocked`；
- 在Codex对话输出 `FlowForge Phase Blocked Log`，准确列出缺失的host/domain/secrets/approval；
- 不得把Phase 10标记COMPLETED。

Phase 10的真正COMPLETED必须包含至少一次真实授权cloud host部署证据。

## 3. Deployment target

默认目标：
- Ubuntu 24.04 LTS 或兼容Ubuntu VPS / AWS EC2
- amd64
- Docker Engine + Docker Compose plugin
- 单host
- 一个private Docker network
- persistent named volumes

不要加入：
- Kubernetes
- Terraform
- Ansible
- Nomad
- ECS/EKS
- Helm

这些不是当前项目证明Cloud deploy所必需。

### 3.1 Host modes

支持并文档化两种明确模式：

#### A. Public demo mode
必须同时满足：
- domain已指向host
- HTTPS
- single public gateway
- Dashboard/API/WebSocket受最小access control保护
- 80/443以外应用端口不公网开放

#### B. Private cloud mode
当没有domain或不希望public exposure：
- 只开放SSH
- gateway/dashboard/API绑定127.0.0.1或仅private Docker network
- 用户通过SSH tunnel访问
- 不声称public production deployment

**禁止**使用“公网IP + 明文HTTP + 无认证Dashboard/API”作为Phase 10 completion evidence。

## 4. Production gateway

当前frontend是Vite local/dev server。Phase 10必须提供真实production static build。

推荐使用**一个轻量reverse proxy/gateway**统一：
- React static assets
- SPA fallback
- `/api/*` reverse proxy to API
- WebSocket upgrade
- TLS
- public mode access control

推荐 **Caddy**，因为它可以在一个组件中完成static serving + reverse proxy + WebSocket + automatic HTTPS + Basic Auth，减少Certbot/Nginx额外编排。

如果实际实现选择Nginx，必须说明具体理由；不要同时加入Caddy和Nginx。

建议新增：
- `deploy/gateway/Dockerfile`
  - Node build stage使用lockfile生产build
  - runtime gateway image只包含dist + gateway config
- `deploy/Caddyfile` 或等价

### 4.1 Access control

FlowForge尚无application auth。

Public mode必须至少通过gateway提供：
- HTTP Basic Auth 或等价小型边界access control
- username + bcrypt hash来自server/GitHub secret
- 不提交plaintext password/hash到Git

Private mode可依赖SSH tunnel/firewall，不要求额外Basic Auth。

不要在Phase 10实现完整users/session/OAuth系统；那不是项目核心。

### 4.2 Origin / WebSocket

Public mode：
- `FLOWFORGE_WS_ORIGINS=https://<public-host>`
- REST + WebSocket same-origin
- 禁止 `*`
- Browser与WS都通过gateway

## 5. Production Docker Compose

新增独立文件，例如：

`deploy/compose.prod.yml`

不要把现有development Compose改成难以本地开发的production文件。

至少包含：
- gateway
- api
- worker
- migrate
- postgres
- redis

可选profile：
- prometheus
- grafana
- otel-collector

### 5.1 Network exposure

默认：
- gateway: public mode只暴露80/443；private mode只loopback/private
- api: no public host port
- postgres: no public host port
- redis: no public host port
- worker metrics: no public host port
- Prometheus/Grafana/OTLP: no public host port；通过SSH tunnel访问
- Postgres/Redis只在private Docker network

### 5.2 Production env

`FLOWFORGE_ENV=production`

当前代码禁止production PostgreSQL `sslmode=disable`，不得为了部署方便删掉此保护。

单host local PostgreSQL方案要求：
- PostgreSQL TLS真正开启；
- 可以使用server-local self-signed certificate + `sslmode=require`；
- cert/key在server bootstrap时生成；
- key mode 0600；
- 不提交private key；
- cert/key只mount到Postgres；
- app通过private Docker network加密连接。

如果明确使用managed PostgreSQL，也可以使用provider TLS；但不要为了关键词主动引入RDS。

Redis：
- production必须非空strong password；
- 不公网暴露；
- 单host private network可不做Redis TLS，但文档必须明确：未来multi-host必须重新评估TLS。

### 5.3 Process safety

Backend image继续non-root。
建议：
- `restart: unless-stopped`
- `init: true`
- container healthchecks
- bounded log rotation
- explicit stop_grace_period
- no privileged containers
- no Docker socket mount
- app容器不mount host source code
- production Compose不使用build context；只pull immutable images

Worker数量：
- 通过显式 `FLOWFORGE_WORKER_COUNT` / deploy script `--scale worker=N`
- 默认保守值，例如2
- concurrency默认1
- 文档引用Phase 9 benchmark，说明没有稳定线性扩展证据

## 6. Immutable images + GHCR

GitHub Actions build至少发布：

### Backend image
一个现有backend runtime image：
- api
- worker
- migrate
- loadgen

### Gateway image
production frontend build + gateway runtime。

Push到GHCR，例如：
- `ghcr.io/daniel-cpz/flowforge-backend:<commit-sha>`
- `ghcr.io/daniel-cpz/flowforge-gateway:<commit-sha>`

要求：
- 部署不使用mutable `latest`
- 记录commit SHA
- 优先使用digest或immutable SHA tag
- build image中加入OCI source/revision labels
- production Compose image ref由deploy env提供
- CI测试的镜像与部署镜像应来自同一build/revision，避免server现场rebuild

不要为了Phase 10加入复杂image signing/SBOM平台；如果简单生成SBOM可以作为bonus，但不是completion gate。

## 7. GitHub Actions CI/CD

保留并增强现有CI，不要降低已有测试。

建议拆分：

### `.github/workflows/ci.yml`
继续：
- Go full checks
- race
- frontend typecheck/tests/build
- observability config validation
- harness safety

增加production config validation：
- gateway config parse/test
- `docker compose -f deploy/compose.prod.yml config`
- deploy scripts syntax
- production image build smoke

### `.github/workflows/release-images.yml`
触发：
- `workflow_dispatch`
- 可选未来version tag

权限最小：
- contents: read
- packages: write

Build and push backend/gateway immutable SHA images到GHCR。
输出image SHA/digest用于deploy。

### `.github/workflows/deploy.yml`
只允许：
- `workflow_dispatch`
- GitHub Environment，例如 `flowforge-cloud`
- deploy concurrency group，禁止两个deploy并发

不要在普通push/PR自动部署cloud。

需要Environment secrets：
- `FLOWFORGE_DEPLOY_HOST`
- `FLOWFORGE_DEPLOY_USER`
- `FLOWFORGE_DEPLOY_SSH_KEY`
- `FLOWFORGE_DEPLOY_KNOWN_HOSTS`
- 必要public host / access-control / application secret inputs

实际secret名称可以优化，但必须文档化。

SSH要求：
- 使用known_hosts pinning
- 禁止 `StrictHostKeyChecking=no`
- private key只在runner临时文件，0600，finally删除
- 不echo secret
- 不提交server `.env.prod`

如果使用GHCR private image：
- workflow可在deploy期间临时让remote Docker login拉取
- 使用短生命周期workflow token /明确deploy credential
- pull完成后logout
- 不把token写入repo或日志

## 8. Server directory layout

建议：

`/opt/flowforge/`

例如：
- `compose.prod.yml`
- `Caddyfile`
- `.env.prod` (0600, untracked)
- `secrets/`
- `backups/`
- `releases/`
- current deployed SHA metadata

远程deploy不依赖git clone整个repo。
Workflow将versioned deployment bundle + immutable image refs复制到server。

## 9. Deployment sequence

实现例如：

`deploy/scripts/deploy.sh`

必须：
1. acquire local deploy lock，避免并发
2. validate required env/files
3. check disk space / Docker / Compose
4. pull exact candidate image refs
5. record previous image refs
6. create PostgreSQL pre-migration backup
7. stop/drain API + Workers，避免mixed-version migration
8. run one-shot migrate up
9. start API + configured Worker count
10. start/update gateway
11. wait bounded health/readiness
12. verify internal API + public/private gateway
13. on success atomically record current release metadata
14. remove deploy lock

### Important migration rule

**禁止自动 `migrate down` 作为rollback。**

如果new app startup失败：
- 可以回到previous application image only when forward schema compatibility is known/verified；
- otherwise stop deployment and require operator decision/backup restore。

No zero-downtime claim。
Short maintenance downtime is acceptable and should be documented.

## 10. PostgreSQL backup / restore

PostgreSQL是authoritative source，因此Cloud release必须有最小real backup证据。

新增：
- `deploy/scripts/backup.sh`
- documented restore command / script

要求：
- `pg_dump -Fc` 或等价consistent logical backup
- backup filename含UTC timestamp + deployed SHA
- file permissions restrictive
- simple retention，例如last 5/7 backups；不要无界增长
- backup失败 -> deployment abort before migration
- 不将backup提交Git

### Restore test

必须在disposable PostgreSQL instance验证：
1. create sample FlowForge state
2. pg_dump
3. restore into a new disposable DB
4. run schema validation/readiness
5. verify representative Jobs/Attempts/Schedules counts and IDs
6. cleanup disposable restore DB

不要在自动acceptance中破坏真实cloud production DB。

## 11. Firewall / Host hardening

提供 `docs/deployment.md` runbook。

至少：
- non-root deploy user in docker group（说明docker group≈root-equivalent）
- SSH key only
- disable password SSH login if operator controls host
- UFW / cloud security group:
  - 22仅允许operator source range where practical
  - 80/443 only public mode
  - 5432/6379/8080/9090/9091/3000/4318不要公网开放
- automatic security updates documented/recommended
- server timezone can be UTC; application already uses UTC
- enough disk for Docker + PG backups
- log rotation

不要自动修改用户其他服务的firewall；deploy bootstrap只能在明确授权host上操作，并报告changes。

## 12. Observability in Cloud

Phase 9 stack继续作为optional profile。

Production/cloud rule：
- Prometheus/Grafana/Collector不公网暴露
- 通过SSH tunnel访问Grafana/Prometheus
- Grafana anonymous viewer只允许在private/loopback exposure下
- tracing collector only private Docker network
- telemetry outage不能影响business state

Cloud completion至少验证：
- API `/metrics` internal reachable
- Worker metrics internal reachable
- 如果启用observability profile：Prometheus targets UP + Grafana dashboard load

小内存VPS可以不常驻Grafana/Prometheus；若禁用必须如实记录，而不能声称cloud observability已部署。

## 13. CI/CD safety / rollback behavior

必须覆盖：
- deployment workflow concurrency
- immutable release SHA
- wrong/missing secret failure
- SSH host key mismatch failure
- remote preflight failure
- image pull failure
- backup failure
- migration failure
- API readiness failure
- gateway health failure
- partial deployment cleanup
- deploy lock cleanup
- previous release metadata preservation

实现local/fake-host或shell-level tests验证deploy state machine核心路径。
不要通过危险mock命令触碰真实host。

## 14. Real Cloud Acceptance

Phase 10真正COMPLETED必须有真实授权Ubuntu VPS/EC2证据。

记录但不要在report中泄露：
- provider/type（例如 generic Ubuntu VPS / AWS EC2）
- OS
- CPU/RAM
- Docker/Compose版本
- public/private mode
- worker count/concurrency
- deployment SHA
- whether observability profile enabled

不要记录：
- public IP if user prefers private
- SSH key
- password/hash
- tokens

### Required cloud smoke

至少：

1. deploy fresh compatible schema from immutable images
2. migration reaches latest schema (including 000008)
3. gateway access control works：
   - unauthenticated public request denied in public mode
   - authenticated request succeeds
   - private mode proves no public app listener
4. Dashboard static assets load from production gateway
5. REST API / WebSocket through gateway works
6. submit SLEEP Job -> SUCCEEDED
7. run >=2 Workers or documented conservative configured count
8. kill one Worker during long SLEEP
9. lease recovery/new Attempt/final success
10. WebSocket reconnect + REST resync
11. schedule/capability basic smoke
12. DEAD_LETTER inspect/redrive basic smoke
13. internal metrics reachable; public metrics not exposed
14. restart Docker services or reboot host（if authorized）
15. verify PostgreSQL data persists and previous Job remains readable
16. create and restore-test backup in disposable DB

### CI/CD evidence

Completion还需要至少一次成功的GitHub Actions deploy workflow run，
或等价GitHub Environment deployment evidence。

如果Codex环境无法trigger该workflow：
- 不伪造；
- 输出Blocked Log；
- 等待用户手动 `workflow_dispatch`；
- 完成后再读取Actions结果并继续finalize Phase 10。

## 15. Release / main branch policy

**Phase 10不得自动merge `main`.**

当前shared branch承载Phase 1–10自动接力，main仍是早期foundation。

Phase 10完成后：
- 创建正常implementation/report checkpoint
- annotated tag，例如 `phase10-cloud-cicd`
- publish completed state on shared branch
- 等待external review
- 不生成Phase 11
- 在completion log中给出“recommended final fast-forward merge / release”但等待用户明确授权

不要自动创建 `v1.0.0` tag，除非用户明确授权最终release version。

## 16. Retained development DB limitation

继续遵守真实历史：

retained development DB：
- schema=4
- one legacy duplicate non-null idempotency-key group
- migration 000005正确拒绝升级

Phase 10不得：
- 自动删除/合并/改key
- bypass migration 000005
- 把该DB作为cloud seed
- 声称它已升级

Cloud deployment必须使用：
- fresh compatible DB
或
- 用户另行明确授权清理后的DB

Phase 10完成前做readonly retained-data audit，确认它未被本阶段修改。

## 17. Out of Scope

不要实现：
- Kubernetes
- Terraform
- multi-region
- HA PostgreSQL cluster
- Redis Cluster
- managed RDS/ElastiCache unless user explicitly chooses
- autoscaling
- blue/green / canary platform
- full OAuth/user management
- secrets manager integration
- zero-downtime migrations
- automatic DNS registrar changes
- production SLA
- new business Job types
- Phase 11

## 18. Testing

### CI / local production-stack tests

至少：
- existing Go full/race tests
- frontend typecheck/tests/build
- production gateway config validation
- production Compose config
- backend/gateway image build
- immutable image smoke
- API health/readiness
- WebSocket proxy through gateway
- basic auth/public-mode gateway test
- private-mode bind/exposure test
- PostgreSQL TLS handshake test in production config
- Redis password required test
- backup/restore disposable DB test
- deploy script state/lock/failure-path tests
- phase-state validator
- `git diff --check`

### Existing regressions

重点重新验证：
- lease/crash recovery
- retry/idempotency
- cancellation/DLQ
- schedule/capability
- Dashboard/WS
- Prometheus/OTel critical tests

不要每次无意义跑Phase 9 heavy benchmark；Phase 10不需要重新做1/4/8/16 matrix，除非本阶段修改了调度/worker性能关键路径。

## 19. Acceptance Criteria

Phase 10只有全部满足才可COMPLETED：

- [ ] production Docker Compose独立于development Compose
- [ ] frontend使用production static build，不使用Vite dev server
- [ ] single gateway正确proxy REST + WS
- [ ] public mode HTTPS + access control，或private-only SSH tunnel模式
- [ ] DB/Redis/API/metrics/Grafana/OTLP不直接公网暴露
- [ ] `FLOWFORGE_ENV=production`
- [ ] PostgreSQL production TLS真实工作
- [ ] Redis strong password + private network
- [ ] immutable backend/gateway GHCR images
- [ ] existing CI不降级
- [ ] release-images workflow PASS
- [ ] controlled deploy workflow存在并经过真实成功run
- [ ] known_hosts/SSH secret handling安全
- [ ] backup before migration
- [ ] disposable restore test PASS
- [ ] migration/health failure有安全abort/rollback policy
- [ ] actual authorized Ubuntu VPS/EC2 deployment PASS
- [ ] cloud SLEEP end-to-end PASS
- [ ] cloud Worker crash/recovery PASS
- [ ] cloud Dashboard/WS PASS
- [ ] restart/reboot persistence evidence
- [ ] cloud metrics internal-only evidence
- [ ] retained schema-4 legacy DB未被修改
- [ ] README/deployment docs/CI-CD docs同步
- [ ] `docs/reports/phase-10-report.md`完整
- [ ] Git checkpoint/tag/push evidence完整
- [ ] no automatic main merge
- [ ] no Phase 11 prompt

如果实际cloud target或secrets缺失，**不得勾选cloud acceptance，也不得completed**。

## 20. Documentation

至少新增/更新：

- README.md
- docs/architecture.md
- docs/dashboard.md
- docs/observability.md
- docs/worker-operations.md
- docs/development-roadmap.md
- docs/reports/index.md
- `docs/deployment.md`
- `deploy/README.md`
- ADR：single-host cloud release / gateway / image immutability / deploy-migration-backup policy
- `docs/reports/phase-10-report.md`

文档必须说明：
- production/private/public模式
- access-control boundary
- TLS boundary
- Postgres/Redis exposure
- exact deploy flow
- backup/restore
- rollback limits
- no automatic migrate down
- GHCR image strategy
- GitHub Environment secrets
- actual cloud host evidence
- observability exposure
- Phase 9 benchmark limitations
- retained DB limitation
- main尚未自动merge
- roadmap 0–10结束，后续工作需新决策

## 21. Phase Automation Protocol

领取本Prompt时先发布ownership claim：

- `current_phase = 10`
- `status = in_progress`
- `prompt_source = automation`
- `prompt_path = automation/prompts/phase-10.md`
- `last_processed_phase = 9`
- `report = null`
- `commit = null`
- `tag = null`
- `next_prompt = null`

先push + reread，再开始开发。

Codex不得：
- 自行生成Phase 11 Prompt
- 自行推进 `last_processed_phase = 10`
- 自动merge main
- 自动创建 `v1.0.0`
- 猜测cloud host/credentials
- 自动修复retained legacy DB

若缺少真实cloud target / GitHub deployment secrets：
- 不要把Phase 10 completed
- 输出Block Log
- 保留已完成的infra/CI代码
- 等待明确operator input

完成后生成：
`docs/reports/phase-10-report.md`

必须包含：
- local production-stack tests
- image digests
- GitHub Actions release/deploy run evidence
- cloud environment摘要（不含secret）
- cloud smoke
- backup/restore test
- persistence/restart evidence
- known limitations
- Git evidence
- roadmap exhausted / no Phase 11

## 22. Git / GitHub Safety

- 继续shared branch，除非明确迁移。
- 禁止force push / force-with-lease / reset --hard / history rewrite。
- 不修改历史migration/report/prompt/tag。
- 不提交server `.env.prod`、cert private key、SSH key、Basic Auth hash、GHCR token、backup、Docker volume、Grafana DB、Prometheus TSDB。
- 不自动main merge。
- 创建annotated `phase10-cloud-cicd` tag only after全部completion gates。
- Release image tag必须immutable SHA；不要用latest作为部署事实。

## 23. Codex 对话日志

开始：
`FlowForge Phase Start Log`

至少包含：
- Phase 10
- prompt path
- execution id
- branch
- previous checkpoint
- status IN_PROGRESS
- cloud target availability：AVAILABLE / MISSING（不显示secret）

如因target/secrets缺失：
`FlowForge Phase Blocked Log`
包含：
- 已完成local/CI内容
- 缺失operator inputs名称
- state仍未completed
- next exact user action
- 不泄露任何credential

完成：
`FlowForge Phase Completion Log`
包含：
- production stack摘要
- GHCR image refs/digests
- CI/release/deploy workflow results
- cloud environment摘要
- cloud smoke
- backup/restore
- restart persistence
- tests
- Phase 10 report
- checkpoint/tag/push
- state
- Known Limitations
- **Roadmap 0–10 complete; waiting for external review; no Phase 11 generated**
- recommended final main fast-forward/release only，等待用户明确授权

核心原则：

**Phase 10不是“把Compose丢到服务器上”。它必须证明同一套经过Phase 1–9验证的正确性，在一个真实、受保护、可重复部署的Cloud环境中通过immutable build、migration、backup、health gate和CI/CD流程被安全发布，并且失败时不会靠猜测或破坏数据恢复。**
