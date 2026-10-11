ALTER TABLE batches ADD COLUMN batch_code text;

-- Existing UUID identifiers remain stable; only the human-readable label is added.
UPDATE batches SET batch_code = 'BATCH-' || upper(id::text);

ALTER TABLE batches ALTER COLUMN batch_code SET NOT NULL;
ALTER TABLE batches ADD CONSTRAINT batches_batch_code_format
 CHECK (batch_code ~ '^[A-Z0-9][A-Z0-9_-]{0,63}$');
CREATE UNIQUE INDEX batches_batch_code_key ON batches (batch_code);
