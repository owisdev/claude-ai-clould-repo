import { useEffect, useState } from "react";
import { get, type AuditEntry } from "../api";
import { Badge, Card, ErrorBox, errorText, fmtTime } from "../ui";

const ACTIONS = ["", "settings.", "markets.", "admins.", "tools.", "admin.login", "budget."];

export function Audit() {
  const [action, setAction] = useState("");
  const [actor, setActor] = useState("");
  const [entries, setEntries] = useState<AuditEntry[]>([]);
  const [more, setMore] = useState(false);
  const [err, setErr] = useState("");

  async function load(before?: number) {
    setErr("");
    const q = new URLSearchParams({ limit: "100", action, actor });
    if (before) q.set("before", String(before));
    try {
      const r = await get<{ entries: AuditEntry[] }>(`/admin/v1/audit?${q}`);
      setEntries(before ? [...entries, ...r.entries] : r.entries);
      setMore(r.entries.length === 100);
    } catch (e) {
      setErr(errorText(e));
    }
  }

  useEffect(() => {
    void load();
  }, [action]);

  return (
    <>
      <h1>Audit log</h1>
      <div className="row" style={{ marginBottom: 16 }}>
        <select value={action} onChange={(e) => setAction(e.target.value)} aria-label="Action">
          {ACTIONS.map((a) => (
            <option key={a} value={a}>
              {a || "all actions"}
            </option>
          ))}
        </select>
        <input placeholder="actor, e.g. admin:you@example.com" value={actor} onChange={(e) => setActor(e.target.value)} style={{ minWidth: 260 }} />
        <button onClick={() => load()}>Apply</button>
      </div>
      <ErrorBox text={err} />
      <Card>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>When</th>
                <th>Who</th>
                <th>Action</th>
                <th>Target</th>
                <th>Details</th>
              </tr>
            </thead>
            <tbody>
              {entries.map((e) => (
                <tr key={e.id}>
                  <td className="small" style={{ whiteSpace: "nowrap" }}>
                    {fmtTime(e.at)}
                  </td>
                  <td className="small">
                    {e.actor}
                    {e.remote_ip && <div className="muted">{e.remote_ip}</div>}
                  </td>
                  <td>
                    <span className="mono">{e.action}</span>{" "}
                    {e.outcome !== "ok" && <Badge kind={e.outcome === "denied" ? "critical" : "warning"}>{e.outcome}</Badge>}
                  </td>
                  <td className="small" style={{ wordBreak: "break-all" }}>
                    {e.target}
                  </td>
                  <td className="mono small" style={{ wordBreak: "break-all" }}>
                    {e.detail && JSON.stringify(e.detail) !== "{}" ? JSON.stringify(e.detail) : ""}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {more && (
          <button style={{ marginTop: 12 }} onClick={() => load(entries[entries.length - 1]?.id)}>
            Older entries
          </button>
        )}
      </Card>
    </>
  );
}
