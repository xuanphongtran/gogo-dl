DROP TABLE notifications;
DELETE FROM domain_outbox WHERE kind IN ('message.mentioned','notification.created');
ALTER TABLE domain_outbox_deliveries DROP CONSTRAINT domain_outbox_deliveries_purpose_check;
ALTER TABLE domain_outbox_deliveries ADD CONSTRAINT domain_outbox_deliveries_purpose_check
    CHECK(purpose IN ('attachment_scan','attachment_cleanup','broker'));
ALTER TABLE domain_outbox DROP CONSTRAINT domain_outbox_reference_check;
ALTER TABLE domain_outbox DROP COLUMN notification_id, DROP COLUMN membership_generation, DROP COLUMN message_id;
ALTER TABLE domain_outbox ALTER COLUMN attachment_id SET NOT NULL;
ALTER TABLE domain_outbox DROP CONSTRAINT domain_outbox_kind_check;
ALTER TABLE domain_outbox ADD CONSTRAINT domain_outbox_kind_check
    CHECK(kind IN ('attachment.scan_requested','attachment.cleanup_requested'));
DROP TABLE room_notification_preferences, notification_preferences, message_send_keys, message_mentions;
ALTER TABLE room_members DROP CONSTRAINT room_members_generation_key;
ALTER TABLE room_members DROP COLUMN membership_generation;
