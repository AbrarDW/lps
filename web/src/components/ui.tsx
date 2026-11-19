import { useState, type ButtonHTMLAttributes, type InputHTMLAttributes, type ReactNode } from "react";
import type { State } from "../api";

/* ---------- Layout ---------- */

/** Cyan registration marks on two corners of a primary panel (blueprint motif). */
export function CornerMarks() {
  return (
    <>
      <span aria-hidden="true" className="pointer-events-none absolute -top-px -left-px size-2.5 border-t-2 border-l-2 border-cyan" />
      <span aria-hidden="true" className="pointer-events-none absolute -right-px -bottom-px size-2.5 border-r-2 border-b-2 border-cyan" />
    </>
  );
}

/**
 * Panel with a header strip (Blueprint.js card). `title` is shown as a mono
 * label; `marks` adds corner registration marks; `tone="danger"` for the
 * danger zone.
 */
export function Card({
  title,
  actions,
  children,
  marks,
  tone,
  flush,
  className = "",
}: {
  title?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  marks?: boolean;
  tone?: "danger";
  /** No body padding (tables, lists, tabs). */
  flush?: boolean;
  className?: string;
}) {
  const danger = tone === "danger";
  return (
    <section className={`relative min-w-0 rounded-bp border bg-panel ${danger ? "border-danger/55" : "border-line"} ${className}`}>
      {marks && <CornerMarks />}
      {(title || actions) && (
        <div
          className={`flex min-h-11 flex-wrap items-center justify-between gap-2 border-b px-4 py-1.5 ${
            danger ? "border-danger/40 bg-danger/12" : "border-line bg-panel-head"
          }`}
        >
          {title && <h2 className={`label-mono m-0 font-medium ${danger ? "text-danger-ink" : "text-ink"}`}>{title}</h2>}
          {actions && <div className="flex flex-wrap items-center gap-1.5">{actions}</div>}
        </div>
      )}
      <div className={flush ? "" : "p-4"}>{children}</div>
    </section>
  );
}

/** Page title block: mono sheet label, title, and a dimension line. */
export function PageHeader({
  sheet,
  title,
  subtitle,
  actions,
  children,
}: {
  sheet?: string;
  title: ReactNode;
  subtitle?: ReactNode;
  actions?: ReactNode;
  children?: ReactNode;
}) {
  return (
    <section className="flex flex-wrap items-end justify-between gap-4">
      <div className="flex min-w-0 flex-col gap-1.5">
        {sheet && <span className="label-mono text-cyan">{sheet}</span>}
        <div className="flex flex-wrap items-center gap-3">
          <h1 className="m-0 text-[28px] font-semibold tracking-tight">{title}</h1>
          {children}
        </div>
        {subtitle && <div className="max-w-2xl text-ink-muted">{subtitle}</div>}
        <DimensionLine className="w-56" />
      </div>
      {actions && <div className="flex flex-wrap gap-2">{actions}</div>}
    </section>
  );
}

/** A technical-drawing dimension line: a rule with end ticks. */
export function DimensionLine({ className = "", label }: { className?: string; label?: string }) {
  return (
    <span className={`flex items-center gap-2 font-mono text-[10px] text-ink-faint ${className}`} aria-hidden="true">
      <span
        className="h-[7px] flex-1 border-x border-line-strong"
        style={{ background: "linear-gradient(var(--color-line-strong), var(--color-line-strong)) center / 100% 1px no-repeat" }}
      />
      {label && <span>{label}</span>}
    </span>
  );
}

/** Drawing-sheet title block used as a page footer. */
export function TitleBlock({ cells }: { cells: string[] }) {
  return (
    <footer className="flex self-end border border-line-strong font-mono text-[11px] text-ink-muted">
      {cells.map((c, i) => (
        <span key={c} className={`px-3 py-1.5 ${i < cells.length - 1 ? "border-r border-line-strong" : ""}`}>
          {c}
        </span>
      ))}
    </footer>
  );
}

/** Tab strip for a panel header (Blueprint.js tabs). */
export function Tabs<T extends string>({
  tabs,
  value,
  onChange,
  label,
  right,
}: {
  tabs: { id: T; label: string }[];
  value: T;
  onChange: (id: T) => void;
  label: string;
  right?: ReactNode;
}) {
  return (
    <div className="flex flex-wrap items-stretch justify-between gap-2 border-b border-line bg-panel-head pr-3 pl-1">
      <div role="tablist" aria-label={label} className="flex flex-wrap">
        {tabs.map((t) => {
          const on = t.id === value;
          return (
            <button
              key={t.id}
              type="button"
              role="tab"
              aria-selected={on}
              onClick={() => onChange(t.id)}
              className={`label-mono min-h-11 cursor-pointer border-0 border-b-2 bg-transparent px-3.5 ${
                on ? "border-cyan text-white" : "border-transparent text-ink-muted hover:text-ink"
              }`}
            >
              {t.label}
            </button>
          );
        })}
      </div>
      {right && <div className="flex flex-wrap items-center gap-1.5 py-1.5">{right}</div>}
    </div>
  );
}

/* ---------- Controls ---------- */

type Variant = "primary" | "secondary" | "minimal" | "danger";
const variants: Record<Variant, string> = {
  primary: "bg-primary border-primary-line text-white hover:bg-[#3a80e0]",
  secondary: "bg-white/5 border-line-strong text-ink hover:bg-white/10",
  minimal: "bg-transparent border-transparent text-cyan-soft hover:bg-white/5",
  danger: "bg-danger border-[#e76a6e] text-white hover:bg-[#db5458]",
};

export function Button({
  variant = "primary",
  size = "md",
  className = "",
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant; size?: "sm" | "md" }) {
  return (
    <button
      type="button"
      {...props}
      className={`inline-flex cursor-pointer items-center justify-center gap-2 rounded-bp border font-medium transition-colors disabled:cursor-not-allowed disabled:border-[#2f5f92] disabled:bg-[#1e4b7c] disabled:text-ink-faint ${
        size === "sm" ? "h-[30px] px-2.5 text-[13px]" : "h-9 px-3.5"
      } ${variants[variant]} ${className}`}
    />
  );
}

/** Same look as Button for router links (`<Link className={buttonClass()}>`). */
export function buttonClass(variant: Variant = "primary", size: "sm" | "md" = "md") {
  return `inline-flex items-center justify-center gap-2 rounded-bp border font-medium no-underline transition-colors ${
    size === "sm" ? "h-[30px] px-2.5 text-[13px]" : "h-9 px-3.5"
  } ${variants[variant]} hover:text-white`;
}

export function Field({ label, hint, children }: { label: string; hint?: ReactNode; children: ReactNode }) {
  return (
    <label className="flex flex-col gap-1.5">
      <span className="text-[13px] font-medium">{label}</span>
      {children}
      {hint && <span className="text-xs text-ink-muted">{hint}</span>}
    </label>
  );
}

const inputCls =
  "w-full rounded-bp border border-[#2f5f92] bg-ground-deep px-2.5 text-ink outline-none placeholder:text-ink-faint focus:border-cyan focus:ring-2 focus:ring-cyan/25";

export function Input(props: InputHTMLAttributes<HTMLInputElement>) {
  return <input {...props} className={`${inputCls} h-[34px] ${props.className ?? ""}`} />;
}

export function TextArea(props: React.TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea {...props} className={`${inputCls} py-2 font-mono text-[13px] leading-relaxed ${props.className ?? ""}`} />;
}

export function Select(props: React.SelectHTMLAttributes<HTMLSelectElement>) {
  return <select {...props} className={`${inputCls} h-[34px] ${props.className ?? ""}`} />;
}

export function Switch({
  checked,
  onChange,
  disabled,
  label,
}: {
  checked: boolean;
  onChange: (v: boolean) => void;
  disabled?: boolean;
  label: string;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={`relative inline-flex h-5 w-9 shrink-0 cursor-pointer items-center rounded-full border transition disabled:cursor-not-allowed disabled:opacity-50 ${
        checked ? "border-primary-line bg-primary" : "border-line-strong bg-ground-deep"
      }`}
    >
      <span className={`inline-block size-4 rounded-full bg-white shadow transition ${checked ? "translate-x-[17px]" : "translate-x-[1px]"}`} />
    </button>
  );
}

/* ---------- Status ---------- */

type Intent = "success" | "neutral" | "primary" | "warning" | "danger";
const intents: Record<Intent, { box: string; dot: string }> = {
  success: { box: "bg-success/18 text-success-ink", dot: "bg-[#72ca9b]" },
  neutral: { box: "bg-ink-faint/16 text-ink-2", dot: "bg-ink-faint" },
  primary: { box: "bg-primary/22 text-cyan-soft", dot: "bg-cyan" },
  warning: { box: "bg-warning/18 text-warning-ink", dot: "bg-warning-ink" },
  danger: { box: "bg-danger/20 text-danger-ink", dot: "bg-[#fa999c]" },
};

/** Blueprint.js-style tag: small, square, mono, intent colour. */
export function Tag({ intent = "neutral", dot, children }: { intent?: Intent; dot?: boolean; children: ReactNode }) {
  const c = intents[intent];
  return (
    <span className={`inline-flex h-5 items-center gap-1.5 rounded-[2px] px-[7px] font-mono text-[11px] whitespace-nowrap uppercase ${c.box}`}>
      {dot && <span className={`size-1.5 rounded-full ${c.dot}`} />}
      {children}
    </span>
  );
}

const stateIntent: Record<string, Intent> = {
  running: "success",
  active: "success",
  stopped: "neutral",
  missing: "neutral",
  provisioning: "primary",
  building: "primary",
  pending: "primary",
  error: "danger",
  failed: "danger",
};

export function StateBadge({ state }: { state: State | string }) {
  const label = state === "missing" ? "not deployed" : state;
  return (
    <Tag intent={stateIntent[state] ?? "neutral"} dot>
      {label}
    </Tag>
  );
}

/** Callout box (Blueprint.js callout). */
export function Callout({ intent = "primary", children }: { intent?: "primary" | "warning" | "danger"; children: ReactNode }) {
  const cls = {
    primary: "border-cyan/45 bg-primary/12 text-[#cfe9ff]",
    warning: "border-warning-ink/45 bg-warning/10 text-[#fcd9ae]",
    danger: "border-[#fa999c]/45 bg-danger/12 text-[#fdd3d4]",
  }[intent];
  return <div className={`rounded-[2px] border px-3.5 py-3 text-[13px] ${cls}`}>{children}</div>;
}

export function ErrorBox({ error }: { error: unknown }) {
  if (!error) return null;
  return <Callout intent="danger">{error instanceof Error ? error.message : String(error)}</Callout>;
}

export function ActionBox({ message, url, linkText = "Open" }: { message: string; url?: string; linkText?: string }) {
  return (
    <Callout intent="warning">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <span>{message}</span>
        {url && (
          <a href={url} target="_blank" rel="noreferrer" className="font-medium text-warning-ink underline hover:text-white">
            {linkText} ↗
          </a>
        )}
      </div>
    </Callout>
  );
}

/* ---------- Data display ---------- */

export function LogView({ text, empty = "No output yet.", className = "" }: { text?: string; empty?: string; className?: string }) {
  return (
    <pre
      className={`m-0 max-h-96 overflow-auto rounded-[2px] border border-line-soft bg-term p-3 font-mono text-xs leading-relaxed break-all whitespace-pre-wrap text-[#d7e3ef] ${className}`}
    >
      {text?.trim() ? text : empty}
    </pre>
  );
}

export function CopyField({ value, label = "Copy" }: { value: string; label?: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="flex min-w-0">
      <code className="flex h-[34px] min-w-0 flex-1 items-center overflow-x-auto rounded-l-bp border border-r-0 border-[#2f5f92] bg-ground-deep px-2.5 font-mono text-xs whitespace-nowrap">
        {value}
      </code>
      <button
        type="button"
        aria-label={`${label}: ${value}`}
        onClick={() => {
          navigator.clipboard?.writeText(value).then(() => {
            setCopied(true);
            setTimeout(() => setCopied(false), 1500);
          });
        }}
        className="h-[34px] shrink-0 cursor-pointer rounded-r-bp border border-[#2f5f92] bg-panel-head px-3 text-[13px] text-ink hover:bg-[#184371]"
      >
        {copied ? "Copied" : "Copy"}
      </button>
    </div>
  );
}

/** Label/value grid used for specifications. */
export function SpecGrid({ items }: { items: { label: string; value: ReactNode }[] }) {
  return (
    <dl className="m-0 grid grid-cols-2 text-[13px]">
      {items.map((it, i) => (
        <div
          key={it.label}
          className={`border-line-soft px-4 py-2.5 ${i % 2 === 0 ? "border-r" : ""} ${i < items.length - (items.length % 2 === 0 ? 2 : 1) ? "border-b" : ""}`}
        >
          <dt className="font-mono text-[10px] tracking-[0.1em] text-ink-faint uppercase">{it.label}</dt>
          <dd className="m-0 mt-0.5">{it.value}</dd>
        </div>
      ))}
    </dl>
  );
}

export function formatLimits(cpus: number, memoryMb: number) {
  const c = cpus > 0 ? `${cpus} CPU` : "unlimited CPU";
  const m = memoryMb > 0 ? (memoryMb >= 1024 ? `${(memoryMb / 1024).toFixed(1)} GB` : `${memoryMb} MB`) : "unlimited RAM";
  return `${c} · ${m}`;
}

/* ---------- Icons (inline stroke SVG) ---------- */

const icons = {
  plus: "M8 3v10M3 8h10",
  terminal: "M3 5l3 3-3 3M8 11h5",
  stop: "",
  play: "",
  restart: "M13 8a5 5 0 1 1-1.5-3.5M13 3v3h-3",
  deploy: "M8 2v8M4.5 6.5L8 10l3.5-3.5M3 13h10",
  fullscreen: "M2 6V2h4M14 6V2h-4M2 10v4h4M14 10v4h-4",
  chevron: "M4 6l4 4 4-4",
  search: "",
  app: "M2 3h12v10H2zM5 7l2 2-2 2M9 11h3",
} as const;

export function Icon({ name, size = 14 }: { name: keyof typeof icons; size?: number }) {
  if (name === "stop")
    return (
      <svg aria-hidden="true" width={size - 2} height={size - 2} viewBox="0 0 16 16" fill="currentColor">
        <rect x="3" y="3" width="10" height="10" rx="1" />
      </svg>
    );
  if (name === "play")
    return (
      <svg aria-hidden="true" width={size - 2} height={size - 2} viewBox="0 0 16 16" fill="currentColor">
        <path d="M5 3l8 5-8 5z" />
      </svg>
    );
  if (name === "search")
    return (
      <svg aria-hidden="true" width={size} height={size} viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6">
        <circle cx="7" cy="7" r="4.5" />
        <path d="M10.5 10.5L14 14" />
      </svg>
    );
  return (
    <svg aria-hidden="true" width={size} height={size} viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.6">
      <path d={icons[name]} />
    </svg>
  );
}
