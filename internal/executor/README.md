# Executor

Implemented: the bounded context-aware SLEEP executor is in
internal/service/execution. Outcomes classify permanent/retryable failure
explicitly; controlled transient executors exist only in tests.

This directory is a navigation placeholder, not a second executor framework.
Arbitrary shell/Docker execution and additional production Job types are not implemented.
