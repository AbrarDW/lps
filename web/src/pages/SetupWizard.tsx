import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type SetupCheck } from "../api";
import { Button, Card, CopyField, ErrorBox, Switch } from "../components/ui";
import { CredentialsForm } from "./Auth";

const STEPS = ["Check this PC", "Admin account", "Remote access", "Finish"];

// The wizard keeps going after the admin is created (which ends "needs setup"),
// so App asks this flag whether to keep showing it.
const FLAG = "lps.setupWizard";
export function wizardActive(): boolean {
  try {
    return sessionStorage.getItem(FLAG) === "1";
  } catch {
    return false;
  }
}
function setWizardActive(on: boolean) {
  try {
    if (on) sessionStorage.setItem(FLAG, "1");
    else sessionStorage.removeItem(FLAG);
  } catch {
    /* storage unavailable: the wizard simply ends after the admin step */
  }
}

export function SetupWizard() {
  const [step, setStep] = useState(() => (wizardActive() ? Number(safeGet("lps.setupStep") ?? 0) : 0));
  const go = (n: number) => {
    setWizardActive(true);
    safeSet("lps.setupStep", String(n));
    setStep(n);
  };

  return (
    <div className="min-h-screen px-4 py-8">
      <div className="mx-auto max-w-2xl space-y-6">
        <div className="text-center">
          <div className="mx-auto mb-3 grid h-12 w-12 place-items-center rounded-xl bg-indigo-600 font-bold text-white">LPS</div>
          <h1 className="text-2xl font-semibold">Set up LPS</h1>
          <p className="text-sm text-slate-500 dark:text-slate-400">Turn this PC into your own server in a few steps.</p>
        </div>
        <ol className="grid grid-cols-4 gap-2 text-xs sm:text-sm">
          {STEPS.map((label, i) => (
            <li key={label} className="flex flex-col items-center gap-1 text-center">
              <span
                className={`grid h-8 w-8 place-items-center rounded-full font-semibold ${
                  i < step ? "bg-emerald-600 text-white" : i === step ? "bg-indigo-600 text-white" : "bg-slate-200 text-slate-500 dark:bg-slate-800"
                }`}
              >
                {i < step ? "✓" : i + 1}
              </span>
              <span className={i === step ? "font-medium" : "text-slate-500 dark:text-slate-400"}>{label}</span>
            </li>
          ))}
        </ol>
        {step === 0 && <ChecksStep onNext={() => go(1)} />}
        {step === 1 && <AdminStep onNext={() => go(2)} />}
        {step === 2 && <RemoteStep onNext={() => go(3)} />}
        {step === 3 && <FinishStep />}
      </div>
    </div>
  );
}

function safeGet(k: string) {
  try {
    return sessionStorage.getItem(k);
  } catch {
    return null;
  }
}
function safeSet(k: string, v: string) {
  try {
    sessionStorage.setItem(k, v);
  } catch {
    /* ignore */
  }
}

const icon = { ok: "✅", warn: "⚠️", fail: "❌" } as const;

function CheckRow({ c }: { c: SetupCheck }) {
  const qc = useQueryClient();
  const action = useMutation({
    mutationFn: api.setupAction,
    onSuccess: () => setTimeout(() => qc.invalidateQueries({ queryKey: ["setupChecks"] }), 3000),
  });
  return (
    <li className="space-y-2 py-3">
      <div className="flex items-start gap-3">
        <span aria-label={c.status} className="text-lg leading-6">
          {icon[c.status]}
        </span>
        <div className="min-w-0 flex-1">
          <div className="font-medium">
            {c.label}
            {!c.required && <span className="ml-2 text-xs font-normal text-slate-500">optional</span>}
          </div>
          {c.detail && <div className="text-sm text-slate-600 dark:text-slate-300">{c.detail}</div>}
        </div>
      </div>
      {c.status !== "ok" && c.fixes && c.fixes.length > 0 && (
        <div className="ml-9 space-y-2">
          {c.fixes.map((f) =>
            f.kind === "command" ? (
              <CopyField key={f.label} value={f.value} />
            ) : f.kind === "link" ? (
              <a
                key={f.label}
                href={f.value}
                target="_blank"
                rel="noreferrer"
                className="inline-block rounded-lg border border-slate-300 px-3 py-1.5 text-sm hover:bg-slate-100 dark:border-slate-700 dark:hover:bg-slate-800"
              >
                {f.label} ↗
              </a>
            ) : (
              <Button key={f.label} variant="secondary" disabled={action.isPending || action.isSuccess} onClick={() => action.mutate(f.value)}>
                {action.isSuccess ? "Starting… (this can take a minute)" : f.label}
              </Button>
            ),
          )}
          <ErrorBox error={action.error} />
        </div>
      )}
    </li>
  );
}

function ChecksStep({ onNext }: { onNext: () => void }) {
  const checks = useQuery({ queryKey: ["setupChecks"], queryFn: api.setupChecks, refetchInterval: 10000 });
  const d = checks.data;
  return (
    <Card title="1. Check this PC">
      <p className="mb-2 text-sm text-slate-500 dark:text-slate-400">
        LPS runs your VPSes and apps in Linux containers using Docker. This list updates by itself while you fix things.
      </p>
      <ErrorBox error={checks.error} />
      {!d ? (
        <p className="py-6 text-center text-slate-500">Checking…</p>
      ) : (
        <>
          <ul className="divide-y divide-slate-200 dark:divide-slate-800">
            {d.checks.map((c) => (
              <CheckRow key={c.id} c={c} />
            ))}
          </ul>
          <div className="mt-4 flex flex-wrap items-center gap-3">
            <Button onClick={onNext} disabled={!d.ready}>
              Continue
            </Button>
            {!d.ready && (
              <button className="text-sm text-slate-500 underline" onClick={onNext}>
                Continue anyway (fix it later)
              </button>
            )}
          </div>
        </>
      )}
    </Card>
  );
}

function AdminStep({ onNext }: { onNext: () => void }) {
  const qc = useQueryClient();
  const setup = useQuery({ queryKey: ["setup"], queryFn: api.setupStatus });
  if (setup.data && !setup.data.needsSetup) {
    // Admin already exists (e.g. page reloaded after this step).
    return (
      <Card title="2. Admin account">
        <p className="mb-4 text-sm">The admin account is ready.</p>
        <Button onClick={onNext}>Continue</Button>
      </Card>
    );
  }
  return (
    <Card title="2. Admin account">
      <p className="mb-4 text-sm text-slate-500 dark:text-slate-400">
        You'll use this to sign in to LPS, also from your phone later. Use a strong password.
      </p>
      <CredentialsForm
        mode="setup"
        onDone={() => {
          qc.invalidateQueries({ queryKey: ["setup"] });
          qc.invalidateQueries({ queryKey: ["me"] });
          onNext();
        }}
      />
    </Card>
  );
}

function RemoteStep({ onNext }: { onNext: () => void }) {
  return (
    <Card title="3. Remote access (optional)">
      <div className="space-y-3 text-sm">
        <p>
          Want to open LPS, SSH into your VPSes and test your apps when you're away from home? LPS can use{" "}
          <b>Tailscale</b> (free) to make them reachable from your own phone and laptop, without opening your router.
        </p>
        <p className="text-slate-500 dark:text-slate-400">You can do this any time from the "Remote access" page.</p>
        <div className="flex flex-wrap gap-3 pt-1">
          <Button
            onClick={() => {
              finish("/remote");
            }}
          >
            Set it up now
          </Button>
          <Button variant="secondary" onClick={onNext}>
            Skip for now
          </Button>
        </div>
      </div>
    </Card>
  );
}

function finish(path: string) {
  setWizardActive(false);
  try {
    sessionStorage.removeItem("lps.setupStep");
  } catch {
    /* ignore */
  }
  window.location.assign(path);
}

function FinishStep() {
  const qc = useQueryClient();
  const auto = useQuery({ queryKey: ["autostart"], queryFn: api.autostart });
  const set = useMutation({ mutationFn: api.setAutostart, onSuccess: (v) => qc.setQueryData(["autostart"], v) });
  return (
    <Card title="4. All set">
      <div className="space-y-4 text-sm">
        <p>LPS is ready. Next, create your first Linux VPS or deploy an app from a project folder.</p>
        {auto.data?.supported && (
          <div className="flex items-center gap-3 rounded-lg border border-slate-200 p-3 dark:border-slate-800">
            <Switch label="Start with Windows" checked={auto.data.enabled} disabled={set.isPending} onChange={(v) => set.mutate(v)} />
            <div>
              <div className="font-medium">Start LPS when I sign in to Windows</div>
              <div className="text-xs text-slate-500 dark:text-slate-400">Recommended, so your VPSes and apps are reachable after a restart.</div>
            </div>
          </div>
        )}
        <ErrorBox error={set.error} />
        <p className="text-slate-500 dark:text-slate-400">
          LPS keeps running in the background{auto.data?.supported ? " (look for the LPS icon in the system tray)" : ""}. Bookmark{" "}
          <code>{window.location.origin}</code>.
        </p>
        <Button onClick={() => finish("/")}>Open the dashboard</Button>
      </div>
    </Card>
  );
}
