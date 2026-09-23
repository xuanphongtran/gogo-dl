DROP TRIGGER IF EXISTS room_members_owner_invariant ON room_members;
DROP TRIGGER IF EXISTS rooms_owner_invariant ON rooms;
DROP FUNCTION IF EXISTS enforce_room_owner_invariant();
