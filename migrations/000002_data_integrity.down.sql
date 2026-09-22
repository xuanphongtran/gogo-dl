-- Downgrade is intentionally refused if deleted authors exist because the
-- historical schema cannot represent a NULL message author safely.

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM messages WHERE user_id IS NULL) THEN
        RAISE EXCEPTION 'cannot downgrade data-integrity migration while messages have deleted authors';
    END IF;
END
$$;

ALTER TABLE messages
    DROP CONSTRAINT IF EXISTS messages_user_id_fkey;

ALTER TABLE messages
    ALTER COLUMN user_id SET NOT NULL;

ALTER TABLE messages
    ADD CONSTRAINT messages_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE SET NULL;

ALTER TABLE rooms
    DROP CONSTRAINT IF EXISTS rooms_created_by_fkey;

ALTER TABLE rooms
    ADD CONSTRAINT rooms_created_by_fkey
    FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE SET NULL;
