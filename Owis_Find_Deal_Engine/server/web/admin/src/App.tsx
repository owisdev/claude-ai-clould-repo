import { useEffect, useState, type FormEvent } from "react";
import type { User } from "@firebase/auth";
import { ApiError, get, setAuthErrorListener, type Me } from "./api";
import { authErrorText, initAuth, loadConfig, logOut, resetPassword, signIn, verifyEmail, watchUser, type AdminConfig } from "./auth";
import { ErrorBox, errorText } from "./ui";
import { Dashboard } from "./pages/Dashboard";
import { Settings } from "./pages/Settings";
import { Markets } from "./pages/Markets";
import { TestSearch } from "./pages/TestSearch";
import { Tools } from "./pages/Tools";
import { Admins } from "./pages/Admins";
import { Clients } from "./pages/Clients";
import { Audit } from "./pages/Audit";

const PAGES = [
  { path: "dashboard", title: "Dashboard", el: () => <Dashboard /> },
  { path: "settings", title: "Settings", el: () => <Settings /> },
  { path: "markets", title: "Countries & shops", el: () => <Markets /> },
  { path: "test-search", title: "Test search", el: () => <TestSearch /> },
  { path: "tools", title: "Cache & sources", el: () => <Tools /> },
  { path: "clients", title: "API clients", el: () => <Clients /> },
  { path: "admins", title: "Admins", el: () => <Admins /> },
  { path: "audit", title: "Audit log", el: () => <Audit /> },
];

function useHashPage(): string {
  const read = () => window.location.hash.replace(/^#\/?/, "") || "dashboard";
  const [page, setPage] = useState(read);
  useEffect(() => {
    const on = () => setPage(read());
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return page;
}

export function App() {
  const [cfg, setCfg] = useState<AdminConfig | null>(null);
  const [cfgError, setCfgError] = useState("");
  const [user, setUser] = useState<User | null | undefined>(undefined);
  const [me, setMe] = useState<Me | null>(null);
  const [denied, setDenied] = useState("");

  useEffect(() => {
    loadConfig()
      .then((c) => {
        if (!c.firebase_api_key) throw new Error("ADMIN_FIREBASE_API_KEY is not set on the server (see .env.example).");
        initAuth(c);
        setCfg(c);
        return watchUser(setUser);
      })
      .catch((e) => setCfgError(errorText(e)));
  }, []);

  useEffect(() => {
    setAuthErrorListener((e: ApiError) => {
      setMe(null);
      setDenied(e.message);
      if (e.code === "session_expired") void logOut();
    });
  }, []);

  useEffect(() => {
    if (!user) {
      setMe(null);
      return;
    }
    setDenied("");
    get<Me>("/admin/v1/me")
      .then(setMe)
      .catch((e) => setDenied(errorText(e)));
  }, [user?.uid]);

  if (cfgError) return <Centered><ErrorBox text={cfgError} /></Centered>;
  if (!cfg || user === undefined) return <Centered><p className="muted">Loading…</p></Centered>;
  if (!user) return <SignIn />;
  if (!me) return <NotAdmin user={user} denied={denied} needVerified={cfg.require_verified_email} />;
  return <Shell me={me} />;
}

function Centered({ children }: { children: React.ReactNode }) {
  return <div className="signin card">{children}</div>;
}

function SignIn() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [info, setInfo] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await signIn(email, password);
    } catch (err) {
      setError(authErrorText(err));
    } finally {
      setBusy(false);
    }
  }

  async function reset() {
    setError("");
    if (!email) {
      setError("Enter your email first.");
      return;
    }
    try {
      await resetPassword(email);
      setInfo("If the account exists, a reset email is on its way.");
    } catch (err) {
      setError(authErrorText(err));
    }
  }

  return (
    <div className="signin card">
      <h1>Admin sign-in</h1>
      <form onSubmit={submit}>
        <label className="field">
          Email
          <input type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} required />
        </label>
        <label className="field">
          Password
          <input type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
        </label>
        <ErrorBox text={error} />
        {info && <p className="notice">{info}</p>}
        <div className="row">
          <button className="primary" disabled={busy}>
            {busy ? "Signing in…" : "Sign in"}
          </button>
          <span className="spacer" />
          <button type="button" onClick={reset}>
            Forgot password
          </button>
        </div>
      </form>
      <p className="muted small">The session ends when you close this tab.</p>
    </div>
  );
}

function NotAdmin({ user, denied, needVerified }: { user: User; denied: string; needVerified: boolean }) {
  const [sent, setSent] = useState(false);
  return (
    <div className="signin card">
      <h1>No admin access</h1>
      <p>
        Signed in as <b>{user.email}</b>.
      </p>
      {denied ? <ErrorBox text={denied} /> : <p className="muted">Checking…</p>}
      {denied && (
        <p className="muted small">
          Only emails on the admin list can enter. Another admin can add yours on the Admins page, or on the server:{" "}
          <code>owis_find_deal_engine admins add {user.email}</code>
        </p>
      )}
      {needVerified && !user.emailVerified && (
        <p className="notice">
          Your email is not verified. Admins must verify their email once.{" "}
          {sent ? (
            "Email sent: open the link, then sign in again."
          ) : (
            <button onClick={() => verifyEmail().then(() => setSent(true))}>Send verification email</button>
          )}
        </p>
      )}
      <button onClick={() => logOut()}>Sign out</button>
    </div>
  );
}

function Shell({ me }: { me: Me }) {
  const page = useHashPage();
  const current = PAGES.find((p) => p.path === page) ?? PAGES[0];
  useEffect(() => {
    document.title = `${current.title} · Owis Find Deal admin`;
  }, [current.title]);
  return (
    <div className="layout">
      <nav className="nav" aria-label="Admin">
        <div className="brand">Owis Find Deal</div>
        {PAGES.map((p) => (
          <a key={p.path} href={`#/${p.path}`} className={p === current ? "active" : ""} aria-current={p === current ? "page" : undefined}>
            {p.title}
          </a>
        ))}
        <div className="who">
          {me.email}
          <br />
          <button className="small" style={{ marginTop: 6 }} onClick={() => logOut()}>
            Sign out
          </button>
        </div>
      </nav>
      <main className="main">{current.el()}</main>
    </div>
  );
}
