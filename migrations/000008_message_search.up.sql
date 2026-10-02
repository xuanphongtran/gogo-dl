-- Explicit configuration keeps index behavior independent of session defaults.
-- Generated storage synchronizes edits/deletions with the message transaction.
ALTER TABLE messages ADD COLUMN search_vector tsvector
    GENERATED ALWAYS AS (
        CASE WHEN deleted_at IS NULL
             THEN to_tsvector('simple'::regconfig, content)
             ELSE ''::tsvector END
    ) STORED;

CREATE INDEX idx_messages_search_vector ON messages USING GIN (search_vector)
    WHERE deleted_at IS NULL;
