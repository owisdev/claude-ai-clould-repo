import { useState } from "react";
import { get, type UsageReport } from "../api";
import { BarChart } from "../chart";
import { Badge, Card, ErrorBox, Tile, fmtInt, fmtMS, fmtPct, fmtUSD, useLoad } from "../ui";

const RANGES = [7, 30, 90];

export function Dashboard() {
  const [days, setDays] = useState(30);
  const { data, error, loading, reload } = useLoad(
    () => get<{ report: UsageReport; note: string }>(`/admin/v1/usage?days=${days}&top=20`),
    [days],
  );
  const r = data?.report;

  return (
    <>
      <h1>Usage and cost</h1>
      <div className="row" style={{ marginBottom: 16 }}>
        <span className="muted">Last</span>
        {RANGES.map((d) => (
          <button key={d} className={d === days ? "primary" : ""} onClick={() => setDays(d)} aria-pressed={d === days}>
            {d} days
          </button>
        ))}
        <span className="spacer" />
        <button onClick={() => reload()} disabled={loading}>
          {loading ? "Loading…" : "Refresh"}
        </button>
      </div>
      <ErrorBox text={error} />
      {r && (
        <div style={{ opacity: loading ? 0.6 : 1 }}>
          <div className="grid-tiles">
            <Tile label="Searches" value={fmtInt(r.totals.searches)} sub={`${fmtInt(r.totals.live)} live`} />
            <Tile label="Cache hit rate" value={fmtPct(r.totals.cache_hit_rate)} sub="answered from cache (free)" />
            <Tile label="Estimated cost" value={fmtUSD(r.totals.cost_usd)} sub={`${r.from} to ${r.to}`} />
            <Tile label="Fallbacks" value={fmtInt(r.totals.fallbacks)} sub={`${fmtInt(r.totals.errors)} failed searches`} />
            <BudgetTile b={r.budget} />
          </div>

          <Card title="Searches per day">
            <BarChart
              label="Searches per day, cached and live"
              labels={r.days.map((d) => d.day)}
              series={[
                { name: "From cache", color: "var(--series-1)", values: r.days.map((d) => d.cache_hits + d.cache_stale) },
                { name: "Live", color: "var(--series-2)", values: r.days.map((d) => d.live) },
              ]}
              format={(v) => fmtInt(Math.round(v))}
            />
          </Card>

          <Card title="Estimated cost per day">
            <BarChart
              label="Estimated cost per day in US dollars"
              labels={r.days.map((d) => d.day)}
              series={[{ name: "Cost", color: "var(--series-1)", values: r.days.map((d) => d.cost_usd) }]}
              format={(v) => fmtUSD(v)}
              height={180}
            />
          </Card>

          <Card title="Sources per shop">
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>Source</th>
                    <th>Shop</th>
                    <th className="num">Calls</th>
                    <th className="num">OK</th>
                    <th className="num">No results</th>
                    <th className="num">Errors</th>
                    <th className="num">Avg time</th>
                    <th className="num">Est. cost</th>
                  </tr>
                </thead>
                <tbody>
                  {r.sources.map((s) => (
                    <tr key={s.provider + s.market}>
                      <td className="mono">{s.provider}</td>
                      <td>{s.market}</td>
                      <td className="num">{fmtInt(s.calls)}</td>
                      <td className="num">{fmtInt(s.ok)}</td>
                      <td className="num">{fmtInt(s.no_results)}</td>
                      <td className="num">
                        {s.errors > 0 && s.error_rate >= 0.2 ? <Badge kind="critical">{`${fmtInt(s.errors)} (${fmtPct(s.error_rate)})`}</Badge> : fmtInt(s.errors)}
                      </td>
                      <td className="num">{fmtMS(s.avg_ms)}</td>
                      <td className="num">{fmtUSD(s.cost_usd)}</td>
                    </tr>
                  ))}
                  {r.sources.length === 0 && (
                    <tr>
                      <td colSpan={8} className="muted">
                        No source calls in this period.
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </Card>

          <div className="grid-tiles" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(340px, 1fr))", alignItems: "start" }}>
            <Card title="Countries">
              <table>
                <thead>
                  <tr>
                    <th>Country</th>
                    <th className="num">Searches</th>
                    <th className="num">Live</th>
                    <th className="num">Cache hit rate</th>
                    <th className="num">Refused</th>
                  </tr>
                </thead>
                <tbody>
                  {r.countries.map((c) => (
                    <tr key={c.country}>
                      <td>{c.country}</td>
                      <td className="num">{fmtInt(c.searches)}</td>
                      <td className="num">{fmtInt(c.live)}</td>
                      <td className="num">{fmtPct(c.cache_hit_rate)}</td>
                      <td className="num">{fmtInt(c.quota_refused)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </Card>
            <Card title="Fallbacks">
              <table>
                <thead>
                  <tr>
                    <th>From</th>
                    <th>To</th>
                    <th>Why</th>
                    <th className="num">Count</th>
                  </tr>
                </thead>
                <tbody>
                  {r.fallbacks.map((f) => (
                    <tr key={f.chain + f.from + f.to + f.reason}>
                      <td className="mono">{f.from}</td>
                      <td className="mono">{f.to}</td>
                      <td>{f.reason.replace("_", " ")}</td>
                      <td className="num">{fmtInt(f.count)}</td>
                    </tr>
                  ))}
                  {r.fallbacks.length === 0 && (
                    <tr>
                      <td colSpan={4} className="muted">
                        None.
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </Card>
          </div>

          <Card title="Top queries">
            <table>
              <thead>
                <tr>
                  <th>Query</th>
                  <th>Country</th>
                  <th className="num">Searches</th>
                  <th className="num">Live</th>
                </tr>
              </thead>
              <tbody>
                {r.top_queries.map((q) => (
                  <tr key={q.country + q.query}>
                    <td>{q.query}</td>
                    <td>{q.country}</td>
                    <td className="num">{fmtInt(q.searches)}</td>
                    <td className="num">{fmtInt(q.live)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </Card>
          <p className="muted small">{data?.note}</p>
        </div>
      )}
    </>
  );
}

function BudgetTile({ b }: { b: UsageReport["budget"] }) {
  if (b.status === "none") return <Tile label={`Budget ${b.month}`} value={fmtUSD(b.spent_usd)} sub="no monthly budget set" />;
  const kind = b.status === "exceeded" ? "critical" : b.status === "warning" ? "warning" : "good";
  const label = b.status === "exceeded" ? "Used up" : b.status === "warning" ? `Over ${b.alert_percent}%` : "OK";
  return (
    <Tile label={`Budget ${b.month}`} value={fmtUSD(b.spent_usd)} sub={<>of {fmtUSD(b.limit_usd)} · <Badge kind={kind}>{label}</Badge></>}>
      <div
        className={"meter " + (kind === "good" ? "" : kind)}
        role="meter"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.min(b.percent, 100)}
        aria-label="Share of the monthly budget used"
      >
        <div style={{ width: `${Math.min(b.percent, 100)}%` }} />
      </div>
    </Tile>
  );
}
