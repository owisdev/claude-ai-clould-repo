import { useState, type FormEvent } from "react";
import { api, get, post, type ApiClient, type ApiKeyInfo, type Country, type NewKeyAnswer, type WebhookSecretAnswer } from "../api";
import { Badge, Card, ErrorBox, errorText, fmtInt, fmtTime, useLoad } from "../ui";

// Form state; numbers stay strings while typing.
interface Form {
  name: string;
  owner: string;
  scopes: string[];
  daily_quota: string;
  rate_limit_rps: string;
  rate_limit_burst: string;
  countries: string[];
  ip_allow: string;
  expires: string; // yyyy-mm-dd, empty = never
}

const emptyForm: Form = {
  name: "",
  owner: "",
  scopes: ["countries", "search"],
  daily_quota: "100",
  rate_limit_rps: "1",
  rate_limit_burst: "5",
  countries: [],
  ip_allow: "",
  expires: "",
};

function formOf(c: ApiClient): Form {
  return {
    name: c.name,
    owner: c.owner,
    scopes: c.scopes,
    daily_quota: String(c.daily_quota),
    rate_limit_rps: String(c.rate_limit_rps),
    rate_limit_burst: String(c.rate_limit_burst),
    countries: c.countries,
    ip_allow: c.ip_allow.join("\n"),
    expires: c.expires_at ? c.expires_at.slice(0, 10) : "",
  };
}

function bodyOf(f: Form) {
  return {
    name: f.name,
    owner: f.owner,
    scopes: f.scopes,
    daily_quota: Number(f.daily_quota),
    rate_limit_rps: Number(f.rate_limit_rps),
    rate_limit_burst: Number(f.rate_limit_burst),
    countries: f.countries,
    ip_allow: f.ip_allow.split(/[\s,]+/).filter(Boolean),
    // The key works through the whole chosen day (UTC).
    expires_at: f.expires ? `${f.expires}T23:59:59Z` : null,
  };
}

const statusKind = (s: string) => (s === "active" ? "good" : s === "grace" ? "warning" : "critical");

export function Clients() {
  const { data, error, reload } = useLoad(() => get<{ clients: ApiClient[]; scopes: string[] }>("/admin/v1/clients"), []);
  const countries = useLoad(() => get<{ countries: Country[] }>("/api/v1/countries"), []);
  const [editing, setEditing] = useState<ApiClient | null>(null);
  const [form, setForm] = useState<Form>(emptyForm);
  const [newKey, setNewKey] = useState<NewKeyAnswer | null>(null);
  const [newSecret, setNewSecret] = useState<WebhookSecretAnswer | null>(null);
  const [showRevoked, setShowRevoked] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");

  async function act(fn: () => Promise<unknown>) {
    setErr("");
    setBusy(true);
    try {
      await fn();
      await reload();
    } catch (e) {
      setErr(errorText(e));
    } finally {
      setBusy(false);
    }
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    void act(async () => {
      if (editing) {
        await api("PATCH", `/admin/v1/clients/${editing.id}`, bodyOf(form));
        setEditing(null);
      } else {
        setNewKey(await post<NewKeyAnswer>("/admin/v1/clients", bodyOf(form)));
      }
      setForm(emptyForm);
    });
  }

  function edit(c: ApiClient) {
    setEditing(c);
    setForm(formOf(c));
    setErr("");
    window.scrollTo({ top: 0, behavior: "smooth" });
  }

  function rotate(c: ApiClient) {
    const hours = prompt(
      `New key for "${c.name}". How many hours should the current key keep working (0 = stop it at once, at most 720)?`,
      "24",
    );
    if (hours === null) return;
    const n = Number(hours);
    if (!Number.isFinite(n) || n < 0 || n > 720) {
      setErr("Enter a number of hours from 0 to 720.");
      return;
    }
    void act(async () => setNewKey(await post<NewKeyAnswer>(`/admin/v1/clients/${c.id}/rotate`, { grace: `${n}h` })));
  }

  function webhookSecret(c: ApiClient) {
    const replace = c.webhook_secret_prefix
      ? ` The current secret (${c.webhook_secret_prefix}…) stops signing at once: update the receiver right after.`
      : "";
    if (!confirm(`New webhook signing secret for "${c.name}"?${replace}`)) return;
    void act(async () => setNewSecret(await post<WebhookSecretAnswer>(`/admin/v1/clients/${c.id}/webhook-secret`)));
  }

  function revoke(c: ApiClient) {
    if (!confirm(`Revoke "${c.name}"? All its keys stop working at once. This cannot be undone.`)) return;
    void act(() => post(`/admin/v1/clients/${c.id}/revoke`));
  }

  function revokeKey(c: ApiClient, k: ApiKeyInfo) {
    if (!confirm(`Stop key ${k.prefix}… of "${c.name}" now?`)) return;
    void act(() => post(`/admin/v1/clients/${c.id}/keys/${k.id}/revoke`));
  }

  const set = <K extends keyof Form>(k: K, v: Form[K]) => setForm((f) => ({ ...f, [k]: v }));
  const toggle = (k: "scopes" | "countries", v: string) =>
    setForm((f) => ({ ...f, [k]: f[k].includes(v) ? f[k].filter((x) => x !== v) : [...f[k], v] }));

  const all = data?.clients ?? [];
  const list = showRevoked ? all : all.filter((c) => c.status !== "revoked");
  const scopes = data?.scopes ?? ["search", "countries"];

  return (
    <>
      <h1>API clients</h1>
      <p className="muted">
        Keys for your own servers and AI agents. They call the same API as the app, with{" "}
        <code>Authorization: Bearer ofd_live_…</code>. Never put a key inside a mobile or web app (it could be extracted): the
        app keeps using the user's login. A key is shown once; only its hash is stored.
      </p>

      {newKey && <NewKeyBox answer={newKey} onClose={() => setNewKey(null)} />}
      {newSecret && <NewSecretBox answer={newSecret} onClose={() => setNewSecret(null)} />}

      <Card
        title={editing ? `Change "${editing.name}"` : "New client"}
        actions={
          editing && (
            <button
              onClick={() => {
                setEditing(null);
                setForm(emptyForm);
              }}
            >
              Cancel
            </button>
          )
        }
      >
        <form onSubmit={submit} style={{ display: "flex", flexDirection: "column", gap: 12 }}>
          <div className="row">
            <label className="field" style={{ flex: 1, minWidth: 200 }}>
              Name
              <input value={form.name} onChange={(e) => set("name", e.target.value)} required maxLength={100} placeholder="price-alert agent" />
            </label>
            <label className="field" style={{ flex: 1, minWidth: 200 }}>
              Owner
              <input value={form.owner} onChange={(e) => set("owner", e.target.value)} maxLength={200} placeholder="who runs it" />
            </label>
          </div>
          <div className="row">
            <label className="field">
              Live searches per day
              <input type="number" min={1} max={1000000} value={form.daily_quota} onChange={(e) => set("daily_quota", e.target.value)} required style={{ width: 150 }} />
            </label>
            <label className="field">
              Requests per second
              <input type="number" min={0.01} max={100} step="any" value={form.rate_limit_rps} onChange={(e) => set("rate_limit_rps", e.target.value)} required style={{ width: 150 }} />
            </label>
            <label className="field">
              Burst
              <input type="number" min={1} max={1000} value={form.rate_limit_burst} onChange={(e) => set("rate_limit_burst", e.target.value)} required style={{ width: 110 }} />
            </label>
            <label className="field">
              Expires (end of day, UTC)
              <input type="date" value={form.expires} onChange={(e) => set("expires", e.target.value)} />
            </label>
          </div>
          <div className="row" style={{ alignItems: "flex-start", gap: 28 }}>
            <fieldset className="checks">
              <legend>Scopes</legend>
              {scopes.map((s) => (
                <label key={s}>
                  <input type="checkbox" checked={form.scopes.includes(s)} onChange={() => toggle("scopes", s)} /> {s}
                </label>
              ))}
            </fieldset>
            <fieldset className="checks">
              <legend>Countries (none ticked = all)</legend>
              {(countries.data?.countries ?? []).map((c) => (
                <label key={c.code}>
                  <input type="checkbox" checked={form.countries.includes(c.code)} onChange={() => toggle("countries", c.code)} /> {c.code} ·{" "}
                  {c.name}
                </label>
              ))}
            </fieldset>
            <label className="field" style={{ flex: 1, minWidth: 220 }}>
              Allowed addresses (one IP or CIDR per line; empty = any)
              <textarea rows={3} value={form.ip_allow} onChange={(e) => set("ip_allow", e.target.value)} placeholder={"203.0.113.7\n198.51.100.0/24"} className="mono" />
            </label>
          </div>
          <p className="muted small" style={{ margin: 0 }}>
            Cached answers are free and do not count; each live search counts once against the daily limit (answers 429 when used
            up). The <code>countries</code> scope is for the agent tools of step 13 (<code>/api/v1/countries</code> is public).
          </p>
          <ErrorBox text={err} />
          <div className="row">
            <button className="primary" disabled={busy}>
              {editing ? "Save changes" : "Create client and key"}
            </button>
          </div>
        </form>
      </Card>

      <ErrorBox text={error} />
      <Card
        title="Clients"
        actions={
          <label className="small muted">
            <input type="checkbox" checked={showRevoked} onChange={(e) => setShowRevoked(e.target.checked)} /> show revoked
          </label>
        }
      >
        {list.length === 0 ? (
          <p className="muted">No API clients yet.</p>
        ) : (
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>Client</th>
                  <th>Status</th>
                  <th>Access</th>
                  <th className="num">Limits</th>
                  <th>Keys</th>
                  <th>Webhook</th>
                  <th>Last used</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {list.map((c) => (
                  <tr key={c.id}>
                    <td>
                      <b>{c.name}</b>
                      <div className="muted small">
                        #{c.id}
                        {c.owner && ` · ${c.owner}`}
                      </div>
                    </td>
                    <td>
                      <Badge kind={statusKind(c.status)}>{c.status}</Badge>
                      {c.expires_at && c.status === "active" && <div className="muted small">until {fmtTime(c.expires_at)}</div>}
                    </td>
                    <td className="small">
                      {c.scopes.join(", ")}
                      <div className="muted">{c.countries.length ? c.countries.join(", ") : "all countries"}</div>
                      <div className="muted mono">{c.ip_allow.length ? c.ip_allow.join(" ") : "any address"}</div>
                    </td>
                    <td className="num small" style={{ whiteSpace: "nowrap" }}>
                      {fmtInt(c.daily_quota)} / day
                      <div className="muted">
                        {c.rate_limit_rps}/s, burst {c.rate_limit_burst}
                      </div>
                    </td>
                    <td className="small">
                      {c.keys
                        .filter((k) => k.status === "active" || k.status === "grace" || c.status === "revoked")
                        .map((k) => (
                          <div key={k.id} className="row" style={{ gap: 6, marginBottom: 2 }}>
                            <code>{k.prefix}…</code>
                            {c.status !== "revoked" && <Badge kind={statusKind(k.status)}>{k.status === "grace" ? `until ${fmtTime(k.expires_at)}` : k.status}</Badge>}
                            {k.status === "grace" && c.status !== "revoked" && (
                              <button className="small danger" onClick={() => revokeKey(c, k)} title="Stop this key now">
                                Stop now
                              </button>
                            )}
                          </div>
                        ))}
                    </td>
                    <td className="small">
                      {c.webhook_secret_prefix ? (
                        <>
                          <code>{c.webhook_secret_prefix}…</code>
                          <div className="muted">since {fmtTime(c.webhook_secret_at ?? undefined)}</div>
                        </>
                      ) : (
                        <span className="muted">no secret</span>
                      )}
                    </td>
                    <td className="small">{c.last_used_at ? fmtTime(c.last_used_at) : <span className="muted">never</span>}</td>
                    <td className="num" style={{ whiteSpace: "nowrap" }}>
                      {c.status !== "revoked" && (
                        <>
                          <button onClick={() => edit(c)} disabled={busy}>
                            Edit
                          </button>{" "}
                          <button onClick={() => rotate(c)} disabled={busy}>
                            New key
                          </button>{" "}
                          <button onClick={() => webhookSecret(c)} disabled={busy} title="Signing secret for the webhooks of asynchronous searches">
                            Webhook secret
                          </button>{" "}
                          <button className="danger" onClick={() => revoke(c)} disabled={busy}>
                            Revoke
                          </button>
                        </>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </>
  );
}

function NewKeyBox({ answer, onClose }: { answer: NewKeyAnswer; onClose: () => void }) {
  const [copied, setCopied] = useState<"" | "yes" | "no">("");
  const curl = `curl -X POST ${location.origin}/api/v1/search -H "Authorization: Bearer ${answer.key}" -H "Content-Type: application/json" -d '{"title":"usb c hub","country":"${answer.client.countries[0] ?? "jor"}"}'`;
  return (
    <Card title={`Key for "${answer.client.name}" — copy it now, it is shown only once`}>
      <div className="row">
        <input id="new-key" readOnly value={answer.key} className="mono" style={{ flex: 1, minWidth: 280 }} onFocus={(e) => e.target.select()} aria-label="API key" />
        <button
          className="primary"
          onClick={() =>
            (navigator.clipboard?.writeText(answer.key) ?? Promise.reject()).then(
              () => setCopied("yes"),
              () => {
                setCopied("no");
                (document.getElementById("new-key") as HTMLInputElement | null)?.select();
              },
            )
          }
        >
          {copied === "yes" ? "Copied" : "Copy"}
        </button>
        <button onClick={onClose}>I have saved it</button>
      </div>
      {copied === "no" && <p className="notice">The browser did not allow copying: the key is selected, press Ctrl+C (⌘C).</p>}
      {answer.old_keys_until && <p className="notice">The previous key keeps working until {fmtTime(answer.old_keys_until)}.</p>}
      <p className="muted small">Try it:</p>
      <pre className="mono small" style={{ whiteSpace: "pre-wrap", wordBreak: "break-all", margin: 0 }}>
        {curl}
      </pre>
    </Card>
  );
}

function NewSecretBox({ answer, onClose }: { answer: WebhookSecretAnswer; onClose: () => void }) {
  const [copied, setCopied] = useState<"" | "yes" | "no">("");
  const country = answer.client.countries[0] ?? "jor";
  const curl = `curl -X POST ${location.origin}/api/v1/searches -H "Authorization: Bearer ofd_live_…" -H "Content-Type: application/json" -d '{"title":"usb c hub","country":"${country}","webhook_url":"https://your-server.example/hooks/search"}'`;
  return (
    <Card title={`Webhook secret for "${answer.client.name}" — copy it now, it is shown only once`}>
      <div className="row">
        <input id="new-secret" readOnly value={answer.webhook_secret} className="mono" style={{ flex: 1, minWidth: 280 }} onFocus={(e) => e.target.select()} aria-label="Webhook secret" />
        <button
          className="primary"
          onClick={() =>
            (navigator.clipboard?.writeText(answer.webhook_secret) ?? Promise.reject()).then(
              () => setCopied("yes"),
              () => {
                setCopied("no");
                (document.getElementById("new-secret") as HTMLInputElement | null)?.select();
              },
            )
          }
        >
          {copied === "yes" ? "Copied" : "Copy"}
        </button>
        <button onClick={onClose}>I have saved it</button>
      </div>
      {copied === "no" && <p className="notice">The browser did not allow copying: the secret is selected, press Ctrl+C (⌘C).</p>}
      <p className="muted small">
        Each webhook carries <code>X-OFD-Signature: t=…,v1=…</code>: HMAC-SHA256 of <code>&lt;t&gt;.&lt;raw body&gt;</code> with this
        secret. The receiver recomputes it, compares in constant time and refuses timestamps older than 5 minutes. Start a search
        with a webhook:
      </p>
      <pre className="mono small" style={{ whiteSpace: "pre-wrap", wordBreak: "break-all", margin: 0 }}>
        {curl}
      </pre>
    </Card>
  );
}
