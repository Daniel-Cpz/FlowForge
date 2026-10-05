# 0004 — Fixed worker slots and process supervision
Status: Accepted

## Context

Phase 2 established durable dispatch, conditional claim and terminal-before-ACK.
Phase 3 needs multiple processes and bounded execution without weakening those
transactions or adding recovery semantics. This supersedes only ADR 0003's
single serial executor restriction; its database/queue boundaries still apply.

## Decision

Each process constructs one UUID worker instance and C fixed consumer/executor
slots, where FLOWFORGE_WORKER_CONCURRENCY is a decimal integer in 1..32, default 1.
A slot receives COUNT 1 only when free and holds that delivery through local
pre-claim retry, execution, finalization and ACK. There is no prefetch channel.
At most C deliveries and C claimed executions are held in each process. Redis
consumer names are <worker_id>:<zero-based slot>; all processes share the same
stream and flowforge-workers-v1 group. Restart creates a new worker UUID.

One supervisor owns C slots, one cancellation watcher and one dispatcher per
process. A post-claim persistence/ACK failure or unexpected panic drains the
whole process with a static error. Panic values/stacks are not logged. Business
FAILED outcomes keep the pool running. The supervisor logs draining before
canceling execution, sets one five-second cleanup cancellation timer, and joins
all goroutines before app.Run closes clients. Finalize+ACK has a five-second
operation budget additionally limited by the shared cleanup context. Existing
PostgreSQL rollback may take its separate bounded five-second cleanup; these
run in parallel, never sequentially C times. Compose grants fifteen seconds.
Adapters and executors must honor cancellation; arbitrary untrusted executors
are outside this phase. A panicking claimed job remains RUNNING/pending.

pgxpool and go-redis clients are shared, concurrency safe, and created once.
Worker PostgreSQL maximum is C+2 connections; Redis PoolSize and MaxActiveConns
are both C+4, leaving room beyond at most C blocking reads. Every slot owns its
Job pointer; repositories and queues have no mutable per-call shared state.
SLEEP is stateless, slog handles concurrent writes, and active_jobs uses atomics.
Concurrent test doubles protect shared state with mutexes/atomics and barriers.

Multiple process dispatchers may publish the same durable intent. No leader or
long database transaction is introduced. Existing SQL CAS and owner/attempt
finalization decide execution; rejected duplicate deliveries ACK only their own
MessageID. Published-but-unmarked and still-QUEUED republication retain their
previous rules (100 per scan, one-second cycles, 30-second eligibility).

## Consequences

N live processes provide at most N*C local execution capacity; this is not API
admission control, a global queue limit, strict distribution, FIFO completion or
a performance claim. Operators must budget N*(C+2) PostgreSQL connections plus
API's ten and other clients, and N*(C+4) Redis sockets plus other processes.
Consumers/stream/outbox history still need a later retention policy.

SIGTERM can race with in-flight Receive or Claim. A returned unclaimed delivery
is left unresolved; a successful Claim observes cancellation and attempts FAILED
persistence. An ambiguous commit, failed persistence or abrupt crash can leave
RUNNING indefinitely. Terminal commit followed by failed ACK stays terminal.
There is no heartbeat, lease, liveness registry, recovery or exactly-once claim.

## Alternatives

A single reader with bounded queue/semaphore could also bound work, but fixed
slots make held-delivery and blocking-connection limits easier to explain.
Per-slot dispatchers add unnecessary duplicate scanning; leader election adds
unneeded coordination. A process-local claim mutex cannot protect independent
processes, so PostgreSQL's existing conditional transaction remains decisive.
