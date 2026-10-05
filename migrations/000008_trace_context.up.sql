-- Observability only: immutable first-create context; never part of identity.
ALTER TABLE jobs ADD COLUMN traceparent text NOT NULL DEFAULT '';
ALTER TABLE jobs ADD CONSTRAINT jobs_traceparent_format CHECK
 (traceparent='' OR traceparent ~ '^00-[0-9a-f]{32}-[0-9a-f]{16}-0[01]$');
