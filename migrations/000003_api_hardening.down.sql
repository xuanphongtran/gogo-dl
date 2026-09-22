DROP INDEX IF EXISTS users_email_lower_key;

ALTER TABLE users
    ADD CONSTRAINT users_email_key UNIQUE (email);
