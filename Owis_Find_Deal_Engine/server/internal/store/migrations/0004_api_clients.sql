-- API clients and keys (phase 3, step 11): own apps and AI agents call
-- the public API with a key instead of a user's login token. Keys are
-- stored as SHA-256 hashes only; the key itself is shown once.
CREATE TABLE api_clients (
    id          bigserial PRIMARY KEY,
    name        text NOT NULL,
    owner       text NOT NULL DEFAULT '',          -- who runs it (person, team, app)
    scopes      text[] NOT NULL,                   -- e.g. {search,countries}
    daily_quota bigint NOT NULL CHECK (daily_quota > 0),   -- live searches per UTC day
    rate_rps    double precision NOT NULL CHECK (rate_rps > 0),
    rate_burst  integer NOT NULL CHECK (rate_burst > 0),
    countries   text[] NOT NULL DEFAULT '{}',      -- allowed countries; empty = all
    ip_allow    text[] NOT NULL DEFAULT '{}',      -- CIDRs; empty = any address
    expires_at  timestamptz,                       -- the client and all its keys; NULL = never
    created_at  timestamptz NOT NULL DEFAULT now(),
    created_by  text NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    updated_by  text NOT NULL,
    revoked_at  timestamptz,
    revoked_by  text NOT NULL DEFAULT ''
);

CREATE TABLE api_keys (
    id           bigserial PRIMARY KEY,
    client_id    bigint NOT NULL REFERENCES api_clients (id) ON DELETE CASCADE,
    prefix       text NOT NULL,                    -- first characters, to recognise a key
    hash         bytea NOT NULL UNIQUE CHECK (length(hash) = 32),   -- SHA-256 of the key
    created_at   timestamptz NOT NULL DEFAULT now(),
    created_by   text NOT NULL,
    expires_at   timestamptz,                      -- set when rotated: end of the grace period
    revoked_at   timestamptz,
    last_used_at timestamptz
);
CREATE INDEX api_keys_client ON api_keys (client_id, id);
