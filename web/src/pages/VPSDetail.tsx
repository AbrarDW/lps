import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "react-router-dom";
import { api, displayHost } from "../api";
import { Button, Card, CopyField, ErrorBox, LogView, StateBadge, formatLimits } from "../components/ui";

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
          <div>
            <div className="mb-1 text-sm font-medium">Connect with SSH</div>
            <CopyField value={sshCmd} />
            <p className="mt-1 text-xs text-slate-500 dark:text-slate-400">
              Published on {system.data?.bindIp ?? "127.0.0.1"}:{v.sshPort}. Remote access through a tunnel arrives in a
              later release; for now use this PC, or start lpsd with <code>-bind-ip 0.0.0.0</code> to reach it from your LAN.
            </p>
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
