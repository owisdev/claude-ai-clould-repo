-- Signed webhooks of the asynchronous search (phase 3, step 12). Each API
-- client can get a webhook signing secret. The secret is never stored: it
-- is derived from SETTINGS_ENCRYPTION_KEY and this random salt, so a copy
-- of the database alone cannot sign webhooks. A new salt = a new secret.
ALTER TABLE api_clients
    ADD COLUMN webhook_salt          bytea CHECK (webhook_salt IS NULL OR length(webhook_salt) = 32),
    ADD COLUMN webhook_secret_prefix text NOT NULL DEFAULT '',   -- first characters, to recognise it
    ADD COLUMN webhook_secret_at     timestamptz;                -- when it was made
