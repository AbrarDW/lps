import { lazy, Suspense, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, displayHost } from "../api";
import { Button, Card, CopyField, ErrorBox, LogView, StateBadge, formatLimits } from "../components/ui";

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
  const logs = useQuery({ queryKey: ["vps", id, "logs"], queryFn: () => api.vpsLogs(id), enabled: !!vps.data?.containerId });

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["vps"] });
  };
  const action = useMutation({ mutationFn: (a: "start" | "stop") => api.vpsAction(id, a), onSuccess: refresh });
  const del = useMutation({
    mutationFn: () => api.deleteVPS(id, keepData),
    onSuccess: () => {
      qc.removeQueries({ queryKey: ["vps", id] });
      refresh();
      nav("/");
    },
  });

  if (vps.isLoading) return <p className="text-slate-500">Loading…</p>;
  if (vps.error || !vps.data) return <ErrorBox error={vps.error ?? "not found"} />;
  const v = vps.data;
  const host = displayHost(system.data?.bindIp);
  const sshCmd = `ssh ${v.sshUser}@${host} -p ${v.sshPort}`;
  const busy = v.state === "provisioning" || action.isPending;

  return (
    <>
      <Card
        title={
          <span className="flex items-center gap-3">
            {v.name} <StateBadge state={v.state} />
          </span>
        }
        actions={
          <>
            {v.state === "running" ? (
              <Button variant="secondary" disabled={busy} onClick={() => action.mutate("stop")}>
                Stop
              </Button>
            ) : (
              <Button variant="secondary" disabled={busy || !v.containerId} onClick={() => action.mutate("start")}>
                Start
              </Button>
            )}
          </>
        }
      >
        <div className="space-y-4">
          <ErrorBox error={action.error ?? v.error} />
          {v.state === "provisioning" && (
            <p className="text-sm text-sky-700 dark:text-sky-300">
              Provisioning… The first VPS builds the Ubuntu image, which can take a few minutes.
            </p>
          )}
          {v.provisionLog && <LogView text={v.provisionLog} />}
          {v.remoteSsh && (
            <div>
              <div className="mb-1 text-sm font-medium">Connect from anywhere (Tailscale)</div>
              <CopyField value={v.remoteSsh} />
              <p className="mt-1 text-xs text-slate-500 dark:text-slate-400">
                Only works on devices with the Tailscale app installed and signed in to the same account. The error
                "Could not resolve hostname" means the device you are using is not on your tailnet.
              </p>
            </div>
          )}
          <div>
            <div className="mb-1 text-sm font-medium">{v.remoteSsh ? "Connect on this PC" : "Connect with SSH"}</div>
            <CopyField value={sshCmd} />
            {!v.remoteSsh && (
              <p className="mt-1 text-xs text-slate-500 dark:text-slate-400">
                Published on {system.data?.bindIp ?? "127.0.0.1"}:{v.sshPort}. To connect when you are away, turn on{" "}
                <Link to="/remote" className="text-indigo-600 underline dark:text-indigo-400">
                  remote access
                </Link>
                .
              </p>
            )}
          </div>
          <dl className="grid gap-3 text-sm sm:grid-cols-3">
            <div>
              <dt className="text-slate-500 dark:text-slate-400">User</dt>
              <dd>{v.sshUser}</dd>
            </div>
            <div>
              <dt className="text-slate-500 dark:text-slate-400">Limits</dt>
              <dd>{formatLimits(v.cpus, v.memoryMb)}</dd>
            </div>
            <div>
              <dt className="text-slate-500 dark:text-slate-400">Auth</dt>
              <dd>{v.sshPublicKey ? "SSH key" : "password"}</dd>
            </div>
          </dl>
        </div>
      </Card>

      <TerminalCard vpsId={v.id} running={v.state === "running"} user={v.sshUser} />

      <Card
        title="Logs"
        actions={
          <Button variant="secondary" onClick={() => logs.refetch()}>
            Refresh
          </Button>
        }
      >
        <ErrorBox error={logs.error} />
        <LogView text={logs.data?.logs} />
      </Card>

      <Card title="Danger zone">
        <div className="flex flex-wrap items-center gap-4">
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={keepData} onChange={(e) => setKeepData(e.target.checked)} />
            Keep the /home volume (re-creating a VPS with the same name reuses it)
          </label>
          <Button
            variant="danger"
            disabled={del.isPending || v.state === "provisioning"}
            onClick={() => {
              if (confirm(`Delete VPS "${v.name}"?${keepData ? "" : " All files in /home will be lost."}`)) del.mutate();
            }}
          >
            Delete VPS
          </Button>
        </div>
        <div className="mt-3">
          <ErrorBox error={del.error} />
        </div>
      </Card>
    </>
  );
}

function TerminalCard({ vpsId, running, user }: { vpsId: number; running: boolean; user: string }) {
  const [session, setSession] = useState(0); // 0 = closed; bump to reconnect
  const [full, setFull] = useState(false);
  const [connected, setConnected] = useState(false);
  const open = session > 0;

  const actions = (
    <>
      {open && connected && (
        <Button variant="secondary" onClick={() => setFull(!full)}>
          {full ? "Exit full screen" : "Full screen"}
        </Button>
      )}
      {open ? (
        <>
          {!connected && (
            <Button variant="secondary" disabled={!running} onClick={() => setSession(session + 1)}>
              Reconnect
            </Button>
          )}
          <Button
            variant="secondary"
            onClick={() => {
              setSession(0);
              setFull(false);
            }}
          >
            Close
          </Button>
        </>
      ) : (
        <Button disabled={!running} onClick={() => setSession(1)}>
          Open terminal
        </Button>
      )}
    </>
  );

  const body = open ? (
    <Suspense fallback={<p className="text-sm text-slate-500">Loading terminal…</p>}>
      <VPSTerminal key={session} vpsId={vpsId} focusKey={full} onStatus={(st) => setConnected(st === "connected")} />
    </Suspense>
  ) : (
    <p className="text-sm text-slate-500 dark:text-slate-400">
      {running
        ? `Open a shell in this VPS as ${user}, right here in the browser. Works from your phone too, over remote access.`
        : "Start the VPS to open a terminal."}
    </p>
  );

  // One element whether or not it is full screen: switching parents would
  // unmount the terminal and drop the shell session.
  const fs = full && open;
  return (
    <section
      className={
        fs
          ? "fixed inset-0 z-50 flex flex-col bg-slate-950 p-3 text-slate-100"
          : "rounded-xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-800 dark:bg-slate-900"
      }
    >
      <div className={`flex flex-wrap items-center justify-between gap-2 ${fs ? "mb-2" : "mb-4"}`}>
        <h2 className={fs ? "text-sm text-slate-300" : "text-lg font-semibold"}>{fs ? `Terminal · ${user}` : "Terminal"}</h2>
        <div className="flex flex-wrap gap-2">{actions}</div>
      </div>
      <div className={fs ? "min-h-0 flex-1" : open ? "h-[26rem]" : ""}>{body}</div>
    </section>
  );
}
