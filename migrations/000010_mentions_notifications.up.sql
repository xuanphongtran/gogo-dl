ALTER TABLE room_members ADD COLUMN membership_generation BIGINT GENERATED ALWAYS AS IDENTITY;
ALTER TABLE room_members ADD CONSTRAINT room_members_generation_key UNIQUE(room_id,user_id,membership_generation);

CREATE TABLE message_mentions (
    message_id BIGINT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    room_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    membership_generation BIGINT NOT NULL,
    PRIMARY KEY(message_id,user_id),
    FOREIGN KEY(room_id,user_id,membership_generation)
        REFERENCES room_members(room_id,user_id,membership_generation) ON DELETE CASCADE
);
CREATE INDEX idx_mentions_membership ON message_mentions(room_id,user_id,membership_generation);
CREATE TABLE message_send_keys (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    request_key TEXT NOT NULL CHECK(octet_length(request_key) BETWEEN 16 AND 128),
    request_hash TEXT NOT NULL CHECK(request_hash ~ '^[a-f0-9]{64}$'),
    message_id BIGINT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()+interval '24 hours',
    PRIMARY KEY(user_id,request_key)
);
CREATE INDEX idx_message_send_expiry ON message_send_keys(expires_at);
CREATE TABLE notification_preferences (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    mentions_enabled BOOLEAN NOT NULL DEFAULT true
);
CREATE TABLE room_notification_preferences (
    room_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    membership_generation BIGINT NOT NULL,
    muted BOOLEAN NOT NULL DEFAULT false,
    PRIMARY KEY(room_id,user_id,membership_generation),
    FOREIGN KEY(room_id,user_id,membership_generation)
        REFERENCES room_members(room_id,user_id,membership_generation) ON DELETE CASCADE
);
ALTER TABLE domain_outbox DROP CONSTRAINT domain_outbox_kind_check;
ALTER TABLE domain_outbox ADD CONSTRAINT domain_outbox_kind_check CHECK(kind IN
    ('attachment.scan_requested','attachment.cleanup_requested','message.mentioned','notification.created'));
ALTER TABLE domain_outbox ALTER COLUMN attachment_id DROP NOT NULL;
ALTER TABLE domain_outbox ADD COLUMN message_id BIGINT;
ALTER TABLE domain_outbox ADD COLUMN membership_generation BIGINT;
ALTER TABLE domain_outbox ADD COLUMN notification_id BIGINT;
ALTER TABLE domain_outbox ADD CONSTRAINT domain_outbox_reference_check CHECK (
    (kind IN ('attachment.scan_requested','attachment.cleanup_requested') AND attachment_id IS NOT NULL) OR
    (kind='message.mentioned' AND message_id>0 AND user_id>0 AND membership_generation>0) OR
    (kind='notification.created' AND notification_id>0 AND user_id>0));
ALTER TABLE domain_outbox_deliveries DROP CONSTRAINT domain_outbox_deliveries_purpose_check;
ALTER TABLE domain_outbox_deliveries ADD CONSTRAINT domain_outbox_deliveries_purpose_check
    CHECK(purpose IN ('attachment_scan','attachment_cleanup','broker','notification'));
CREATE TABLE notifications (
    id BIGSERIAL PRIMARY KEY,
    event_id UUID NOT NULL REFERENCES domain_outbox(event_id),
    user_id BIGINT NOT NULL,
    room_id BIGINT NOT NULL,
    membership_generation BIGINT NOT NULL,
    message_id BIGINT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    channel TEXT NOT NULL DEFAULT 'in_app' CHECK(channel='in_app'),
    kind TEXT NOT NULL DEFAULT 'mention' CHECK(kind='mention'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    read_at TIMESTAMPTZ,
    UNIQUE(event_id,user_id,channel),
    FOREIGN KEY(room_id,user_id,membership_generation)
        REFERENCES room_members(room_id,user_id,membership_generation) ON DELETE CASCADE
);
CREATE INDEX idx_notifications_feed ON notifications(user_id,id DESC);
CREATE INDEX idx_notifications_unread ON notifications(user_id,id DESC) WHERE read_at IS NULL;
CREATE INDEX idx_notifications_retention ON notifications(created_at,id);
CREATE INDEX idx_notifications_membership ON notifications(room_id,user_id,membership_generation);
CREATE INDEX idx_notifications_message ON notifications(message_id);
