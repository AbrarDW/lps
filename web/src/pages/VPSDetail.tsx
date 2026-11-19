import { lazy, Suspense, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, displayHost } from "../api";
import {
  Button,
  Callout,
  Card,
  CopyField,
  CornerMarks,
  ErrorBox,
  Icon,
  LogView,
  SpecGrid,
  StateBadge,
  Tabs,
  buttonClass,
  formatLimits,
} from "../components/ui";

// xterm.js is only downloaded when a terminal is opened.
const VPSTerminal = lazy(() => import("../components/VPSTerminal"));

export function VPSDetail() {
  const id = Number(useParams().id);
  const nav = useNavigate();
  const qc = useQueryClient();
  const [keepData, setKeepData] = useState(false);
  const system = useQuery({ queryKey: ["system"], queryFn: api.system });
  const vps = useQuery({
    queryKey: ["vps", id],
    queryFn: () => api.getVPS(id),
    refetchInterval: (q) => (q.state.data?.state === "provisioning" ? 2000 : 5000),
  });

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["vps"] });
  };
  const action = useMutation({ mutationFn: (a: "start" | "stop") => api.vpsAction(id, a), onSuccess: refresh });
  const del = useMutation({
    mutationFn: () => api.deleteVPS(id, keepData),
    onSuccess: () => {
      qc.removeQueries({ queryKey: ["vps", id] });
      refresh();
      nav("/vps");
    },
  });

  if (vps.isLoading) return <p className="label-mono text-ink-faint">Loading…</p>;
  if (vps.error || !vps.data) return <ErrorBox error={vps.error ?? "not found"} />;
  const v = vps.data;
  const host = displayHost(system.data?.bindIp);
  const sshCmd = `ssh ${v.sshUser}@${host} -p ${v.sshPort}`;
  const busy = v.state === "provisioning" || action.isPending;

  return (
    <>
      <nav aria-label="Breadcrumb" className="flex gap-2 font-mono text-xs text-ink-faint uppercase">
        <Link to="/vps" className="no-underline">
          VPSes
        </Link>
        <span aria-hidden="true">/</span>
        <span className="text-ink">{v.name}</span>
      </nav>

      <section className="flex flex-wrap items-center justify-between gap-4">
        <div className="flex flex-wrap items-center gap-3">
          <h1 className="m-0 text-[28px] font-semibold">{v.name}</h1>
          <StateBadge state={v.state} />
          <span className="font-mono text-xs text-ink-faint">ubuntu-24.04 · lps-vps-{v.name}</span>
        </div>
        <div className="flex flex-wrap gap-2">
          {v.state === "running" ? (
            <Button variant="secondary" disabled={busy} onClick={() => action.mutate("stop")}>
              <Icon name="stop" />
              Stop
            </Button>
          ) : (
            <Button variant="secondary" disabled={busy || !v.containerId} onClick={() => action.mutate("start")}>
              <Icon name="play" />
              Start
            </Button>
          )}
        </div>
      </section>

      <ErrorBox error={action.error ?? v.error} />
      {v.state === "provisioning" && (
        <Callout>
          <b>Provisioning…</b> The first VPS builds the Ubuntu image, which can take a few minutes.
        </Callout>
      )}
      {v.provisionLog && <LogView text={v.provisionLog} />}

      <section className="flex flex-wrap items-start gap-5">
        <div className="min-w-0 flex-[999_1_640px]">
          <ShellPanel vpsId={v.id} running={v.state === "running"} user={v.sshUser} hasContainer={!!v.containerId} />
        </div>

        <aside className="flex min-w-0 flex-[1_1_320px] flex-col gap-5">
          <Card title="Connect with SSH">
            <div className="flex flex-col gap-3.5">
              {v.remoteSsh && (
                <div className="flex flex-col gap-1.5">
                  <span className="text-xs text-ink-muted">From anywhere (Tailscale)</span>
                  <CopyField value={v.remoteSsh} />
                  <span className="text-xs text-ink-faint">
                    Only works on devices signed in to your Tailscale account. "Could not resolve hostname" means this device isn't on your tailnet.
                  </span>
                </div>
              )}
              <div className="flex flex-col gap-1.5">
                <span className="text-xs text-ink-muted">On this PC</span>
                <CopyField value={sshCmd} />
                {!v.remoteSsh && (
                  <span className="text-xs text-ink-faint">
                    To connect when you're away, turn on <Link to="/remote">remote access</Link>.
                  </span>
                )}
              </div>
            </div>
          </Card>

          <Card
            title="Apps on this VPS"
            flush
            actions={
              <Link to={`/apps/new?vps=${v.id}`} className={buttonClass("secondary", "sm")}>
                <Icon name="plus" />
                Deploy an app here
              </Link>
            }
          >
            {v.apps.length === 0 ? (
              <p className="m-0 p-4 text-[13px] text-ink-muted">
                None yet. LPS can copy a project folder from this PC, or use a folder you cloned in the VPS, and keep it running with its own links.
              </p>
            ) : (
              <ul className="m-0 list-none p-0">
                {v.apps.map((a, i) => (
                  <li key={a.id} className={`flex items-center justify-between gap-3 px-4 py-2.5 ${i === v.apps.length - 1 ? "" : "border-b border-line-soft"}`}>
                    <Link to={`/apps/${a.id}`} className="font-semibold text-ink no-underline hover:text-cyan-soft">
                      {a.name}
                    </Link>
                    <span className="font-mono text-xs text-ink-2">
                      {host}:{a.hostPort}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </Card>

          <Card title="Specification" flush>
            <SpecGrid
              items={[
                { label: "User", value: `${v.sshUser} · ${v.sshPublicKey ? "SSH key" : "password"}` },
                { label: "Image", value: "Ubuntu 24.04" },
                { label: "Limits", value: formatLimits(v.cpus, v.memoryMb) },
                { label: "SSH port", value: <span className="font-mono">{v.sshPort}</span> },
                { label: "/home volume", value: "persistent" },
                { label: "Published on", value: <span className="font-mono">{system.data?.bindIp ?? "127.0.0.1"}</span> },
              ]}
            />
          </Card>

          <Card title="Danger zone" tone="danger">
            <div className="flex flex-col gap-3 text-[13px]">
              <label className="flex items-start gap-2 text-ink-2">
                <input type="checkbox" className="mt-1 accent-[#2d72d2]" checked={keepData} onChange={(e) => setKeepData(e.target.checked)} />
                Keep the /home volume (re-creating a VPS with the same name reuses it)
              </label>
              <Button
                variant="danger"
                className="self-start"
                disabled={del.isPending || v.state === "provisioning" || v.apps.length > 0}
                onClick={() => {
                  if (confirm(`Delete VPS "${v.name}"?${keepData ? "" : " All files in /home will be lost."}`)) del.mutate();
                }}
              >
                Delete VPS
              </Button>
              {v.apps.length > 0 && <span className="text-xs text-ink-muted">Delete the apps on this VPS first.</span>}
              <ErrorBox error={del.error} />
            </div>
          </Card>
        </aside>
      </section>
    </>
  );
}

/** Terminal + logs, as tabs in one panel. */
function ShellPanel({ vpsId, running, user, hasContainer }: { vpsId: number; running: boolean; user: string; hasContainer: boolean }) {
  const [tab, setTab] = useState<"terminal" | "logs">("terminal");
  const [session, setSession] = useState(0); // 0 = closed; bump to reconnect
  const [full, setFull] = useState(false);
  const [status, setStatus] = useState<"connecting" | "connected" | "closed">("connecting");
  const logs = useQuery({ queryKey: ["vps", vpsId, "logs"], queryFn: () => api.vpsLogs(vpsId), enabled: hasContainer && tab === "logs" });
  const open = session > 0;
  const fs = full && open && tab === "terminal";

  const right =
    tab === "terminal" ? (
      <>
        {open && (
          <span className={`flex items-center gap-1.5 font-mono text-[11px] uppercase ${status === "connected" ? "text-success-ink" : "text-ink-faint"}`}>
            <span className={`size-1.5 rounded-full ${status === "connected" ? "bg-[#72ca9b]" : "bg-ink-faint"}`} />
            {status}
          </span>
        )}
        {open && status === "connected" && (
          <Button variant="secondary" size="sm" aria-label={full ? "Exit full screen" : "Full screen"} onClick={() => setFull(!full)}>
            <Icon name="fullscreen" />
            {full ? "Exit full screen" : "Full screen"}
          </Button>
        )}
        {open && (
          <>
            {status !== "connected" && (
              <Button variant="secondary" size="sm" disabled={!running} onClick={() => setSession(session + 1)}>
                Reconnect
              </Button>
            )}
            <Button
              variant="secondary"
              size="sm"
              onClick={() => {
                setSession(0);
                setFull(false);
              }}
            >
              Close
            </Button>
          </>
        )}
      </>
    ) : (
      <Button variant="secondary" size="sm" onClick={() => logs.refetch()}>
        Refresh
      </Button>
    );

  // One element whether or not it is full screen: switching parents would
  // unmount the terminal and drop the shell session.
  return (
    <section className={fs ? "fixed inset-0 z-50 flex flex-col bg-ground-deep p-3" : "relative rounded-bp border border-line bg-panel"}>
      {!fs && <CornerMarks />}
      <Tabs
        label="VPS views"
        tabs={[
          { id: "terminal", label: "Terminal" },
          { id: "logs", label: "Logs" },
        ]}
        value={tab}
        onChange={(t) => {
          setTab(t);
          setFull(false);
        }}
        right={right}
      />
      {/* The terminal stays mounted while the Logs tab is shown, so switching tabs keeps the session. */}
      <div className={tab === "terminal" ? (fs ? "min-h-0 flex-1 pt-3" : "p-3") : "hidden"}>
        {open ? (
          <div className={fs ? "h-full" : "h-[30rem]"}>
            <Suspense fallback={<p className="label-mono text-ink-faint">Loading terminal…</p>}>
              <VPSTerminal key={session} vpsId={vpsId} focusKey={`${full}-${tab}`} onStatus={(st) => setStatus(st)} />
            </Suspense>
          </div>
        ) : (
          <div className="grid h-[30rem] place-items-center rounded-[2px] border border-dashed border-line-strong bg-term/60 p-6 text-center">
            <div className="flex max-w-sm flex-col items-center gap-3">
              <span className="label-mono text-cyan">Shell · bash -l as {user}</span>
              <p className="m-0 text-ink-muted">
                {running
                  ? "Open a shell in this VPS right here in the browser. Works from your phone too, over remote access."
                  : "Start the VPS to open a terminal."}
              </p>
              <Button disabled={!running} onClick={() => setSession(1)}>
                <Icon name="terminal" />
                Open terminal
              </Button>
            </div>
          </div>
        )}
        {!fs && <p className="label-mono m-0 mt-2 text-[10px] text-ink-faint">Shell as {user} · sessions are logged</p>}
      </div>
      {tab === "logs" && (
        <div className="p-3">
          <ErrorBox error={logs.error} />
          <LogView text={logs.data?.logs} className="!max-h-[30rem] min-h-[30rem]" />
        </div>
      )}
    </section>
  );
}
