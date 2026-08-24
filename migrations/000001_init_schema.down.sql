-- migrations/000001_init_schema.down.sql
-- Drops all tables created in the up migration (reverse order to respect FK constraints)

DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS room_members;
DROP TABLE IF EXISTS rooms;
DROP TABLE IF EXISTS users;
