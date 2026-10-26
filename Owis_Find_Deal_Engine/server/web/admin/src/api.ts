// Admin API client: same origin, Firebase ID token as bearer.
import { idToken } from "./auth";

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
  }
}

type Listener = (e: ApiError) => void;
let onAuthError: Listener = () => {};

/** Called on 401 / 403 answers that mean "sign in again" or "not an admin". */
export function setAuthErrorListener(l: Listener) {
  onAuthError = l;
}

export async function api<T>(method: string, path: string, body?: unknown, raw?: string): Promise<T> {
  const token = await idToken();
  const headers: Record<string, string> = {};
  if (token) headers.Authorization = `Bearer ${token}`;
  if (body !== undefined || raw !== undefined) headers["Content-Type"] = "application/json";
  const res = await fetch(path, {
    method,
    headers,
    body: raw ?? (body !== undefined ? JSON.stringify(body) : undefined),
    cache: "no-store",
  });
  const text = await res.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = null;
  }
  if (!res.ok) {
    const e = (data as { error?: { code?: string; message?: string } } | null)?.error;
    const err = new ApiError(res.status, e?.code ?? "http_" + res.status, e?.message ?? (text || res.statusText));
    if (res.status === 401 || (res.status === 403 && err.code !== "conflict")) onAuthError(err);
    throw err;
  }
  return data as T;
}

export const get = <T>(path: string) => api<T>("GET", path);
export const post = <T>(path: string, body?: unknown) => api<T>("POST", path, body ?? {});

// ---- response types (see the server README, section "Admin API") ----

export interface Me {
  email: string;
  uid: string;
  second_factor: string;
  signed_in_at: string;
  admin_since: string;
}

export interface Setting {
  key: string;
  value?: string;
  secret?: boolean;
  set: boolean;
  updated_at?: string;
  updated_by?: string;
}

export interface Change {
  id: number;
  key: string;
  old: string | null;
  new: string | null;
  secret?: boolean;
  changed_by: string;
  changed_at: string;
}

export interface AdminEntry {
  id: number;
  email: string;
  linked: boolean;
  created_at: string;
  created_by: string;
  disabled_at?: string;
}

export interface AuditEntry {
  id: number;
  at: string;
  actor: string;
  action: string;
  target?: string;
  detail?: unknown;
  outcome: string;
  request_id?: string;
  remote_ip?: string;
}

export interface Product {
  market: string;
  title: string;
  link: string;
  price?: number;
  currency?: string;
  thumbnail?: string;
  provider: string;
}

export interface SourceAnswer {
  market: string;
  provider: string;
  fallback?: boolean;
  status: string;
  error?: string;
  took_ms: number;
  cost_usd: number;
  products: Product[];
}

export interface ProbeResult {
  query: string;
  country: string;
  sources: SourceAnswer[];
  took_ms: number;
  cost_usd: number;
}

export interface Country {
  code: string;
  name: string;
  currency: string;
  markets: { id: string; name: string; domain: string }[];
}

export interface SourceHealth {
  provider: string;
  state: string;
  consecutive_failures: number;
  open_until?: string;
  last_resort?: boolean;
}

export interface ChainHealth {
  name: string;
  chain: string;
  sources: SourceHealth[];
}

export interface RuntimeStatus {
  version: string;
  settings_version: number;
  settings_poll_interval: string;
  markets_source: string;
  cache_enabled: boolean;
  cache_generation: number;
  budget_monthly_usd: number;
  budget_alert_percent: number;
}

export interface SearchCounts {
  searches: number;
  cache_hits: number;
  cache_stale: number;
  live: number;
  quota_refused: number;
  errors: number;
  cache_hit_rate: number;
}

export interface Totals extends SearchCounts {
  source_calls: number;
  fallbacks: number;
  cost_usd: number;
}

export interface UsageReport {
  from: string;
  to: string;
  totals: Totals;
  days: (Totals & { day: string })[];
  countries: (SearchCounts & { country: string })[];
  sources: {
    provider: string;
    market: string;
    calls: number;
    ok: number;
    no_results: number;
    errors: number;
    error_rate: number;
    avg_ms: number;
    cost_usd: number;
  }[];
  fallbacks: { chain: string; from: string; to: string; reason: string; count: number }[];
  top_queries: { country: string; query: string; searches: number; live: number }[];
  clients: (SearchCounts & {
    client: string;
    client_id: number;
    name: string;
    cost_usd_estimate: number;
    last_day: string;
  })[];
  budget: {
    month: string;
    limit_usd: number;
    spent_usd: number;
    percent: number;
    alert_percent: number;
    status: "none" | "ok" | "warning" | "exceeded";
  };
}

export interface ApiKeyInfo {
  id: number;
  prefix: string;
  status: "active" | "grace" | "expired" | "revoked";
  created_at: string;
  created_by: string;
  expires_at?: string;
  revoked_at?: string;
  last_used_at: string | null;
}

export interface ApiClient {
  id: number;
  name: string;
  owner: string;
  status: "active" | "expired" | "revoked";
  scopes: string[];
  daily_quota: number;
  rate_limit_rps: number;
  rate_limit_burst: number;
  countries: string[];
  ip_allow: string[];
  expires_at: string | null;
  created_at: string;
  created_by: string;
  updated_at: string;
  updated_by: string;
  revoked_at?: string;
  revoked_by?: string;
  last_used_at: string | null;
  keys: ApiKeyInfo[];
  /** First characters of the webhook signing secret; "" = none yet. */
  webhook_secret_prefix: string;
  webhook_secret_at: string | null;
}

/** Answer of making a webhook signing secret: shown once. */
export interface WebhookSecretAnswer {
  client: ApiClient;
  webhook_secret: string;
  note: string;
}

/** Answer of creating a client or rotating its key: the key is shown once. */
export interface NewKeyAnswer {
  client: ApiClient;
  key: string;
  old_keys_until?: string;
  note: string;
}
