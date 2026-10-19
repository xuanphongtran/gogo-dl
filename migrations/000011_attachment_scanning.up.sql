ALTER TABLE attachments
    ADD COLUMN scan_started_at TIMESTAMPTZ,
    ADD COLUMN scan_completed_at TIMESTAMPTZ,
    ADD COLUMN scan_error TEXT CHECK (scan_error IS NULL OR scan_error IN ('infected','invalid_content','scanner_rejected'));
ALTER TABLE attachments DROP CONSTRAINT attachments_state_check;
ALTER TABLE attachments ADD CONSTRAINT attachments_state_check CHECK (state IN
    ('pending_upload','scanning','ready','cancelled','expired','rejected','deleting','deleted'));
ALTER TABLE attachments ADD CONSTRAINT attachments_ready_scan_check CHECK
    (state <> 'ready' OR (scan_started_at IS NOT NULL AND scan_completed_at IS NOT NULL AND scan_error IS NULL));
ALTER TABLE domain_outbox_deliveries DROP CONSTRAINT domain_outbox_deliveries_last_error_check;
ALTER TABLE domain_outbox_deliveries ADD CONSTRAINT domain_outbox_deliveries_last_error_check CHECK
    (last_error IS NULL OR last_error IN ('storage_unavailable','scanner_unavailable','invalid_job'));
