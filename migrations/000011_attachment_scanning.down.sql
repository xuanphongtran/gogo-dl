-- Old cleanup binaries know only quarantine keys. Never discard promotion
-- evidence while a possibly-created final object is still awaiting cleanup.
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM attachments WHERE scan_started_at IS NOT NULL AND state <> 'deleted') THEN
        RAISE EXCEPTION 'clean scanned attachment objects before downgrading';
    END IF;
END $$;
ALTER TABLE attachments DROP CONSTRAINT attachments_ready_scan_check;
ALTER TABLE attachments DROP CONSTRAINT attachments_state_check;
ALTER TABLE attachments ADD CONSTRAINT attachments_state_check CHECK (state IN
    ('pending_upload','scanning','cancelled','expired','rejected','deleting','deleted'));
ALTER TABLE attachments DROP COLUMN scan_started_at, DROP COLUMN scan_completed_at, DROP COLUMN scan_error;
UPDATE domain_outbox_deliveries SET last_error='storage_unavailable' WHERE last_error='scanner_unavailable';
ALTER TABLE domain_outbox_deliveries DROP CONSTRAINT domain_outbox_deliveries_last_error_check;
ALTER TABLE domain_outbox_deliveries ADD CONSTRAINT domain_outbox_deliveries_last_error_check CHECK
    (last_error IS NULL OR last_error IN ('storage_unavailable','invalid_job'));
