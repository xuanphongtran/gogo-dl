ALTER TABLE messages
    ADD COLUMN revision BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN edited_at TIMESTAMPTZ,
    ADD COLUMN deleted_at TIMESTAMPTZ,
    ADD COLUMN deleted_by BIGINT REFERENCES users (id) ON DELETE SET NULL,
    ADD CONSTRAINT messages_revision_positive CHECK (revision > 0),
    ADD CONSTRAINT messages_tombstone_empty CHECK (deleted_at IS NULL OR content = '');

-- Account deletion locates audit references through the foreign key action.
CREATE INDEX messages_deleted_by_idx ON messages (deleted_by) WHERE deleted_by IS NOT NULL;
