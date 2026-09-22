-- Canonicalize email identity and enforce case-insensitive uniqueness.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM users
        GROUP BY lower(btrim(email))
        HAVING COUNT(*) > 1
    ) THEN
        RAISE EXCEPTION $message$cannot canonicalize users.email: case-insensitive duplicates exist$message$;
    END IF;
END
$$;

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_email_key;

UPDATE users
SET email = lower(btrim(email))
WHERE email IS DISTINCT FROM lower(btrim(email));

CREATE UNIQUE INDEX users_email_lower_key ON users (lower(email));
