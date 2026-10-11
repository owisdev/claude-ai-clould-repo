import { useState } from "react";

export interface Series {
  name: string;
  color: string; // CSS color, e.g. var(--series-1)
  values: number[];
}

interface Props {
  labels: string[]; // one per column (e.g. days)
  series: Series[]; // stacked bottom to top
  format: (v: number) => string;
  height?: number;
  label: string; // accessible name
}

const W = 800;
const PAD = { top: 10, right: 8, bottom: 24, left: 52 };

/** Niceish axis maximum and 4 ticks. */
function ticks(max: number): number[] {
  if (max <= 0) return [0];
  const raw = max / 4;
  const p = Math.pow(10, Math.floor(Math.log10(raw)));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * p).find((s) => s >= raw) ?? raw;
  return [0, 1, 2, 3, 4].map((i) => i * step);
}

/**
 * Stacked bar chart with one column per label. Hover or focus on a column
 * shows every series' value for it. A table view is offered next to it.
 */
export function BarChart({ labels, series, format, height = 220, label }: Props) {
  const [hover, setHover] = useState<number | null>(null);
  const [table, setTable] = useState(false);
  const totals = labels.map((_, i) => series.reduce((s, x) => s + (x.values[i] ?? 0), 0));
  const yt = ticks(Math.max(...totals, 0));
  const ymax = yt[yt.length - 1] || 1;
  const plotW = W - PAD.left - PAD.right;
  const plotH = height - PAD.top - PAD.bottom;
  const slot = plotW / Math.max(labels.length, 1);
  const barW = Math.max(Math.min(slot - 4, 28), 2);
  const y = (v: number) => PAD.top + plotH - (v / ymax) * plotH;
  const every = Math.ceil(labels.length / 10);

  return (
    <div className="chart">
      <div className="row">
        {series.length > 1 && (
          <div className="legend" aria-hidden="true">
            {series.map((s) => (
              <span key={s.name} style={{ ["--c" as string]: s.color }}>
                {s.name}
              </span>
            ))}
          </div>
        )}
        <span className="spacer" />
        <button className="small" onClick={() => setTable(!table)}>
          {table ? "Chart" : "Table"}
        </button>
      </div>
      {totals.every((t) => t === 0) && !table ? (
        <p className="muted" style={{ padding: "40px 0", textAlign: "center" }}>
          No data in this period yet.
        </p>
      ) : table ? (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Day</th>
                {series.map((s) => (
                  <th key={s.name} className="num">
                    {s.name}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {labels.map((l, i) => (
                <tr key={l}>
                  <td>{l}</td>
                  {series.map((s) => (
                    <td key={s.name} className="num">
                      {format(s.values[i] ?? 0)}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <svg viewBox={`0 0 ${W} ${height}`} role="img" aria-label={label} onPointerLeave={() => setHover(null)}>
          {yt.map((t) => (
            <g key={t}>
              <line className="gridline" x1={PAD.left} x2={W - PAD.right} y1={y(t)} y2={y(t)} />
              <text className="axis-label" x={PAD.left - 8} y={y(t) + 4} textAnchor="end">
                {format(t)}
              </text>
            </g>
          ))}
          {labels.map((l, i) => {
            const cx = PAD.left + slot * i + slot / 2;
            let base = 0;
            const segs = series
              .map((s) => {
                const v = s.values[i] ?? 0;
                const seg = { s, v, from: base, to: base + v };
                base += v;
                return seg;
              })
              .filter((g) => g.v > 0);
            return (
              <g
                key={l}
                className="bar"
                tabIndex={0}
                aria-label={`${l}: ${series.map((s) => `${s.name} ${format(s.values[i] ?? 0)}`).join(", ")}`}
                onPointerEnter={() => setHover(i)}
                onFocus={() => setHover(i)}
                onBlur={() => setHover(null)}
              >
                {/* Hit target: the whole column, bigger than the bar. */}
                <rect x={cx - slot / 2} y={PAD.top} width={slot} height={plotH} fill="transparent" />
                {segs.map((g, k) => {
                  const top = k === segs.length - 1;
                  const y0 = y(g.from);
                  const y1 = y(g.to) + (top ? 0 : 1); // 2px surface gap between segments
                  const h = Math.max(y0 - y1 - (k > 0 ? 1 : 0), 1);
                  const yy = y0 - (k > 0 ? 1 : 0) - h;
                  const r = top ? Math.min(4, barW / 2, h) : 0;
                  const x0 = cx - barW / 2;
                  const d = `M${x0},${yy + h} V${yy + r} Q${x0},${yy} ${x0 + r},${yy} H${x0 + barW - r} Q${x0 + barW},${yy} ${x0 + barW},${yy + r} V${yy + h} Z`;
                  return <path key={g.s.name} d={d} fill={g.s.color} />;
                })}
                {i % every === 0 && (
                  <text className="axis-label" x={cx} y={height - 6} textAnchor="middle">
                    {l.slice(5)}
                  </text>
                )}
              </g>
            );
          })}
        </svg>
      )}
      {!table && hover !== null && totals.some((t) => t > 0) && (
        <div
          className="tooltip"
          style={{
            left: `min(calc(${((PAD.left + slot * hover + slot / 2) / W) * 100}% + 12px), calc(100% - 160px))`,
            top: 44,
          }}
        >
          <div className="t-head">{labels[hover]}</div>
          {[...series].reverse().map((s) => (
            <div className="t-row" key={s.name}>
              <span className="t-key" style={{ ["--c" as string]: s.color }} />
              <b>{format(s.values[hover] ?? 0)}</b>
              <span className="muted">{s.name}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
