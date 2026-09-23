-- This rollback removes Phase 05 data and cannot preserve invitation history,
-- roles, or private-room visibility in the historical schema.

DROP TABLE IF EXISTS room_invitations;

DROP INDEX IF EXISTS room_invitations_room_idx;
DROP INDEX IF EXISTS room_invitations_invitee_status_idx;
DROP INDEX IF EXISTS room_invitations_pending_pair_idx;
DROP INDEX IF EXISTS room_members_one_owner_idx;

ALTER TABLE room_members
    DROP CONSTRAINT IF EXISTS room_members_role_check;

ALTER TABLE room_members
    DROP COLUMN IF EXISTS role;

ALTER TABLE rooms
    DROP CONSTRAINT IF EXISTS rooms_visibility_check;

ALTER TABLE rooms
    DROP COLUMN IF EXISTS visibility;
