# 0002 — Phase 1 API contract and persistence boundaries

Status: Accepted

## Context

The Phase 0 JSON decoder accepted case-insensitive names and duplicate envelope
keys. Offset pages can drift when jobs arrive between requests. The repository
already has an index on `(created_at DESC, id DESC)` and no external API consumer
or stable offset compatibility promise was found.

## Decision

Use the standard JSON decoder's object tokens to validate exact, unique top-level
keys and raw field values. Numeric null is invalid; omission supplies defaults.
Payload is required but JSON null is valid. Metadata keys are stored unchanged
after rejecting blank values; null means no key. Nested payload duplicate keys
retain JSONB behavior. The 1 MiB reader limit precedes parsing.

Replace offset with exclusive keyset pagination on `(created_at, id)` in descending
order. Query limit+1, and encode the last returned row as a versioned base64url
cursor. HTTP handles encoding and bounded validation; domain/repository use a
typed boundary. Reuse the existing index without speculative new indexes.

Create uses one INSERT ... RETURNING and returns canonical persisted values.
Get/List each use one SELECT. These statements need no explicit transaction.
Multi-statement atomic use cases will introduce transactions when they exist;
migrations retain their explicit transaction and version lock.

## Alternatives

Keeping offset and cursor indefinitely introduces two contracts without a current
consumer need. A cursor containing only a timestamp cannot resolve ties. Snapshot
sessions, JWTs, HMAC, Redis cursor storage, ORM and a custom recursive JSON parser
add mechanisms unnecessary for this persistence API.

## Consequences

The early API changes: wrong-case/duplicate keys and numeric null return 400;
offset is removed; List returns jobs and next_cursor. Newer inserts do not shift
older pages, but pagination is not a snapshot. Structurally valid unsigned
cursors can be constructed and are not authorization tokens. Nested duplicates
are normalized by JSONB. No measured performance claim is made.

Validation is shared in domain and mirrored by simple database constraints, with
corrupt stored rows treated as internal failures. Submission still is not
idempotent. Database commit may succeed while the client loses its response.
