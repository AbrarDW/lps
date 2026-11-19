import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, ApiError, displayHost } from "../api";
import { DockerfileCard } from "../components/DockerfileCard";
import { ActionBox, Button, Card, ErrorBox, LogView, StateBadge, Switch, formatLimits } from "../components/ui";

export function AppDetail() {
  const id = Number(useParams().id);
  const nav = useNavigate();
  const qc = useQueryClient();
  const [selected, setSelected] = useState<number | null>(null);
  const system = useQuery({ queryKey: ["system"], queryFn: api.system });
  const app = useQuery({
    queryKey: ["apps", id],
    queryFn: () => api.getApp(id),
    refetchInterval: (q) => (q.state.data?.deploying ? 1500 : 5000),
  });
  const deploying = app.data?.deploying;
  const deployments = useQuery({
    queryKey: ["apps", id, "deployments"],
    queryFn: () => api.deployments(id),
    refetchInterval: deploying ? 1500 : 5000,
  });
  const logs = useQuery({ queryKey: ["apps", id, "logs"], queryFn: () => api.appLogs(id), enabled: !!app.data?.containerId });

  const remote = useQuery({ queryKey: ["remote"], queryFn: api.remote });
  const refresh = () => qc.invalidateQueries({ queryKey: ["apps"] });
  const setPublic = useMutation({
    mutationFn: (p: boolean) => api.setAppPublic(id, p),
    onSuccess: () => {
      refresh();
      qc.invalidateQueries({ queryKey: ["remote"] });
    },
  });
  const deploy = useMutation({ mutationFn: () => api.deployApp(id), onSuccess: () => { setSelected(null); refresh(); } });
  const action = useMutation({ mutationFn: (a: "start" | "stop") => api.appAction(id, a), onSuccess: refresh });
  const del = useMutation({
    mutationFn: () => api.deleteApp(id),
    onSuccess: () => {
      qc.removeQueries({ queryKey: ["apps", id] });
      refresh();
      nav("/");
    },
  });

  if (app.isLoading) return <p className="text-slate-500">Loading…</p>;
  if (app.error || !app.data) return <ErrorBox error={app.error ?? "not found"} />;
  const a = app.data;
  const url = `http://${displayHost(system.data?.bindIp)}:${a.hostPort}`;
  const shown = deployments.data?.find((d) => d.id === selected) ?? deployments.data?.[0];

  return (
    <>
      <Card
        title={
          <span className="flex items-center gap-3">
            {a.name} <StateBadge state={deploying ? "building" : a.state} />
          </span>
        }
        actions={
          <>
            <Button disabled={deploying || deploy.isPending} onClick={() => deploy.mutate()}>
              {deploying ? "Deploying…" : "Redeploy"}
            </Button>
            {a.state === "running" ? (
              <Button variant="secondary" disabled={action.isPending} onClick={() => action.mutate("stop")}>
                Stop
              </Button>
            ) : (
              <Button variant="secondary" disabled={action.isPending || !a.containerId} onClick={() => action.mutate("start")}>
                Start
              </Button>
            )}
          </>
        }
      >
        <div className="space-y-3 text-sm">
          <ErrorBox error={deploy.error ?? action.error ?? a.error} />
          <div>
            <span className="text-slate-500 dark:text-slate-400">URL: </span>
            {a.state === "running" ? (
              <a className="text-indigo-600 hover:underline dark:text-indigo-400" href={url} target="_blank" rel="noreferrer">
                {url}
              </a>
            ) : (
              <span>{url}</span>
            )}
          </div>
          {a.remoteUrl && (
            <div>
              <span className="text-slate-500 dark:text-slate-400">From anywhere (Tailscale): </span>
              <a className="text-indigo-600 hover:underline dark:text-indigo-400" href={a.remoteUrl} target="_blank" rel="noreferrer">
                {a.remoteUrl}
              </a>
              <span className="block text-xs text-slate-500 dark:text-slate-400">
                Tailscale devices only. Use the public link below to share with anyone.
              </span>
            </div>
          )}
          <div className="break-all">
            <span className="text-slate-500 dark:text-slate-400">Source: </span>
            <code>{a.sourcePath}</code>
          </div>
          <div>
            <span className="text-slate-500 dark:text-slate-400">Container port {a.containerPort} · </span>
            {formatLimits(a.cpus, a.memoryMb)}
          </div>
        </div>
      </Card>

      <Card title="Public link">
        {!remote.data?.enabled ? (
          <p className="text-sm text-slate-500 dark:text-slate-400">
            Turn on{" "}
            <Link to="/remote" className="text-indigo-600 underline dark:text-indigo-400">
              remote access
            </Link>{" "}
            to reach this app from your devices, or to share it with a public link.
          </p>
        ) : (
          <div className="space-y-3 text-sm">
            <div className="flex items-center gap-3">
              <Switch
                label="Public link"
                checked={a.publicPort > 0}
                disabled={setPublic.isPending}
                onChange={(v) => {
                  if (!v || confirm("Anyone on the internet who has the link will be able to open this app. Continue?")) {
                    setPublic.mutate(v);
                  }
                }}
              />
              <span>{a.publicPort > 0 ? "Anyone with the link can open this app" : "Only your Tailscale devices can open this app"}</span>
            </div>
            {a.publicUrl && (
              <a className="block break-all text-indigo-600 hover:underline dark:text-indigo-400" href={a.publicUrl} target="_blank" rel="noreferrer">
                {a.publicUrl}
              </a>
            )}
            {a.publicPort > 0 && !a.publicUrl && <p className="text-slate-500">Setting up the public link…</p>}
            {setPublic.error instanceof ApiError && setPublic.error.needsAction ? (
              <ActionBox message={setPublic.error.needsAction.message} url={setPublic.error.needsAction.url} linkText="How to enable" />
            ) : (
              <ErrorBox error={setPublic.error} />
            )}
            <p className="text-xs text-slate-500 dark:text-slate-400">
              Uses Tailscale Funnel. A PC can have at most 2 public apps at a time.
            </p>
          </div>
        )}
      </Card>

      <DockerfileCard appId={a.id} deploying={deploying} />

      <Card title="Deployments">
        <div className="mb-3 flex flex-wrap gap-2">
          {deployments.data?.map((d) => (
            <button
              key={d.id}
              onClick={() => setSelected(d.id)}
              className={`flex items-center gap-2 rounded-lg border px-2.5 py-1 text-xs ${
                shown?.id === d.id ? "border-indigo-500" : "border-slate-200 dark:border-slate-700"
              }`}
            >
              #{d.id} <StateBadge state={d.status} />
            </button>
          ))}
        </div>
        {shown && (
          <p className="mb-2 text-xs text-slate-500 dark:text-slate-400">
            Started {new Date(shown.startedAt).toLocaleString()}
            {shown.image && <> · image {shown.image}</>}
          </p>
        )}
        <LogView text={shown?.log} empty="No deployments yet." />
      </Card>

      <Card title="Container logs" actions={<Button variant="secondary" onClick={() => logs.refetch()}>Refresh</Button>}>
        <ErrorBox error={logs.error} />
        <LogView text={logs.data?.logs} />
      </Card>

      <Card title="Danger zone">
        <Button
          variant="danger"
          disabled={del.isPending || deploying}
          onClick={() => {
            if (confirm(`Delete app "${a.name}"? The container is removed; your project folder is not touched.`)) del.mutate();
          }}
        >
          Delete app
        </Button>
        <div className="mt-3">
          <ErrorBox error={del.error} />
        </div>
      </Card>
    </>
  );
}
