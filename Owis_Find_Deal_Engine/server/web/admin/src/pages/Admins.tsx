import { useState, type FormEvent } from "react";
import { get, post, type AdminEntry } from "../api";
import { Badge, Card, ErrorBox, errorText, fmtTime, useLoad } from "../ui";

export function Admins() {
  const { data, error, reload } = useLoad(() => get<{ admins: AdminEntry[] }>("/admin/v1/admins"), []);
  const [email, setEmail] = useState("");
  const [err, setErr] = useState("");

  async function act(fn: () => Promise<unknown>) {
    setErr("");
    try {
      await fn();
      await reload();
    } catch (e) {
      setErr(errorText(e));
    }
  }

  function add(e: FormEvent) {
    e.preventDefault();
    void act(async () => {
      await post("/admin/v1/admins", { email });
      setEmail("");
    });
  }

  const change = (a: AdminEntry, action: string) => {
    const q: Record<string, string> = {
      disable: `Disable ${a.email}? They lose access at once.`,
      unlink: `Unlink the login of ${a.email}? Their next sign-in with a verified email links it again.`,
    };
    if (q[action] && !confirm(q[action])) return;
    void act(() => post(`/admin/v1/admins/${encodeURIComponent(a.email)}/${action}`));
  };

  return (
    <>
      <h1>Admins</h1>
      <p className="muted">
        Admins sign in with their Firebase account (same project as the app). The first sign-in with a verified email links the
        account to the entry.
      </p>
      <Card title="Add an admin">
        <form className="row" onSubmit={add}>
          <input type="email" placeholder="email" value={email} onChange={(e) => setEmail(e.target.value)} required style={{ minWidth: 260 }} />
          <button className="primary">Add</button>
        </form>
      </Card>
      <ErrorBox text={err || error} />
      <Card>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Email</th>
                <th>Status</th>
                <th>Added</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {(data?.admins ?? []).map((a) => (
                <tr key={a.id}>
                  <td>{a.email}</td>
                  <td>
                    {a.disabled_at ? <Badge kind="critical">disabled</Badge> : <Badge kind="good">active</Badge>}{" "}
                    <span className="muted small">{a.linked ? "signed in before" : "not signed in yet"}</span>
                  </td>
                  <td className="small">
                    {fmtTime(a.created_at)}
                    <div className="muted">{a.created_by}</div>
                  </td>
                  <td className="num" style={{ whiteSpace: "nowrap" }}>
                    {a.disabled_at ? (
                      <button onClick={() => change(a, "enable")}>Enable</button>
                    ) : (
                      <button className="danger" onClick={() => change(a, "disable")}>
                        Disable
                      </button>
                    )}{" "}
                    {a.linked && <button onClick={() => change(a, "unlink")}>Unlink</button>}
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
