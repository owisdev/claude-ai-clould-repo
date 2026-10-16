import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { ApiError } from "./api";

export function errorText(e: unknown): string {
  if (e instanceof ApiError) return e.message;
  return e instanceof Error ? e.message : String(e);
}

/** Loads data with fn; reload() runs it again, keeping the old data meanwhile. */
export function useLoad<T>(fn: () => Promise<T>, deps: unknown[]) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string>("");
  const [loading, setLoading] = useState(true);
  const seq = useRef(0);
  const load = useCallback(fn, deps);
  const reload = useCallback(async () => {
    const n = ++seq.current;
    setLoading(true);
    try {
      const d = await load();
      if (n === seq.current) {
        setData(d);
        setError("");
      }
    } catch (e) {
      if (n === seq.current) setError(errorText(e));
    } finally {
      if (n === seq.current) setLoading(false);
    }
  }, [load]);
  useEffect(() => {
    void reload();
  }, [reload]);
  return { data, error, loading, reload, setData };
}

export function ErrorBox({ text }: { text: string }) {
  return text ? (
    <p className="error" role="alert">
      {text}
    </p>
  ) : null;
}

export function Card({ title, actions, children }: { title?: ReactNode; actions?: ReactNode; children: ReactNode }) {
  return (
    <section className="card">
      {(title || actions) && (
        <div className="row" style={{ marginBottom: 12 }}>
          {title && <h2 style={{ margin: 0 }}>{title}</h2>}
          <span className="spacer" />
          {actions}
        </div>
      )}
      {children}
    </section>
  );
}

export function Tile({ label, value, sub, children }: { label: string; value: ReactNode; sub?: ReactNode; children?: ReactNode }) {
  return (
    <div className="tile">
      <div className="label">{label}</div>
      <div className="value">{value}</div>
      {sub && <div className="sub">{sub}</div>}
      {children}
    </div>
  );
}

/** Status badge: the dot carries the color, the text carries the meaning. */
export function Badge({ kind, children }: { kind?: "good" | "warning" | "critical"; children: ReactNode }) {
  return <span className={"badge " + (kind ?? "")}>{children}</span>;
}

export const fmtInt = (n: number) => n.toLocaleString("en-US");
export const fmtUSD = (n: number) =>
  "$" + n.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: n < 1 && n > 0 ? 4 : 2 });
export const fmtPct = (r: number) => (r * 100).toLocaleString("en-US", { maximumFractionDigits: 1 }) + "%";
export const fmtTime = (s?: string) => (s ? new Date(s).toLocaleString() : "");
export const fmtMS = (ms: number) => (ms >= 10000 ? (ms / 1000).toFixed(0) + " s" : ms >= 1000 ? (ms / 1000).toFixed(1) + " s" : ms + " ms");
