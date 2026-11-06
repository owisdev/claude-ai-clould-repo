import { useState, type FormEvent } from "react";
import { get, post, type ChainHealth, type RuntimeStatus } from "../api";
import { Badge, Card, ErrorBox, errorText, fmtTime, useLoad } from "../ui";

export function Tools() {
  const providers = useLoad(() => get<{ settings_version: number; chains: ChainHealth[] }>("/admin/v1/tools/providers"), []);
  const status = useLoad(() => get<{ runtime: RuntimeStatus; stored_settings_version: number }>("/admin/v1/status"), []);
  const [title, setTitle] = useState("");
  const [country, setCountry] = useState("jor");
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");

  async function forget(e: FormEvent) {
    e.preventDefault();
    setErr("");
    setMsg("");
    try {
      const r = await post<{ found: boolean }>("/admin/v1/tools/cache/forget", { title, country });
      setMsg(r.found ? "Removed: the next search for it is live." : "It was not cached.");
    } catch (e2) {
      setErr(errorText(e2));
    }
  }

  async function bump() {
    if (!confirm("Drop every cached answer? The next search of each query is live (and may cost money).")) return;
    setErr("");
    setMsg("");
    try {
      await post("/admin/v1/tools/cache/bump");
      setMsg("Done: every cached answer is dropped once the servers apply it (about 10 seconds).");
      void status.reload();
    } catch (e2) {
      setErr(errorText(e2));
    }
  }

  const rt = status.data?.runtime;
  return (
    <>
      <h1>Cache and sources</h1>
      <ErrorBox text={status.error} />
      {rt && (
        <Card title="Server">
          <table>
            <tbody>
              <tr>
                <th>Build</th>
                <td className="mono">{rt.version}</td>
              </tr>
              <tr>
                <th>Settings version</th>
                <td>
                  {rt.settings_version} applied{" "}
                  {status.data!.stored_settings_version !== rt.settings_version && (
                    <Badge kind="warning">{`${status.data!.stored_settings_version} stored, applying within ${rt.settings_poll_interval}`}</Badge>
                  )}
                </td>
              </tr>
              <tr>
                <th>Countries and shops</th>
                <td>{rt.markets_source}</td>
              </tr>
              <tr>
                <th>Cache</th>
                <td>
                  {rt.cache_enabled ? "on" : "off"} · generation {rt.cache_generation}
                </td>
              </tr>
            </tbody>
          </table>
        </Card>
      )}

      <Card title="Cache">
        <form className="row" onSubmit={forget}>
          <input placeholder="product title" value={title} onChange={(e) => setTitle(e.target.value)} required minLength={2} style={{ flex: 1, minWidth: 200 }} />
          <input placeholder="country" value={country} onChange={(e) => setCountry(e.target.value)} required style={{ width: 90 }} />
          <button>Forget this search</button>
          <span className="spacer" />
          <button type="button" className="danger" onClick={bump}>
            Drop all cached answers
          </button>
        </form>
        <ErrorBox text={err} />
        {msg && <p className="notice ok-text">{msg}</p>}
      </Card>

      <Card title="Sources (circuit breaker)" actions={<button onClick={() => providers.reload()}>Refresh</button>}>
        <p className="muted small">
          A source that fails several times in a row is skipped for a while ("open"); then one search tries it again ("half open").
          The last source of a chain is always tried. This is this server's view.
        </p>
        <ErrorBox text={providers.error} />
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Chain</th>
                <th>Source</th>
                <th>State</th>
                <th className="num">Failures in a row</th>
              </tr>
            </thead>
            <tbody>
              {(providers.data?.chains ?? []).flatMap((c) =>
                c.sources.map((s, i) => (
                  <tr key={c.name + i}>
                    <td className="mono">{i === 0 ? c.name : ""}</td>
                    <td className="mono">
                      {s.provider} {s.last_resort && <span className="muted small">(last resort)</span>}
                    </td>
                    <td>
                      <Badge kind={s.state === "closed" ? "good" : s.state === "open" ? "critical" : "warning"}>
                        {s.state === "closed" ? "working" : s.state === "open" ? `skipped until ${fmtTime(s.open_until)}` : "trying again"}
                      </Badge>
                    </td>
                    <td className="num">{s.consecutive_failures}</td>
                  </tr>
                )),
              )}
            </tbody>
          </table>
        </div>
      </Card>
    </>
  );
}
