// Loaded on demand by library.tsx (PRD PERF-001 item 4): a chat that never shows a
// chart never fetches this chunk. Plain SVG, every label and number rendered as
// React text; no library, no URL, no HTML sink.

export interface ChartProps {
  kind: "bar" | "line" | "pie";
  title: string;
  labels: string[];
  values: number[];
}

const W = 320;
const H = 160;
const PAD = 24;

const fmt = (n: number) => String(Math.round(n * 100) / 100);

function Pie({ values }: Pick<ChartProps, "values">) {
  const total = values.reduce((a, b) => a + Math.max(b, 0), 0);
  if (total <= 0) return <p className="ou-text">No data to chart.</p>;
  let from = 0;
  const arcs = values.map((v, i) => {
    const frac = Math.max(v, 0) / total;
    const a0 = from * 2 * Math.PI;
    from += frac;
    const a1 = from * 2 * Math.PI;
    const p = (a: number) => `${80 + 70 * Math.sin(a)} ${80 - 70 * Math.cos(a)}`;
    const d = frac >= 0.9999 ? "M80 10 A70 70 0 1 1 79.99 10 Z" : `M80 80 L${p(a0)} A70 70 0 ${frac > 0.5 ? 1 : 0} 1 ${p(a1)} Z`;
    return <path key={i} d={d} className="ou-slice" />;
  });
  return (
    <svg viewBox="0 0 160 160" className="ou-chart-svg" role="img" aria-label="Pie chart">
      {arcs}
    </svg>
  );
}

function Plot({ kind, values }: Pick<ChartProps, "kind" | "values">) {
  const max = Math.max(...values, 0);
  const min = Math.min(...values, 0);
  const span = max - min || 1;
  const y = (v: number) => H - PAD - ((v - min) / span) * (H - 2 * PAD);
  const step = (W - 2 * PAD) / values.length;
  const x = (i: number) => PAD + step * (i + 0.5);
  return (
    <svg viewBox={`0 0 ${W} ${H}`} className="ou-chart-svg" role="img" aria-label={kind === "bar" ? "Bar chart" : "Line chart"}>
      <line x1={PAD} x2={W - PAD} y1={y(0)} y2={y(0)} className="ou-axis" />
      {kind === "bar" ? (
        values.map((v, i) => <rect key={i} x={x(i) - step * 0.35} width={step * 0.7} y={Math.min(y(v), y(0))} height={Math.abs(y(v) - y(0))} className="ou-bar" />)
      ) : (
        <>
          <polyline points={values.map((v, i) => `${x(i)},${y(v)}`).join(" ")} className="ou-line" fill="none" />
          {values.map((v, i) => (
            <circle key={i} cx={x(i)} cy={y(v)} r={3} className="ou-dot" />
          ))}
        </>
      )}
    </svg>
  );
}

export default function Chart({ kind, title, labels, values }: ChartProps) {
  return (
    <figure className="ou-chart" data-ou-chart={kind}>
      <figcaption className="ou-heading">{title}</figcaption>
      {kind === "pie" ? <Pie values={values} /> : <Plot kind={kind} values={values} />}
      {/* The numbers are always available as text, not only as shapes. */}
      <table className="ou-table ou-chart-data">
        <tbody>
          {values.map((v, i) => (
            <tr key={i}>
              <th scope="row">
                {kind === "pie" && <span className="ou-swatch" data-slice={i % 6} aria-hidden="true" />}
                {labels[i] ?? ""}
              </th>
              <td>{fmt(v)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </figure>
  );
}
