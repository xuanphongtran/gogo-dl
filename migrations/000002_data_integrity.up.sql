-- Make deletion semantics explicit without editing the historical migration.

ALTER TABLE rooms
    DROP CONSTRAINT IF EXISTS rooms_created_by_fkey;

ALTER TABLE rooms
    ADD CONSTRAINT rooms_created_by_fkey
    FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE RESTRICT;

ALTER TABLE messages
    ALTER COLUMN user_id DROP NOT NULL;

ALTER TABLE messages
    DROP CONSTRAINT IF EXISTS messages_user_id_fkey;

ALTER TABLE messages
    ADD CONSTRAINT messages_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE SET NULL;
