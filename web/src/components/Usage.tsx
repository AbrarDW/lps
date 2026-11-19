import { useId, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, type StatPoint, type UsageStat } from "../api";
import { Card } from "./ui";

/** Live usage of every running VPS and app, refreshed every few seconds. */
export function useStats() {
  return useQuery({ queryKey: ["stats"], queryFn: api.stats, refetchInterval: 3000 });
}

export function formatBytes(n: number) {
  if (n >= 1 << 30) return `${(n / (1 << 30)).toFixed(n >= 10 * (1 << 30) ? 0 : 1)} GB`;
  if (n >= 1 << 20) return `${Math.round(n / (1 << 20))} MB`;
  if (n >= 1 << 10) return `${Math.round(n / (1 << 10))} KB`;
  return `${n} B`;
}

/** CPU as a share of what the VPS/app may use (its core limit), 0–100. */
export function cpuShare(u: UsageStat) {
  return u.cpuLimit > 0 ? u.cpu / u.cpuLimit : u.cpu;
}

const pct = (n: number) => (n >= 10 || n === 0 ? `${Math.round(n)}%` : `${n.toFixed(1)}%`);
const cores = (n: number) => `${n} ${n === 1 ? "core" : "cores"}`;
const WINDOW = 5 * 60 * 1000;

/** Rounds a chart's top up to a readable value. */
function niceMax(v: number, floor: number) {
  const m = Math.max(v * 1.2, floor);
  const p = 10 ** Math.floor(Math.log10(m));
  return Math.ceil(m / p / 2) * p * 2;
}

/**
 * A single-series sparkline over the last 5 minutes (fixed time window,
 * baseline at zero) with a crosshair and tooltip on hover.
 */
export function Sparkline({
  points,
  value,
  format,
  label,
  floor,
}: {
  points: StatPoint[];
  value: (p: StatPoint) => number;
  format: (n: number) => string;
  label: string;
  /** Smallest top of the y-scale, so idle noise stays flat. */
  floor: number;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [hover, setHover] = useState<number | null>(null);
  const clip = useId();
  const W = 300;
  const H = 48;
  const end = points.length ? points[points.length - 1].t : Date.now();
  const start = end - WINDOW;
  const vals = points.map(value);
  const top = niceMax(Math.max(0, ...vals), floor);
  const x = (t: number) => ((t - start) / WINDOW) * W;
  const y = (v: number) => H - 2 - (Math.min(v, top) / top) * (H - 4);
  const line = points.map((p, i) => `${i ? "L" : "M"}${x(p.t).toFixed(1)},${y(vals[i]).toFixed(1)}`).join("");
  const area = points.length > 1 ? `${line}L${x(end).toFixed(1)},${H}L${x(points[0].t).toFixed(1)},${H}Z` : "";
  const h = hover != null ? points[hover] : null;
  const summary = vals.length
    ? `${label} over the last 5 minutes: low ${format(Math.min(...vals))}, high ${format(Math.max(...vals))}, now ${format(vals[vals.length - 1])}.`
    : `${label}: collecting data.`;

  function onMove(e: React.PointerEvent) {
    const box = ref.current?.getBoundingClientRect();
    if (!box || !points.length) return;
    const t = start + ((e.clientX - box.left) / box.width) * WINDOW;
    let best = 0;
    for (let i = 1; i < points.length; i++) if (Math.abs(points[i].t - t) < Math.abs(points[best].t - t)) best = i;
    setHover(best);
  }

  return (
    <div ref={ref} className="relative h-12 touch-none" onPointerMove={onMove} onPointerLeave={() => setHover(null)} role="img" aria-label={summary}>
      <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" className="block h-full w-full overflow-visible" aria-hidden="true">
        <defs>
          <clipPath id={clip}>
            <rect width={W} height={H} />
          </clipPath>
        </defs>
        {/* recessive baseline */}
        <line x1={0} x2={W} y1={H - 0.5} y2={H - 0.5} stroke="var(--color-line)" vectorEffect="non-scaling-stroke" />
        <g clipPath={`url(#${clip})`}>
          {area && <path d={area} fill="var(--color-primary-line)" opacity={0.14} />}
          {points.length > 1 && (
            <path d={line} fill="none" stroke="var(--color-primary-line)" strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" vectorEffect="non-scaling-stroke" />
          )}
        </g>
        {h && <line x1={x(h.t)} x2={x(h.t)} y1={0} y2={H} stroke="var(--color-ink-faint)" strokeDasharray="2 2" vectorEffect="non-scaling-stroke" />}
      </svg>
      {h && (
        <>
          <span
            className="pointer-events-none absolute size-2.5 -translate-x-1/2 -translate-y-1/2 rounded-full border-2 border-panel bg-primary-line"
            style={{ left: `${(x(h.t) / W) * 100}%`, top: `${(y(value(h)) / H) * 100}%` }}
          />
          <span
            className={`pointer-events-none absolute -top-8 z-10 rounded-[2px] border border-line-strong bg-ground-deep px-2 py-0.5 font-mono text-[11px] whitespace-nowrap text-ink ${
              x(h.t) > W * 0.6 ? "-translate-x-full" : ""
            }`}
            style={{ left: `${(x(h.t) / W) * 100}%` }}
          >
            {new Date(h.t).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })} · {format(value(h))}
          </span>
        </>
      )}
    </div>
  );
}

/** Thin meter: memory used against its limit. */
function Meter({ used, limit }: { used: number; limit: number }) {
  const share = limit > 0 ? Math.min(1, used / limit) : 0;
  const fill = share > 0.95 ? "bg-danger" : share > 0.8 ? "bg-warning" : "bg-primary-line";
  return (
    <div className="h-1.5 w-full overflow-hidden rounded-full bg-primary-line/20" role="meter" aria-valuemin={0} aria-valuemax={limit} aria-valuenow={used} aria-label="Memory used of limit">
      <div className={`h-full rounded-full ${fill}`} style={{ width: `${Math.max(share * 100, used > 0 ? 1 : 0)}%` }} />
    </div>
  );
}

function Tile({ label, children, foot, chart }: { label: string; children: React.ReactNode; foot: React.ReactNode; chart: React.ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1.5 px-4 py-3">
      <span className="label-mono text-[10px] text-ink-faint">{label}</span>
      <div className="flex items-baseline gap-1.5 text-[22px] leading-none font-semibold">{children}</div>
      <div className="text-xs text-ink-muted">{foot}</div>
      <div className="pt-6">{chart}</div>
    </div>
  );
}

/** CPU and memory tiles with 5-minute sparklines. */
export function UsageTiles({ u }: { u: UsageStat }) {
  const share = cpuShare(u);
  const memShare = u.memLimit > 0 ? (u.mem / u.memLimit) * 100 : 0;
  return (
    <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] divide-line max-sm:divide-y sm:divide-x">
      <Tile
        label="CPU"
        foot={u.cpuLimit > 0 ? `${(u.cpu / 100).toFixed(2)} of ${cores(u.cpuLimit)} in use` : `${(u.cpu / 100).toFixed(2)} cores in use`}
        chart={<Sparkline points={u.points} value={(p) => (u.cpuLimit > 0 ? p.cpu / u.cpuLimit : p.cpu)} format={pct} label="CPU" floor={5} />}
      >
        {pct(share)}
        <span className="text-sm font-normal text-ink-muted">{u.cpuLimit > 0 ? `of ${cores(u.cpuLimit)}` : ""}</span>
      </Tile>
      <Tile
        label="Memory"
        foot={
          <span className="flex flex-col gap-1.5">
            <Meter used={u.mem} limit={u.memLimit} />
            {pct(memShare)} of the limit
          </span>
        }
        chart={<Sparkline points={u.points} value={(p) => p.mem} format={formatBytes} label="Memory" floor={64 << 20} />}
      >
        {formatBytes(u.mem)}
        <span className="text-sm font-normal text-ink-muted">of {formatBytes(u.memLimit)}</span>
      </Tile>
    </div>
  );
}

/** "Usage" card for a VPS or app detail page. */
export function UsageCard({ kind, id, running, note }: { kind: "vps" | "apps"; id: number; running: boolean; note?: React.ReactNode }) {
  const stats = useStats();
  const u = stats.data?.[kind][String(id)];
  return (
    <Card title="Usage · last 5 minutes" flush>
      {u ? (
        <UsageTiles u={u} />
      ) : (
        <p className="m-0 p-4 text-[13px] text-ink-muted">{running ? "Collecting the first samples…" : "Not running, so it uses no CPU or memory."}</p>
      )}
      {note && u && <p className="m-0 border-t border-line-soft px-4 py-2 text-xs text-ink-faint">{note}</p>}
    </Card>
  );
}

/** Compact "12% · 312 MB" for list rows. */
export function UsageInline({ u }: { u?: UsageStat }) {
  if (!u) return <span className="text-ink-faint">—</span>;
  return (
    <span className="font-mono text-xs whitespace-nowrap text-ink-2" title={`CPU ${pct(cpuShare(u))} of ${cores(u.cpuLimit)} · memory ${formatBytes(u.mem)} of ${formatBytes(u.memLimit)}`}>
      {pct(cpuShare(u))} · {formatBytes(u.mem)}
    </span>
  );
}
