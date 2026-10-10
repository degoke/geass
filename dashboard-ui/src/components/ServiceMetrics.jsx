import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { Muted } from "@/components/geass-ui";

const WINDOWS = [
  { value: "15m", label: "Last 15 min" },
  { value: "1h", label: "Last hour" },
  { value: "6h", label: "Last 6 hours" },
  { value: "24h", label: "Last 24 hours" },
];

function formatBytes(value) {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let size = Math.max(0, value);
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${size >= 100 || unit === 0 ? Math.round(size) : size.toFixed(1)} ${units[unit]}`;
}

function formatCPU(value) {
  return `${value >= 10 ? value.toFixed(0) : value.toFixed(2)} vCPU`;
}

function formatRate(value) {
  return `${formatBytes(value)}/s`;
}

function formatRequests(value) {
  return `${value >= 10 ? value.toFixed(0) : value.toFixed(2)} req/s`;
}

const CHARTS = [
  { key: "cpu", title: "CPU", format: formatCPU },
  { key: "memory", title: "Memory", format: formatBytes },
  { key: "network", title: "Public Network Traffic", format: formatRate },
  { key: "requests", title: "Requests", format: formatRequests },
];

const W = 420;
const H = 150;
const PAD = { left: 4, right: 4, top: 8, bottom: 4 };

function timeLabel(seconds) {
  return new Date(seconds * 1000).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

export function LineChart({ points, format }) {
  const shape = useMemo(() => {
    if (!points?.length) return null;
    const maxValue = Math.max(...points.map((point) => point.v), 0);
    const top = maxValue > 0 ? maxValue * 1.15 : 1;
    const minT = points[0].t;
    const maxT = points[points.length - 1].t;
    const span = Math.max(1, maxT - minT);
    const x = (t) => PAD.left + ((t - minT) / span) * (W - PAD.left - PAD.right);
    const y = (v) => H - PAD.bottom - (v / top) * (H - PAD.top - PAD.bottom);
    const line = points.map((point, index) => `${index ? "L" : "M"}${x(point.t).toFixed(1)},${y(point.v).toFixed(1)}`).join(" ");
    const area = `${line} L${x(maxT).toFixed(1)},${H - PAD.bottom} L${x(minT).toFixed(1)},${H - PAD.bottom} Z`;
    return { line, area, top, minT, maxT, last: points[points.length - 1].v };
  }, [points]);

  if (!shape) {
    return <div className="rw-chart-empty">No data for this time range</div>;
  }
  return (
    <div className="rw-chart">
      <div className="rw-chart-scale">
        <span>{format(shape.top)}</span>
        <span>{format(shape.top / 2)}</span>
        <span>{format(0)}</span>
      </div>
      <div className="rw-chart-plot">
        <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" role="img" aria-label={`Latest ${format(shape.last)}`}>
          {[0.25, 0.5, 0.75].map((fraction) => (
            <line key={fraction} x1="0" x2={W} y1={H * fraction} y2={H * fraction} className="rw-chart-grid" />
          ))}
          <path d={shape.area} className="rw-chart-area" />
          <path d={shape.line} className="rw-chart-line" vectorEffect="non-scaling-stroke" />
        </svg>
        <div className="rw-chart-axis">
          <span>{timeLabel(shape.minT)}</span>
          <span>{timeLabel((shape.minT + shape.maxT) / 2)}</span>
          <span>{timeLabel(shape.maxT)}</span>
        </div>
      </div>
    </div>
  );
}

/**
 * Time-series metrics for a service (scope="apps") or a whole project (scope="projects").
 * Data comes from Prometheus through the dashboard API.
 */
export function ServiceMetrics({ scope, name, environment, charts = CHARTS }) {
  const [range, setRange] = useState("15m");
  const query = useQuery({
    queryKey: ["metrics", scope, name, environment || "", range],
    queryFn: () => api(`/api/${scope}/${name}/metrics?window=${range}${environment ? `&environment=${encodeURIComponent(environment)}` : ""}`),
    refetchInterval: 15000,
  });
  const available = query.data?.available;
  const visible = charts.filter((chart) => chart.key !== "requests" || scope === "apps");

  return (
    <div className="rw-metrics">
      <div className="rw-metrics-bar">
        <select className="rw-rail-select rw-metrics-window" value={range} onChange={(event) => setRange(event.currentTarget.value)} aria-label="Time range">
          {WINDOWS.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
        </select>
      </div>
      {query.isError ? <Muted size="sm">{query.error.message}</Muted> : null}
      {query.data && !available ? (
        <div className="rw-metrics-unavailable">
          <strong>Metrics are not available yet</strong>
          <span>{query.data.message || "Metrics appear once Prometheus is installed and reachable from Geass."}</span>
        </div>
      ) : null}
      <div className="rw-metrics-grid">
        {visible.map((chart) => (
          <section className="rw-metrics-card" key={chart.key}>
            <h3>{chart.title}</h3>
            {query.isPending ? (
              <div className="rw-chart-empty">Loading…</div>
            ) : (
              <LineChart points={query.data?.series?.[chart.key]} format={chart.format} />
            )}
          </section>
        ))}
      </div>
    </div>
  );
}

export { CHARTS as METRIC_CHARTS };
