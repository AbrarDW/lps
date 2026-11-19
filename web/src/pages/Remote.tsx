import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type RemoteView } from "../api";
import { ActionBox, Button, Card, CopyField, ErrorBox, StateBadge, Switch } from "../components/ui";

type StepState = "done" | "todo" | "blocked";

function Step({
  n,
  title,
  state,
  keepOpen,
  children,
}: {
  n: number;
  title: string;
  state: StepState;
  keepOpen?: boolean; // show the body even when the step is done
  children?: React.ReactNode;
}) {
  const dot =
    state === "done"
      ? "bg-emerald-600 text-white"
      : state === "todo"
        ? "bg-indigo-600 text-white"
        : "bg-slate-200 text-slate-500 dark:bg-slate-800 dark:text-slate-400";
  return (
    <li className="flex gap-3">
      <span className={`grid h-7 w-7 shrink-0 place-items-center rounded-full text-sm font-semibold ${dot}`}>{state === "done" ? "✓" : n}</span>
      <div className="min-w-0 flex-1 pb-5">
        <div className={`font-medium ${state === "blocked" ? "text-slate-400 dark:text-slate-500" : ""}`}>{title}</div>
        {(state === "todo" || (state === "done" && keepOpen)) && children && <div className="mt-2 space-y-2 text-sm">{children}</div>}
      </div>
    </li>
  );
}

export function Remote() {
  const qc = useQueryClient();
  const remote = useQuery({ queryKey: ["remote"], queryFn: api.remote, refetchInterval: 5000 });
  const update = (v: RemoteView) => {
    qc.setQueryData(["remote"], v);
    qc.invalidateQueries({ queryKey: ["vps"] });
    qc.invalidateQueries({ queryKey: ["apps"] });
  };
  const toggle = useMutation({ mutationFn: api.setRemote, onSuccess: update });
  const sync = useMutation({ mutationFn: api.syncRemote, onSuccess: update });

  if (remote.isLoading) return <p className="text-slate-500">Loading…</p>;
  if (remote.error || !remote.data) return <ErrorBox error={remote.error ?? "unavailable"} />;
  const r = remote.data;
  if (!r.available) {
    return (
      <Card title="Remote access">
        <p className="text-sm">Remote access is turned off for this lpsd (started with <code>-no-remote</code>).</p>
      </Card>
    );
  }
  const st = r.status;
  const installed = st.installed;
  const signedIn = installed && st.running;
  const https = signedIn && st.httpsEnabled;
  const busy = toggle.isPending || sync.isPending;

  return (
    <>
      <Card
        title="Remote access with Tailscale"
        actions={
          <Button variant="secondary" disabled={busy} onClick={() => sync.mutate()}>
            {sync.isPending ? "Checking…" : "Check again"}
          </Button>
        }
      >
        <p className="mb-5 text-sm text-slate-500 dark:text-slate-400">
          Reach the dashboard, your VPSes and your apps from your own phone or laptop, from anywhere. Traffic goes through your
          private Tailscale network: nothing is opened on your router, and only devices signed in to your Tailscale account
          can connect.
        </p>
        <ol>
          <Step n={1} title="Install Tailscale on this PC" state={installed ? "done" : "todo"}>
            <p>Download the Tailscale app for Windows, install it, then come back and press "Check again".</p>
            <a className="text-indigo-600 underline dark:text-indigo-400" href="https://tailscale.com/download" target="_blank" rel="noreferrer">
              tailscale.com/download ↗
            </a>
          </Step>
          <Step n={2} title="Sign in to Tailscale on this PC" state={signedIn ? "done" : installed ? "todo" : "blocked"}>
            <p>
              Open the Tailscale app (system tray icon) and sign in. Current state: <code>{st.state || "unknown"}</code>.
            </p>
          </Step>
          <Step n={3} title="Turn on HTTPS certificates for your tailnet" state={https ? "done" : signedIn ? "todo" : "blocked"}>
            <p>
              In the Tailscale admin console, open <b>DNS</b> and enable <b>HTTPS Certificates</b> (one click). This gives your
              PC a secure address like <code>{st.dnsName || "your-pc.tailnet.ts.net"}</code>.
            </p>
            <a className="text-indigo-600 underline dark:text-indigo-400" href="https://login.tailscale.com/admin/dns" target="_blank" rel="noreferrer">
              Open the DNS settings ↗
            </a>
          </Step>
          <Step n={4} title="Turn on remote access" state={r.enabled ? "done" : https ? "todo" : "blocked"} keepOpen>
            <div className="flex items-center gap-3">
              <Switch
                label="Enable remote access"
                checked={r.enabled}
                disabled={busy || (!r.enabled && !signedIn)}
                onChange={(v) => toggle.mutate(v)}
              />
              <span>{r.enabled ? "On: your dashboard, VPSes and apps are on your tailnet" : "Off"}</span>
            </div>
          </Step>
          <Step n={5} title="Install Tailscale on your phone or laptop" state={r.enabled ? "todo" : "blocked"}>
            <p>
              Sign in with the <b>same account</b>, then open your dashboard from anywhere. These addresses work only on
              devices signed in to your Tailscale account.
            </p>
            {r.dashboardUrl ? <CopyField value={r.dashboardUrl} /> : <p className="text-slate-500">The address appears here once it is active.</p>}
          </Step>
        </ol>
        {r.enabled && r.needsAction && <ActionBox message={r.needsAction.message} url={r.needsAction.url} />}
        <div className="mt-2">
          <ErrorBox error={toggle.error ?? sync.error ?? r.error} />
        </div>
      </Card>

      <Card title="Troubleshooting">
        <div className="space-y-2 text-sm">
          <details className="rounded-lg border border-slate-200 p-3 dark:border-slate-800">
            <summary className="cursor-pointer font-medium">"Could not resolve hostname" or the site can't be reached</summary>
            <ul className="mt-2 list-disc space-y-1 pl-5 text-slate-600 dark:text-slate-300">
              <li>
                The device you are using must have the Tailscale app installed, <b>connected</b>, and signed in to the{" "}
                <b>same account</b> as this PC. Without it, <code>{st.dnsName || "your-pc.tailnet.ts.net"}</code> does not
                exist for that device.
              </li>
              <li>Check that this PC appears in the device's Tailscale device list.</li>
              <li>On a Mac, keep "Use Tailscale DNS settings" turned on in the Tailscale app.</li>
              <li>Turn off other VPNs or iCloud Private Relay, which can override DNS.</li>
            </ul>
          </details>
          <details className="rounded-lg border border-slate-200 p-3 dark:border-slate-800">
            <summary className="cursor-pointer font-medium">It worked before, now it doesn't</summary>
            <ul className="mt-2 list-disc space-y-1 pl-5 text-slate-600 dark:text-slate-300">
              <li>This PC must be on and awake, with Docker Desktop, Tailscale and lpsd running.</li>
              <li>Press "Check again" above and look for errors in "What is reachable".</li>
            </ul>
          </details>
          <details className="rounded-lg border border-slate-200 p-3 dark:border-slate-800">
            <summary className="cursor-pointer font-medium">Sharing with someone who doesn't have Tailscale</summary>
            <p className="mt-2 text-slate-600 dark:text-slate-300">
              Open the app and turn on its <b>Public link</b>. Public links can take about a minute to start working. The
              dashboard and SSH are never made public.
            </p>
          </details>
        </div>
      </Card>

      {r.enabled && (
        <Card title="What is reachable">
          {r.exposures.length === 0 ? (
            <p className="text-sm text-slate-500">Nothing yet.</p>
          ) : (
            <ul className="divide-y divide-slate-200 text-sm dark:divide-slate-800">
              {r.exposures.map((e) => (
                <li key={e.port} className="flex flex-wrap items-center justify-between gap-2 py-2.5">
                  <div className="min-w-0">
                    <div className="font-medium">
                      {e.label}{" "}
                      {e.public && (
                        <span className="ml-1 rounded bg-amber-100 px-1.5 py-0.5 text-xs text-amber-800 dark:bg-amber-900/50 dark:text-amber-300">
                          public
                        </span>
                      )}
                    </div>
                    <div className="truncate text-xs text-slate-500 dark:text-slate-400">
                      {e.url || `${e.proto} port ${e.port}`} → 127.0.0.1:{e.localPort}
                    </div>
                    {e.error && <div className="text-xs text-rose-600 dark:text-rose-400">{e.error}</div>}
                  </div>
                  <StateBadge state={e.state} />
                </li>
              ))}
            </ul>
          )}
          {r.lastSync && <p className="mt-3 text-xs text-slate-500">Last checked {new Date(r.lastSync).toLocaleTimeString()}</p>}
        </Card>
      )}
    </>
  );
}
