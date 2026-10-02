DROP INDEX IF EXISTS idx_messages_search_vector;
ALTER TABLE messages DROP COLUMN search_vector;
