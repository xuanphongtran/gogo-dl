-- Add explicit room visibility, membership roles, and durable invitations.
-- Existing rooms remain discoverable public rooms for backward compatibility.

ALTER TABLE rooms
    ADD COLUMN visibility TEXT NOT NULL DEFAULT 'public';

ALTER TABLE rooms
    ADD CONSTRAINT rooms_visibility_check
    CHECK (visibility IN ('public', 'private'));

ALTER TABLE room_members
    ADD COLUMN role TEXT NOT NULL DEFAULT 'member';

ALTER TABLE room_members
    ADD CONSTRAINT room_members_role_check
    CHECK (role IN ('owner', 'moderator', 'member'));

INSERT INTO room_members (room_id, user_id, role)
SELECT r.id, r.created_by, 'owner'
FROM rooms r
ON CONFLICT (room_id, user_id)
DO UPDATE SET role = 'owner';

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM rooms r
        WHERE NOT EXISTS (
            SELECT 1
            FROM room_members rm
            WHERE rm.room_id = r.id
              AND rm.user_id = r.created_by
              AND rm.role = 'owner'
        )
    ) THEN
        RAISE EXCEPTION 'cannot establish room owner membership invariant';
    END IF;
END
$$;

CREATE UNIQUE INDEX room_members_one_owner_idx
    ON room_members (room_id)
    WHERE role = 'owner';

CREATE TABLE room_invitations (
    id           BIGSERIAL PRIMARY KEY,
    room_id      BIGINT NOT NULL REFERENCES rooms (id) ON DELETE CASCADE,
    invitee_id   BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    invited_by   BIGINT REFERENCES users (id) ON DELETE SET NULL,
    status       TEXT NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'accepted', 'declined')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    responded_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX room_invitations_pending_pair_idx
    ON room_invitations (room_id, invitee_id)
    WHERE status = 'pending';

CREATE INDEX room_invitations_invitee_status_idx
    ON room_invitations (invitee_id, status, id DESC);

CREATE INDEX room_invitations_room_idx
    ON room_invitations (room_id, id DESC);
