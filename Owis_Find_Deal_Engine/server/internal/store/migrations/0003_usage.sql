-- Usage and cost dashboard (phase 2, step 9). Counters per UTC day, added
-- up by every server instance (each flushes its own counts every 30 s).

-- kind 'search':   k1 = country,  k2 = outcome (hit, stale, live, quota, error)
-- kind 'source':   k1 = provider, k2 = shop,     k3 = outcome (ok, no_results, error)
--                  cost_usd = estimated cost, total_ms = time spent
-- kind 'fallback': k1 = chain,    k2 = from,     k3 = to ':' reason
CREATE TABLE usage_daily (
    day      date NOT NULL,
    kind     text NOT NULL,
    k1       text NOT NULL DEFAULT '',
    k2       text NOT NULL DEFAULT '',
    k3       text NOT NULL DEFAULT '',
    count    bigint NOT NULL DEFAULT 0,
    cost_usd double precision NOT NULL DEFAULT 0,
    total_ms bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (day, kind, k1, k2, k3)
);

-- Searches per normalized query (for "top queries"); no user ids. Kept
-- for a shorter time than the counters.
CREATE TABLE query_daily (
    day      date NOT NULL,
    country  text NOT NULL,
    query    text NOT NULL,
    searches bigint NOT NULL DEFAULT 0,
    live     bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (day, country, query)
);
