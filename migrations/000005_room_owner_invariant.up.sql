-- Enforce exactly one owner per room and keep it aligned with rooms.created_by.

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM rooms r
        WHERE (
            SELECT COUNT(*)
            FROM room_members rm
            WHERE rm.room_id = r.id
              AND rm.role = 'owner'
        ) <> 1
        OR NOT EXISTS (
            SELECT 1
            FROM room_members rm
            WHERE rm.room_id = r.id
              AND rm.user_id = r.created_by
              AND rm.role = 'owner'
        )
    ) THEN
        RAISE EXCEPTION 'cannot establish exactly one owner invariant';
    END IF;
END
$$;

CREATE OR REPLACE FUNCTION enforce_room_owner_invariant()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    affected_room_id BIGINT;
    old_room_id      BIGINT;
    new_room_id      BIGINT;
    owner_count     BIGINT;
    creator_is_owner BOOLEAN;
BEGIN
    IF TG_TABLE_NAME = 'rooms' THEN
        IF TG_OP = 'DELETE' THEN
            old_room_id := OLD.id;
        ELSE
            new_room_id := NEW.id;
        END IF;
    ELSE
        IF TG_OP = 'DELETE' THEN
            old_room_id := OLD.room_id;
        ELSIF TG_OP = 'UPDATE' THEN
            old_room_id := OLD.room_id;
            new_room_id := NEW.room_id;
        ELSE
            new_room_id := NEW.room_id;
        END IF;
    END IF;

    FOR affected_room_id IN
        SELECT DISTINCT affected.room_id
        FROM (VALUES (old_room_id), (new_room_id)) AS affected(room_id)
        WHERE affected.room_id IS NOT NULL
    LOOP
        IF NOT EXISTS (SELECT 1 FROM rooms WHERE id = affected_room_id) THEN
            CONTINUE;
        END IF;

        SELECT COUNT(*) FILTER (WHERE rm.role = 'owner'),
               COALESCE(BOOL_OR(rm.user_id = r.created_by AND rm.role = 'owner'), FALSE)
        INTO owner_count, creator_is_owner
        FROM rooms r
        LEFT JOIN room_members rm ON rm.room_id = r.id
        WHERE r.id = affected_room_id
        GROUP BY r.id, r.created_by;

        IF owner_count <> 1 OR NOT creator_is_owner THEN
            RAISE EXCEPTION 'room % must have exactly one owner matching rooms.created_by', affected_room_id
                USING ERRCODE = '23514';
        END IF;
    END LOOP;

    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER room_members_owner_invariant
AFTER INSERT OR DELETE OR UPDATE OF room_id, user_id, role ON room_members
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION enforce_room_owner_invariant();

CREATE CONSTRAINT TRIGGER rooms_owner_invariant
AFTER INSERT OR UPDATE OF created_by ON rooms
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION enforce_room_owner_invariant();
