DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM attachments WHERE state='attached' OR (message_id IS NOT NULL AND state<>'deleted')) THEN
  RAISE EXCEPTION 'clean bound attachment objects before downgrading';
 END IF;
END $$;
DELETE FROM domain_outbox WHERE kind IN ('message.created','message.updated','message.deleted');
ALTER TABLE domain_outbox DROP CONSTRAINT domain_outbox_reference_check;
ALTER TABLE domain_outbox DROP CONSTRAINT domain_outbox_kind_check;
ALTER TABLE domain_outbox ADD CONSTRAINT domain_outbox_kind_check CHECK(kind IN
 ('attachment.scan_requested','attachment.cleanup_requested','message.mentioned','notification.created'));
ALTER TABLE domain_outbox ADD CONSTRAINT domain_outbox_reference_check CHECK (
 (kind IN ('attachment.scan_requested','attachment.cleanup_requested') AND attachment_id IS NOT NULL) OR
 (kind='message.mentioned' AND message_id>0 AND user_id>0 AND membership_generation>0) OR
 (kind='notification.created' AND notification_id>0 AND user_id>0));
ALTER TABLE attachments DROP CONSTRAINT attachments_ready_processed_check;
ALTER TABLE attachments ADD CONSTRAINT attachments_ready_processed_check CHECK (state<>'ready' OR processed_sha256 IS NOT NULL);
ALTER TABLE attachments DROP CONSTRAINT attachments_ready_scan_check;
ALTER TABLE attachments ADD CONSTRAINT attachments_ready_scan_check CHECK
 (state<>'ready' OR (scan_started_at IS NOT NULL AND scan_completed_at IS NOT NULL AND scan_error IS NULL));
ALTER TABLE attachments DROP CONSTRAINT attachments_state_check;
ALTER TABLE attachments ADD CONSTRAINT attachments_state_check CHECK(state IN
 ('pending_upload','scanning','ready','cancelled','expired','rejected','deleting','deleted'));
ALTER TABLE attachments DROP COLUMN message_id;
