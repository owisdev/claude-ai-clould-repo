import { useEffect, useMemo, useState } from "react";
import { api, get } from "../api";
import { Card, ErrorBox, errorText, useLoad } from "../ui";

interface Catalog {
  markets?: Record<string, { name?: string; provider?: string; search_link?: string }>;
  countries?: Record<string, { name?: string; currency?: string; region?: string; markets?: { market: string; domain: string }[] }>;
}

const SOURCE_TEXT: Record<string, string> = {
  setting: "edited here (stored in the database)",
  file: "the server's MARKETS_FILE",
  "built-in": "the built-in markets.json",
};

export function Markets() {
  const { data, error, reload } = useLoad(() => get<{ source: string; markets: Catalog }>("/admin/v1/markets"), []);
  const [text, setText] = useState("");
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (data) setText(JSON.stringify(data.markets, null, 2));
  }, [data]);

  const parsed = useMemo((): { cat?: Catalog; error?: string } => {
    try {
      return { cat: JSON.parse(text) as Catalog };
    } catch (e) {
      return { error: errorText(e) };
    }
  }, [text]);
  const changed = data ? text !== JSON.stringify(data.markets, null, 2) : false;

  async function run(fn: () => Promise<unknown>, ok: string) {
    setBusy(true);
    setErr("");
    setMsg("");
    try {
      await fn();
      setMsg(ok);
      await reload();
    } catch (e) {
      setErr(errorText(e));
    } finally {
      setBusy(false);
    }
  }

  const save = () =>
    run(() => api("PUT", "/admin/v1/markets", undefined, text), "Saved. The servers apply it within about 10 seconds; cached answers of changed countries are dropped.");
  const reset = () => {
    if (confirm("Go back to the countries and shops of the server file / built-in list?"))
      void run(() => api("DELETE", "/admin/v1/markets"), "Reset. The servers apply it within about 10 seconds.");
  };

  const cat = parsed.cat;
  return (
    <>
      <h1>Countries and shops</h1>
      <ErrorBox text={error} />
      {data && (
        <p className="muted">
          Now from {SOURCE_TEXT[data.source] ?? data.source}. Shops use a dedicated source when they are in APIFY_MARKETS or
          SERPAPI_MARKETS (Settings); all others use web search.
        </p>
      )}
      {cat?.countries && (
        <Card title="Overview">
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Country</th>
                  <th>Currency</th>
                  <th>Shops (domain)</th>
                </tr>
              </thead>
              <tbody>
                {Object.entries(cat.countries)
                  .sort(([a], [b]) => a.localeCompare(b))
                  .map(([code, c]) => (
                    <tr key={code}>
                      <td>
                        <b>{code}</b> {c.name}
                      </td>
                      <td>{c.currency}</td>
                      <td>
                        {(c.markets ?? []).map((m) => (
                          <div key={m.market}>
                            {cat.markets?.[m.market]?.name ?? m.market} <span className="muted mono">{m.domain}</span>
                          </div>
                        ))}
                      </td>
                    </tr>
                  ))}
              </tbody>
            </table>
          </div>
        </Card>
      )}
      <Card
        title="Edit (markets.json format)"
        actions={
          <>
            <button onClick={() => cat && setText(JSON.stringify(cat, null, 2))} disabled={!cat}>
              Format
            </button>
            <button className="danger" onClick={reset} disabled={busy || data?.source !== "setting"}>
              Reset to file
            </button>
            <button className="primary" onClick={save} disabled={busy || !changed || !!parsed.error}>
              Save
            </button>
          </>
        }
      >
        {parsed.error && <p className="error">JSON: {parsed.error}</p>}
        <ErrorBox text={err} />
        {msg && <p className="notice ok-text">{msg}</p>}
        <textarea className="mono" rows={28} spellCheck={false} value={text} onChange={(e) => setText(e.target.value)} />
        <p className="muted small">
          "markets": each shop's name, provider ("web"), search_link and product_pages (link patterns). "countries": name, currency,
          region (search region), language and the shops that deliver there with their domain. The server checks everything before saving.
        </p>
      </Card>
    </>
  );
}
