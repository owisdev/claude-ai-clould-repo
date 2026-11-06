-- Admin API (phase 2, step 8): every admin action, who, when, from where.
-- Secret values are never written here.
CREATE TABLE audit_log (
    id         bigserial PRIMARY KEY,
    at         timestamptz NOT NULL DEFAULT now(),
    actor      text NOT NULL,              -- "admin:<email>", "cli", ...
    action     text NOT NULL,              -- e.g. "settings.change", "admins.add"
    target     text NOT NULL DEFAULT '',   -- e.g. the changed keys, an email
    detail     jsonb NOT NULL DEFAULT '{}',
    outcome    text NOT NULL DEFAULT 'ok' CHECK (outcome IN ('ok', 'denied', 'failed')),
    request_id text NOT NULL DEFAULT '',
    remote_ip  text NOT NULL DEFAULT ''
);
CREATE INDEX audit_log_actor ON audit_log (actor, id DESC);
