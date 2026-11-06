-- Settings store (phase 2, step 7) and the admin list (used from step 8).

-- Current value of every runtime setting that is set. A setting that is
-- not here uses its built-in default. Secret values are encrypted by the
-- server (AES-256-GCM) before they reach the database.
CREATE TABLE settings (
    key        text PRIMARY KEY CHECK (key ~ '^[A-Z][A-Z0-9_]*$'),
    value      text NOT NULL,
    secret     boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by text NOT NULL
);

-- Every change: who, when, old -> new. NULL old = was not set, NULL new =
-- removed (back to the default). Secret values are never written here.
CREATE TABLE settings_history (
    id         bigserial PRIMARY KEY,
    key        text NOT NULL,
    old_value  text,
    new_value  text,
    secret     boolean NOT NULL DEFAULT false,
    changed_by text NOT NULL,
    changed_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX settings_history_key ON settings_history (key, id DESC);

-- Admins of the service. Login stays with the auth provider (Firebase);
-- this list decides who is an admin.
CREATE TABLE admins (
    id           bigserial PRIMARY KEY,
    email        text NOT NULL CHECK (email = lower(email) AND email LIKE '%@%'),
    firebase_uid text UNIQUE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    created_by   text NOT NULL,
    disabled_at  timestamptz
);
CREATE UNIQUE INDEX admins_email ON admins (email);
