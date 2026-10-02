-- Pending scanner: no ready/attached state or message binding is admitted yet.
CREATE TABLE attachments (
    id BIGSERIAL PRIMARY KEY,
    room_id BIGINT REFERENCES rooms(id) ON DELETE SET NULL,
    uploader_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    object_key TEXT NOT NULL UNIQUE CHECK (object_key ~ '^quarantine/[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$'),
    filename TEXT NOT NULL CHECK (octet_length(filename) BETWEEN 1 AND 255),
    content_type TEXT NOT NULL CHECK (content_type IN ('image/jpeg','image/png','text/plain')),
    size_bytes BIGINT NOT NULL CHECK (size_bytes BETWEEN 1 AND 10485760),
    sha256 TEXT NOT NULL CHECK (sha256 ~ '^[a-f0-9]{64}$'),
    request_key TEXT NOT NULL CHECK (octet_length(request_key) BETWEEN 16 AND 128),
    request_hash TEXT NOT NULL CHECK (request_hash ~ '^[a-f0-9]{64}$'),
    state TEXT NOT NULL DEFAULT 'pending_upload' CHECK (state IN
        ('pending_upload','scanning','cancelled','expired','rejected','deleting','deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    upload_expires_at TIMESTAMPTZ NOT NULL,
    cleanup_after TIMESTAMPTZ NOT NULL,
    CHECK (upload_expires_at > created_at AND cleanup_after >= upload_expires_at),
    UNIQUE(uploader_id,request_key)
);
-- SET NULL preserves the only object reference through account/room deletion.
-- The sweeper converts orphaned/expired reservations into durable cleanup jobs.
CREATE INDEX idx_attachments_user_quota ON attachments(uploader_id) WHERE state <> 'deleted';
CREATE INDEX idx_attachments_room_quota ON attachments(room_id) WHERE state <> 'deleted';
CREATE INDEX idx_attachments_sweep ON attachments(cleanup_after,id) WHERE state <> 'deleted';

CREATE TABLE domain_aggregate_counters (
    aggregate_type TEXT NOT NULL CHECK (aggregate_type IN ('room','user','attachment')),
    aggregate_id BIGINT NOT NULL CHECK (aggregate_id > 0),
    last_seq BIGINT NOT NULL CHECK (last_seq > 0),
    PRIMARY KEY(aggregate_type,aggregate_id)
);
CREATE TABLE domain_outbox (
    event_id UUID PRIMARY KEY,
    schema_version INTEGER NOT NULL CHECK (schema_version = 1),
    kind TEXT NOT NULL CHECK (kind IN ('attachment.scan_requested','attachment.cleanup_requested')),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    aggregate_type TEXT NOT NULL CHECK (aggregate_type IN ('room','user','attachment')),
    aggregate_id BIGINT NOT NULL CHECK (aggregate_id > 0),
    aggregate_seq BIGINT NOT NULL CHECK (aggregate_seq > 0),
    aggregate_version BIGINT,
    room_id BIGINT,
    user_id BIGINT,
    attachment_id BIGINT NOT NULL CHECK (attachment_id > 0),
    UNIQUE(aggregate_type,aggregate_id,aggregate_seq),
    UNIQUE(kind,attachment_id)
);
-- Opaque provider identity belongs only to cleanup purpose, not public intents.
CREATE TABLE attachment_cleanup_objects (
    event_id UUID PRIMARY KEY REFERENCES domain_outbox(event_id) ON DELETE CASCADE,
    object_key TEXT NOT NULL CHECK (object_key ~ '^quarantine/[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$')
);
CREATE TABLE domain_outbox_deliveries (
    event_id UUID NOT NULL REFERENCES domain_outbox(event_id) ON DELETE CASCADE,
    purpose TEXT NOT NULL CHECK (purpose IN ('attachment_scan','attachment_cleanup','broker')),
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','leased','done','dead')),
    available_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    lease_token UUID,
    lease_until TIMESTAMPTZ,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error TEXT CHECK (last_error IS NULL OR last_error IN ('storage_unavailable','invalid_job')),
    completed_at TIMESTAMPTZ,
    PRIMARY KEY(event_id,purpose),
    CHECK ((state = 'leased' AND lease_token IS NOT NULL AND lease_until IS NOT NULL) OR
           (state <> 'leased' AND lease_token IS NULL AND lease_until IS NULL))
);
CREATE INDEX idx_outbox_claim ON domain_outbox_deliveries(purpose,available_at,lease_until)
    WHERE state IN ('pending','leased');
