-- Original declarations remain immutable for quota and upload idempotency.
ALTER TABLE attachments
 ADD COLUMN processed_sha256 TEXT,
 ADD COLUMN processed_size_bytes BIGINT,
 ADD COLUMN processed_content_type TEXT,
 ADD COLUMN processing_engine TEXT,
 ADD COLUMN processing_policy TEXT;
-- Schema11 only promoted original bytes after a real ClamAV verdict.
UPDATE attachments SET processed_sha256=sha256,processed_size_bytes=size_bytes,
 processed_content_type=content_type,processing_engine='clamav',processing_policy='clamav-v1'
 WHERE state='ready';
ALTER TABLE attachments ADD CONSTRAINT attachments_processed_check CHECK (
 (processed_sha256 IS NULL AND processed_size_bytes IS NULL AND processed_content_type IS NULL AND processing_engine IS NULL AND processing_policy IS NULL)
 OR (processed_sha256 IS NOT NULL AND processed_sha256 ~ '^[a-f0-9]{64}$'
 AND processed_size_bytes IS NOT NULL AND processed_size_bytes BETWEEN 1 AND 10485760
 AND processed_content_type IS NOT NULL AND processed_content_type=content_type
 AND processing_engine IS NOT NULL AND processing_policy IS NOT NULL
 AND ((processing_engine='sanitize' AND processing_policy='sanitize-v1')
 OR (processing_engine='clamav' AND processing_policy='clamav-v1' AND processed_sha256=sha256 AND processed_size_bytes=size_bytes))));
ALTER TABLE attachments ADD CONSTRAINT attachments_ready_processed_check CHECK
 (state<>'ready' OR processed_sha256 IS NOT NULL);
