ALTER TABLE attachments ADD COLUMN message_id BIGINT REFERENCES messages(id) ON DELETE SET NULL;
CREATE INDEX idx_attachments_message ON attachments(message_id,id) WHERE state='attached';
ALTER TABLE attachments DROP CONSTRAINT attachments_state_check;
ALTER TABLE attachments ADD CONSTRAINT attachments_state_check CHECK(state IN
 ('pending_upload','scanning','ready','attached','cancelled','expired','rejected','deleting','deleted'));
ALTER TABLE attachments DROP CONSTRAINT attachments_ready_scan_check;
ALTER TABLE attachments ADD CONSTRAINT attachments_ready_scan_check CHECK
 (state NOT IN ('ready','attached') OR (scan_started_at IS NOT NULL AND scan_completed_at IS NOT NULL AND scan_error IS NULL));
ALTER TABLE attachments DROP CONSTRAINT attachments_ready_processed_check;
ALTER TABLE attachments ADD CONSTRAINT attachments_ready_processed_check CHECK
 (state NOT IN ('ready','attached') OR processed_sha256 IS NOT NULL);
ALTER TABLE domain_outbox DROP CONSTRAINT domain_outbox_kind_check;
ALTER TABLE domain_outbox ADD CONSTRAINT domain_outbox_kind_check CHECK(kind IN
 ('attachment.scan_requested','attachment.cleanup_requested','message.mentioned','notification.created','message.created','message.updated','message.deleted'));
ALTER TABLE domain_outbox DROP CONSTRAINT domain_outbox_reference_check;
ALTER TABLE domain_outbox ADD CONSTRAINT domain_outbox_reference_check CHECK (
 (kind IN ('attachment.scan_requested','attachment.cleanup_requested') AND attachment_id IS NOT NULL) OR
 (kind='message.mentioned' AND message_id>0 AND user_id>0 AND membership_generation>0) OR
 (kind='notification.created' AND notification_id>0 AND user_id>0) OR
 (kind IN ('message.created','message.updated','message.deleted') AND message_id IS NOT NULL AND message_id>0 AND room_id IS NOT NULL AND room_id>0));
