import { useMemo, useState } from "react";
import { api, get, type Change, type Setting } from "../api";
import { Card, ErrorBox, errorText, fmtTime, useLoad } from "../ui";

// Short help per setting (the full list is in the server's .env.example).
const HELP: Record<string, string> = {
  SEARCH_PROVIDERS: "Web search chain in order, e.g. searxng,serpapi",
  SEARXNG_ONE_QUERY: "true: one OR-ed site: query instead of one per shop",
  SEARXNG_MAX_PAGES: "Result pages read from SearXNG",
  SERPAPI_KEY: "SerpApi key (secret)",
  SERPAPI_ENGINE: "google_shopping (prices) or google (web)",
  SERPAPI_MARKETS: "Shops searched by SerpApi's own engines (amazon, ebay); empty = none",
  SERPAPI_TIMEOUT: "Max wait for SerpApi, e.g. 25s",
  SERPAPI_SEARCH_COST_USD: "Estimated cost of one SerpApi search (free plan: 0)",
  PROVIDER_ATTEMPT_TIMEOUT: "Time each web provider gets before the next one",
  PROVIDER_FAILURE_THRESHOLD: "Failures in a row before a source is skipped",
  PROVIDER_COOLDOWN: "How long a failing source is skipped",
  APIFY_TOKEN: "Apify API token (secret)",
  APIFY_MARKETS: "Shops searched by Apify scrapers, e.g. aliexpress,temu,shein",
  APIFY_MAX_ITEMS: "Results per scraper run (default for every shop)",
  APIFY_TIMEOUT: "Max wait for one scraper run, e.g. 90s",
  APIFY_MAX_CHARGE_USD: "Max cost of one pay-per-event run",
  APIFY_EMPTY_FALLBACK: "true: a scraper with nothing relevant falls back to web search",
  SEARCH_COMBINED: "true: one provider call per search (cheapest)",
  RESULTS_PER_MARKET: "Results kept per shop",
  SEARCH_TIMEOUT: "Whole search timeout (raised automatically to fit the chain)",
  SEARCH_MAX_CONCURRENCY: "Provider calls in parallel per search",
  CACHE_ENABLED: "Search cache on / off",
  CACHE_FRESH_TTL: "Answers served as fresh, e.g. 48h",
  CACHE_PARTIAL_TTL: "Fresh time when a shop failed",
  CACHE_EMPTY_TTL: "Fresh time when nothing was found",
  CACHE_STALE_TTL: "Served (and refreshed in the background) until",
  CACHE_MIN_REFRESH: "Pull-to-refresh ignored for younger answers",
  CACHE_MAX_BACKGROUND_REFRESH: "Parallel background refreshes",
  CACHE_GENERATION: "Raise to drop every cached answer (Cache & sources page)",
  PLANS: "Daily live searches per plan, e.g. free:20,pro:500",
  DEFAULT_PLAN: "Plan of users without a plan claim",
  RATE_LIMIT_RPS: "Requests per second per user",
  RATE_LIMIT_BURST: "Burst per user",
  CORS_ALLOWED_ORIGINS: "Browser origins allowed to call the API",
  BUDGET_MONTHLY_USD: "Monthly budget of the estimated cost (default 5; 0 = none)",
  BUDGET_ALERT_PERCENT: "Alert when this share of the budget is used (default 80)",
};

const GROUPS: { title: string; match: (k: string) => boolean }[] = [
  { title: "Sources: SerpApi and web search", match: (k) => /^(SEARCH_PROVIDERS|SEARXNG_|SERPAPI_|PROVIDER_)/.test(k) },
  { title: "Sources: Apify scrapers", match: (k) => k.startsWith("APIFY_") },
  { title: "Search", match: (k) => /^(SEARCH_|RESULTS_PER_MARKET)/.test(k) },
  { title: "Cache", match: (k) => k.startsWith("CACHE_") },
  { title: "Plans, limits and budget", match: (k) => /^(PLANS|DEFAULT_PLAN|RATE_LIMIT_|BUDGET_)/.test(k) },
  { title: "Other", match: () => true },
];

type Pending = Record<string, string | null>;

export function Settings() {
  const list = useLoad(() => get<{ settings_version: number; settings: Setting[] }>("/admin/v1/settings"), []);
  const history = useLoad(() => get<{ changes: Change[] }>("/admin/v1/settings/history?limit=50"), []);
  const [pending, setPending] = useState<Pending>({});
  const [newKey, setNewKey] = useState("");
  const [error, setError] = useState("");
  const [saved, setSaved] = useState("");
  const [busy, setBusy] = useState(false);

  const groups = useMemo(() => {
    const rows = (list.data?.settings ?? []).filter((s) => s.key !== "MARKETS");
    const extra = Object.keys(pending).filter((k) => !rows.some((r) => r.key === k));
    const all: Setting[] = [...rows, ...extra.map((k) => ({ key: k, set: false }))];
    const used = new Set<string>();
    return GROUPS.map((g) => {
      const items = all.filter((s) => !used.has(s.key) && g.match(s.key));
      items.forEach((s) => used.add(s.key));
      return { title: g.title, items };
    }).filter((g) => g.items.length > 0);
  }, [list.data, pending]);

  const count = Object.keys(pending).length;

  async function save() {
    setBusy(true);
    setError("");
    setSaved("");
    try {
      const res = await api<{ settings_version: number; changed: string[] }>("PATCH", "/admin/v1/settings", { changes: pending });
      setPending({});
      setSaved(
        res.changed.length
          ? `Saved ${res.changed.join(", ")} (settings version ${res.settings_version}). The servers apply it within about 10 seconds.`
          : "Nothing changed (same values).",
      );
      void list.reload();
      void history.reload();
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  }

  function addKey() {
    const k = newKey.trim().toUpperCase();
    if (!/^[A-Z][A-Z0-9_]*$/.test(k)) {
      setError("A setting name uses A-Z, 0-9 and _, e.g. APIFY_TEMU_RUN_COST_USD.");
      return;
    }
    setPending({ ...pending, [k]: "" });
    setNewKey("");
  }

  return (
    <>
      <h1>Settings</h1>
      <p className="muted">
        Every change is checked as a whole and refused if the result would be invalid. Secrets can be replaced, never read.
        Countries and shops are on their own page.
      </p>
      <div className="card" style={{ position: "sticky", top: 0, zIndex: 1 }}>
        <div className="row">
          <span>{count ? `${count} unsaved change${count > 1 ? "s" : ""}` : "No unsaved changes"}</span>
          <span className="spacer" />
          <button onClick={() => setPending({})} disabled={!count || busy}>
            Discard
          </button>
          <button className="primary" onClick={save} disabled={!count || busy}>
            {busy ? "Saving…" : "Save changes"}
          </button>
        </div>
        <ErrorBox text={error || list.error} />
        {saved && <p className="notice ok-text">{saved}</p>}
      </div>

      {groups.map((g) => (
        <Card key={g.title} title={g.title}>
          <div className="table-wrap">
            <table>
              <tbody>
                {g.items.map((s) => (
                  <SettingRow key={s.key} s={s} pending={pending} setPending={setPending} />
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      ))}

      <Card title="Add a setting">
        <p className="muted small">
          Per-shop Apify settings: APIFY_&lt;SHOP&gt;_ACTOR, _INPUT, _CURRENCY, _MAX_ITEMS, _RUN_COST_USD (e.g. APIFY_TEMU_RUN_COST_USD).
        </p>
        <div className="row">
          <input className="mono" placeholder="SETTING_NAME" value={newKey} onChange={(e) => setNewKey(e.target.value)} />
          <button onClick={addKey}>Add</button>
        </div>
      </Card>

      <Card title="Change history" actions={<button onClick={() => history.reload()}>Refresh</button>}>
        <ErrorBox text={history.error} />
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>When</th>
                <th>Who</th>
                <th>Setting</th>
                <th>Change</th>
              </tr>
            </thead>
            <tbody>
              {(history.data?.changes ?? []).map((c) => (
                <tr key={c.id}>
                  <td className="small">{fmtTime(c.changed_at)}</td>
                  <td className="small">{c.changed_by}</td>
                  <td className="mono">{c.key}</td>
                  <td className="mono small" style={{ wordBreak: "break-all" }}>
                    {c.secret ? "secret changed" : `${c.old ?? "(default)"} → ${c.new ?? "(default)"}`}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Card>
    </>
  );
}

function SettingRow({ s, pending, setPending }: { s: Setting; pending: Pending; setPending: (p: Pending) => void }) {
  const isPending = s.key in pending;
  const draft = pending[s.key];
  const long = (s.value ?? "").length > 60 || s.key.endsWith("_INPUT");
  const current = s.secret ? (s.set ? "set (hidden)" : "not set") : s.set ? s.value : "default";

  const change = (v: string | null) => setPending({ ...pending, [s.key]: v });
  const undo = () => {
    const next = { ...pending };
    delete next[s.key];
    setPending(next);
  };

  return (
    <tr>
      <td style={{ width: "34%" }}>
        <div className="mono">{s.key}</div>
        {HELP[s.key] && <div className="muted small">{HELP[s.key]}</div>}
      </td>
      <td style={{ width: "46%" }}>
        {isPending ? (
          draft === null ? (
            <span className="muted">back to default on save</span>
          ) : long ? (
            <textarea className="mono" rows={4} value={draft} onChange={(e) => change(e.target.value)} />
          ) : (
            <input
              className="mono"
              style={{ width: "100%" }}
              type={s.secret ? "password" : "text"}
              autoComplete="off"
              placeholder={s.secret ? "new secret value" : ""}
              value={draft}
              onChange={(e) => change(e.target.value)}
            />
          )
        ) : (
          <span className={s.set ? "mono" : "muted"} style={{ wordBreak: "break-all" }}>
            {current}
          </span>
        )}
        {s.updated_by && !isPending && (
          <div className="muted small">
            {s.updated_by} · {fmtTime(s.updated_at)}
          </div>
        )}
      </td>
      <td className="num" style={{ whiteSpace: "nowrap" }}>
        {isPending ? (
          <button onClick={undo}>Undo</button>
        ) : (
          <>
            <button onClick={() => change(s.secret ? "" : (s.value ?? ""))}>{s.secret ? "Replace" : "Edit"}</button>{" "}
            {s.set && (
              <button className="danger" onClick={() => change(null)}>
                Reset
              </button>
            )}
          </>
        )}
      </td>
    </tr>
  );
}
