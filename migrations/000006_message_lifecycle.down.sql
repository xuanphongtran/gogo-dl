-- Erased content cannot be recovered by rolling back lifecycle metadata.
DROP INDEX messages_deleted_by_idx;
ALTER TABLE messages
    DROP CONSTRAINT messages_tombstone_empty,
    DROP CONSTRAINT messages_revision_positive,
    DROP COLUMN deleted_by,
    DROP COLUMN deleted_at,
    DROP COLUMN edited_at,
    DROP COLUMN revision;
