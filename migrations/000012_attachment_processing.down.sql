-- Schema11 cannot interpret sanitized artifacts; clean them before rollback.
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM attachments WHERE state<>'deleted' AND processing_engine='sanitize') THEN
  RAISE EXCEPTION 'clean sanitized attachment objects before downgrading';
 END IF;
END $$;
ALTER TABLE attachments DROP CONSTRAINT attachments_ready_processed_check;
ALTER TABLE attachments DROP CONSTRAINT attachments_processed_check;
ALTER TABLE attachments DROP COLUMN processed_sha256, DROP COLUMN processed_size_bytes,
 DROP COLUMN processed_content_type, DROP COLUMN processing_engine, DROP COLUMN processing_policy;
