import { useState, type ButtonHTMLAttributes, type InputHTMLAttributes, type ReactNode } from "react";
import type { State } from "../api";

export function Card({ title, actions, children }: { title?: ReactNode; actions?: ReactNode; children: ReactNode }) {
  return (
    <section className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-800 dark:bg-slate-900">
      {(title || actions) && (
        <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
          {title && <h2 className="text-lg font-semibold">{title}</h2>}
          {actions && <div className="flex flex-wrap gap-2">{actions}</div>}
        </div>
      )}
      {children}
    </section>
  );
}

type Variant = "primary" | "secondary" | "danger";
const variants: Record<Variant, string> = {
  primary: "bg-indigo-600 text-white hover:bg-indigo-500",
  secondary:
    "border border-slate-300 bg-white text-slate-800 hover:bg-slate-100 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-100 dark:hover:bg-slate-700",
  danger: "bg-rose-600 text-white hover:bg-rose-500",
};

export function Button({ variant = "primary", className = "", ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant }) {
  return (
    <button
      {...props}
      className={`inline-flex items-center justify-center rounded-lg px-3.5 py-2 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-50 ${variants[variant]} ${className}`}
    />
  );
}

export function Field({ label, hint, children }: { label: string; hint?: ReactNode; children: ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1 block text-sm font-medium">{label}</span>
      {children}
      {hint && <span className="mt-1 block text-xs text-slate-500 dark:text-slate-400">{hint}</span>}
    </label>
  );
}

const inputCls =
  "w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm outline-none focus:border-indigo-500 focus:ring-2 focus:ring-indigo-500/30 dark:border-slate-700 dark:bg-slate-950";

export function Input(props: InputHTMLAttributes<HTMLInputElement>) {
  return <input {...props} className={`${inputCls} ${props.className ?? ""}`} />;
}

export function TextArea(props: React.TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea {...props} className={`${inputCls} font-mono ${props.className ?? ""}`} />;
}

const stateStyle: Record<string, string> = {
  running: "bg-emerald-100 text-emerald-800 dark:bg-emerald-900/50 dark:text-emerald-300",
  stopped: "bg-slate-200 text-slate-700 dark:bg-slate-800 dark:text-slate-300",
  missing: "bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-300",
  provisioning: "bg-sky-100 text-sky-800 dark:bg-sky-900/50 dark:text-sky-300",
  building: "bg-sky-100 text-sky-800 dark:bg-sky-900/50 dark:text-sky-300",
  error: "bg-rose-100 text-rose-800 dark:bg-rose-900/50 dark:text-rose-300",
  failed: "bg-rose-100 text-rose-800 dark:bg-rose-900/50 dark:text-rose-300",
};

export function StateBadge({ state }: { state: State | string }) {
  const label = state === "missing" ? "not deployed" : state;
  return (
    <span className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium ${stateStyle[state] ?? stateStyle.stopped}`}>
      <span className="h-1.5 w-1.5 rounded-full bg-current" />
      {label}
    </span>
  );
}

export function ErrorBox({ error }: { error: unknown }) {
  if (!error) return null;
  return (
    <div className="rounded-lg border border-rose-300 bg-rose-50 px-3 py-2 text-sm text-rose-800 dark:border-rose-800 dark:bg-rose-950/50 dark:text-rose-300">
      {error instanceof Error ? error.message : String(error)}
    </div>
  );
}

export function LogView({ text, empty = "No output yet." }: { text?: string; empty?: string }) {
  return (
    <pre className="max-h-96 overflow-auto rounded-lg bg-slate-950 p-3 text-xs leading-relaxed text-slate-200 whitespace-pre-wrap break-all">
      {text?.trim() ? text : empty}
    </pre>
  );
}

export function CopyField({ value }: { value: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="flex items-stretch gap-2">
      <code className="flex-1 overflow-x-auto rounded-lg bg-slate-100 px-3 py-2 text-sm dark:bg-slate-800">{value}</code>
      <Button
        variant="secondary"
        onClick={() => {
          navigator.clipboard?.writeText(value).then(() => {
            setCopied(true);
            setTimeout(() => setCopied(false), 1500);
          });
        }}
      >
        {copied ? "Copied" : "Copy"}
      </Button>
    </div>
  );
}

export function formatLimits(cpus: number, memoryMb: number) {
  const c = cpus > 0 ? `${cpus} CPU` : "unlimited CPU";
  const m = memoryMb > 0 ? (memoryMb >= 1024 ? `${(memoryMb / 1024).toFixed(1)} GB` : `${memoryMb} MB`) : "unlimited RAM";
  return `${c} · ${m}`;
}
