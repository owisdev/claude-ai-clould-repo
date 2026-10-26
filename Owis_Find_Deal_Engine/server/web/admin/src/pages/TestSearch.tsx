import { useState, type FormEvent } from "react";
import { get, post, type Country, type ProbeResult } from "../api";
import { Badge, Card, ErrorBox, errorText, fmtMS, fmtUSD, useLoad } from "../ui";

export function TestSearch() {
  const countries = useLoad(() => get<{ countries: Country[] }>("/api/v1/countries"), []);
  const [title, setTitle] = useState("");
  const [country, setCountry] = useState("jor");
  const [shops, setShops] = useState<string[]>([]);
  const [all, setAll] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [res, setRes] = useState<ProbeResult | null>(null);

  const c = countries.data?.countries.find((x) => x.code === country);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr("");
    try {
      setRes(await post<ProbeResult>("/admin/v1/tools/test-search", { title, country, markets: shops, all_sources: all }));
    } catch (e2) {
      setErr(errorText(e2));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <h1>Test search</h1>
      <p className="muted">
        Asks each shop's source directly — no cache, no quota, no fallback — and shows what it answers, how long it takes and what
        it costs. Paid sources (Apify, SerpApi) are charged for every test.
      </p>
      <Card>
        <form onSubmit={submit} style={{ display: "flex", flexDirection: "column", gap: 12 }}>
          <div className="row">
            <label className="field" style={{ flex: 1, minWidth: 220 }}>
              Product
              <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="usb c hub" required minLength={2} maxLength={200} />
            </label>
            <label className="field">
              Country
              <select
                value={country}
                onChange={(e) => {
                  setCountry(e.target.value);
                  setShops([]);
                }}
              >
                {(countries.data?.countries ?? []).map((x) => (
                  <option key={x.code} value={x.code}>
                    {x.code} · {x.name}
                  </option>
                ))}
              </select>
            </label>
          </div>
          {c && (
            <div className="row">
              <span className="muted">Shops:</span>
              {c.markets.map((m) => (
                <label key={m.id} className="row" style={{ gap: 4 }}>
                  <input
                    type="checkbox"
                    checked={shops.includes(m.id)}
                    onChange={(e) => setShops(e.target.checked ? [...shops, m.id] : shops.filter((s) => s !== m.id))}
                  />
                  {m.name}
                </label>
              ))}
              <span className="muted small">(none ticked = all)</span>
            </div>
          )}
          <label className="row" style={{ gap: 6 }}>
            <input type="checkbox" checked={all} onChange={(e) => setAll(e.target.checked)} />
            Also ask the fallback sources (each paid one costs extra)
          </label>
          <div>
            <button className="primary" disabled={busy}>
              {busy ? "Searching… (scrapers take 20–60 s)" : "Run test search"}
            </button>
          </div>
        </form>
        <ErrorBox text={err || countries.error} />
      </Card>

      {res && (
        <>
          <p>
            <b>{res.sources.length}</b> source calls · {fmtMS(res.took_ms)} · estimated cost <b>{fmtUSD(res.cost_usd)}</b>
          </p>
          {res.sources.map((s, i) => (
            <Card
              key={i}
              title={
                <>
                  {s.market} <span className="muted mono small">{s.provider}</span> {s.fallback && <span className="muted small">(fallback)</span>}
                </>
              }
              actions={
                <>
                  <Badge kind={s.status === "ok" ? "good" : s.status === "error" ? "critical" : "warning"}>{s.status.replace("_", " ")}</Badge>
                  <span className="muted small">
                    {fmtMS(s.took_ms)} · {fmtUSD(s.cost_usd)}
                  </span>
                </>
              }
            >
              {s.error && <p className="error">{s.error}</p>}
              {s.products.length > 0 && (
                <div className="table-wrap">
                  <table>
                    <thead>
                      <tr>
                        <th>Product</th>
                        <th className="num">Price</th>
                      </tr>
                    </thead>
                    <tbody>
                      {s.products.map((p, k) => (
                        <tr key={k}>
                          <td>
                            <a href={p.link} target="_blank" rel="noopener noreferrer">
                              {p.title}
                            </a>
                          </td>
                          <td className="num">{p.price != null ? `${p.currency ?? ""} ${p.price}` : "—"}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </Card>
          ))}
        </>
      )}
    </>
  );
}
