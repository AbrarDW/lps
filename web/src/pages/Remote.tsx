import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type RemoteView } from "../api";
import { Link } from "react-router-dom";
import { ActionBox, Button, Callout, Card, CopyField, ErrorBox, PageHeader, StateBadge, Switch } from "../components/ui";

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
      ? "bg-success/25 text-success-ink border border-success/50"
      : state === "todo"
        ? "bg-primary text-white border border-cyan"
        : "bg-panel text-ink-faint border border-line-strong";
  return (
    <li className="flex gap-3">
      <span className={`grid h-7 w-7 shrink-0 place-items-center rounded-[2px] font-mono text-xs font-medium ${dot}`}>{state === "done" ? "✓" : String(n).padStart(2, "0")}</span>
      <div className="min-w-0 flex-1 pb-5">
        <div className={`font-medium ${state === "blocked" ? "text-ink-faint" : ""}`}>{title}</div>
        {(state === "todo" || (state === "done" && keepOpen)) && children && <div className="mt-2 space-y-2 text-sm">{children}</div>}
      </div>
    </li>
  );
}

export function Remote() {
  const me = useQuery({ queryKey: ["me"], queryFn: api.me });
  const qc = useQueryClient();
  const remote = useQuery({ queryKey: ["remote"], queryFn: api.remote, refetchInterval: 5000 });
  const update = (v: RemoteView) => {
    qc.setQueryData(["remote"], v);
    qc.invalidateQueries({ queryKey: ["vps"] });
    qc.invalidateQueries({ queryKey: ["apps"] });
  };
  const toggle = useMutation({ mutationFn: api.setRemote, onSuccess: update });
  const sync = useMutation({ mutationFn: api.syncRemote, onSuccess: update });

  if (remote.isLoading) return <p className="text-ink-muted">Loading…</p>;
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
      <PageHeader sheet="Sheet 04 — Remote access" title="Reach this PC from anywhere" />
      {me.data && !me.data.twoFactor && (
        <Callout intent="warning">
          <b>Turn on two-factor sign-in first.</b> With remote access, anyone who gets your password could reach this dashboard from another device on
          your tailnet. <Link to="/account">Set up two-factor sign-in</Link> (takes a minute with an authenticator app).
        </Callout>
      )}
      <Card
        title="Remote access with Tailscale"
        marks
        actions={
          <Button variant="secondary" disabled={busy} onClick={() => sync.mutate()}>
            {sync.isPending ? "Checking…" : "Check again"}
          </Button>
        }
      >
        <p className="mb-5 text-[13px] text-ink-muted">
          Reach the dashboard, your VPS and your apps from your own phone or laptop, from anywhere. Traffic goes through your
          private Tailscale network: nothing is opened on your router, and only devices signed in to your Tailscale account
          can connect.
        </p>
        <ol>
          <Step n={1} title="Install Tailscale on this PC" state={installed ? "done" : "todo"}>
            <p>Download the Tailscale app for Windows, install it, then come back and press "Check again".</p>
            <a className="underline" href="https://tailscale.com/download" target="_blank" rel="noreferrer">
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
            <a className="underline" href="https://login.tailscale.com/admin/dns" target="_blank" rel="noreferrer">
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
              <span>{r.enabled ? "On: your dashboard, VPS and apps are on your tailnet" : "Off"}</span>
            </div>
          </Step>
          <Step n={5} title="Install Tailscale on your phone or laptop" state={r.enabled ? "todo" : "blocked"}>
            <p>
              Sign in with the <b>same account</b>, then open your dashboard from anywhere. These addresses work only on
              devices signed in to your Tailscale account.
            </p>
            {r.dashboardUrl ? <CopyField value={r.dashboardUrl} /> : <p className="text-ink-muted">The address appears here once it is active.</p>}
          </Step>
        </ol>
        {r.enabled && r.needsAction && <ActionBox message={r.needsAction.message} url={r.needsAction.url} />}
        <div className="mt-2">
          <ErrorBox error={toggle.error ?? sync.error ?? r.error} />
        </div>
      </Card>

      <Card title="Troubleshooting">
        <div className="space-y-2 text-sm">
          <details className="rounded-[2px] border border-line-soft bg-ground-deep/40 p-3">
            <summary className="cursor-pointer font-medium">"Could not resolve hostname" or the site can't be reached</summary>
            <ul className="mt-2 list-disc space-y-1 pl-5 text-ink-2">
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
          <details className="rounded-[2px] border border-line-soft bg-ground-deep/40 p-3">
            <summary className="cursor-pointer font-medium">It worked before, now it doesn't</summary>
            <ul className="mt-2 list-disc space-y-1 pl-5 text-ink-2">
              <li>This PC must be on and awake, with Docker Desktop, Tailscale and lpsd running.</li>
              <li>Press "Check again" above and look for errors in "What is reachable".</li>
            </ul>
          </details>
          <details className="rounded-[2px] border border-line-soft bg-ground-deep/40 p-3">
            <summary className="cursor-pointer font-medium">Sharing with someone who doesn't have Tailscale</summary>
            <p className="mt-2 text-ink-2">
              Open the app and turn on its <b>Public link</b>. Public links can take about a minute to start working. The
              dashboard and SSH are never made public.
            </p>
          </details>
        </div>
      </Card>

      {r.enabled && (
        <Card title="What is reachable">
          {r.exposures.length === 0 ? (
            <p className="text-[13px] text-ink-muted">Nothing yet.</p>
          ) : (
            <ul className="divide-y divide-line-soft text-[13px]">
              {r.exposures.map((e) => (
                <li key={e.port} className="flex flex-wrap items-center justify-between gap-2 py-2.5">
                  <div className="min-w-0">
                    <div className="font-medium">
                      {e.label}{" "}
                      {e.public && (
                        <span className="ml-1 rounded-[2px] bg-warning/20 px-1.5 py-0.5 font-mono text-[10px] text-warning-ink uppercase">
                          public
                        </span>
                      )}
                    </div>
                    <div className="truncate font-mono text-xs text-ink-muted">
                      {e.url || `${e.proto} port ${e.port}`} → 127.0.0.1:{e.localPort}
                    </div>
                    {e.error && <div className="text-xs text-danger-ink">{e.error}</div>}
                  </div>
                  <StateBadge state={e.state} />
                </li>
              ))}
            </ul>
          )}
          {r.lastSync && <p className="label-mono mt-3 text-[10px] text-ink-faint">Last checked {new Date(r.lastSync).toLocaleTimeString()}</p>}
        </Card>
      )}
    </>
  );
}
