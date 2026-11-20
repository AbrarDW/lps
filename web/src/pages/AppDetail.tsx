import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, ApiError, displayHost } from "../api";
import { DockerfileCard } from "../components/DockerfileCard";
import { RunConfigCard } from "../components/RunConfigCard";
import { EnvCard } from "../components/EnvCard";
import { ActivityList } from "../components/Activity";
import { UsageCard } from "../components/Usage";
import { ActionBox, Button, Callout, Card, CornerMarks, ErrorBox, Icon, LogView, StateBadge, Switch, Tabs, Tag, formatLimits } from "../components/ui";

type AppTab = "deployments" | "dockerfile" | "env" | "logs";

export function AppDetail() {
  const id = Number(useParams().id);
  const nav = useNavigate();
  const qc = useQueryClient();
  const [tab, setTab] = useState<AppTab>("deployments");
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
  const logs = useQuery({ queryKey: ["apps", id, "logs"], queryFn: () => api.appLogs(id), enabled: !!app.data?.containerId && tab === "logs" });

  const remote = useQuery({ queryKey: ["remote"], queryFn: api.remote });
  const refresh = () => qc.invalidateQueries({ queryKey: ["apps"] });
  const setPublic = useMutation({
    mutationFn: (p: boolean) => api.setAppPublic(id, p),
    onSuccess: () => {
      refresh();
      qc.invalidateQueries({ queryKey: ["remote"] });
    },
  });
  const deploy = useMutation({
    mutationFn: () => api.deployApp(id),
    onSuccess: () => {
      setSelected(null);
      setTab("deployments");
      refresh();
    },
  });
  const action = useMutation({ mutationFn: (a: "start" | "stop") => api.appAction(id, a), onSuccess: refresh });
  const del = useMutation({
    mutationFn: () => api.deleteApp(id),
    onSuccess: () => {
      qc.removeQueries({ queryKey: ["apps", id] });
      refresh();
      nav("/apps");
    },
  });

  if (app.isLoading) return <p className="label-mono text-ink-faint">Loading…</p>;
  if (app.error || !app.data) return <ErrorBox error={app.error ?? "not found"} />;
  const a = app.data;
  const url = `http://${displayHost(system.data?.bindIp)}:${a.hostPort}`;
  const shown = deployments.data?.find((d) => d.id === selected) ?? deployments.data?.[0];
  const running = a.state === "running";
  const inVPS = a.vpsId > 0;

  return (
    <>
      <nav aria-label="Breadcrumb" className="flex gap-2 font-mono text-xs text-ink-faint uppercase">
        <Link to="/apps" className="no-underline">
          Apps
        </Link>
        <span aria-hidden="true">/</span>
        <span className="text-ink">{a.name}</span>
      </nav>

      <section className="flex flex-wrap items-center justify-between gap-4">
        <div className="flex min-w-0 flex-col gap-1.5">
          <div className="flex flex-wrap items-center gap-3">
            <h1 className="m-0 text-[28px] font-semibold">{a.name}</h1>
            <StateBadge state={deploying ? "building" : a.state} />
            {a.publicUrl && <Tag intent="warning">Public</Tag>}
            {inVPS && (
              <Link to={`/vps/${a.vpsId}`} className="no-underline">
                <Tag intent="primary">VPS {a.vpsName}</Tag>
              </Link>
            )}
          </div>
          <span className="font-mono text-xs break-all text-ink-faint">
            {inVPS ? (
              <>
                {a.sourceKind === "vps" ? `${a.vpsName}:${a.vpsPath}` : a.sourcePath} · port {a.containerPort} in the VPS · <span className="normal-case">{a.startCmd}</span>
              </>
            ) : (
              <>
                {a.sourcePath} · container port {a.containerPort} · {formatLimits(a.cpus, a.memoryMb)} · {a.dockerfileSource === "lps" ? "Dockerfile stored in LPS" : "project Dockerfile"}
              </>
            )}
          </span>
        </div>
        <div className="flex flex-wrap gap-2">
          {running ? (
            <Button variant="secondary" disabled={action.isPending} onClick={() => action.mutate("stop")}>
              <Icon name="stop" />
              Stop
            </Button>
          ) : (
            <Button variant="secondary" disabled={action.isPending || !a.containerId} onClick={() => action.mutate("start")}>
              <Icon name="play" />
              Start
            </Button>
          )}
          <Button disabled={deploying || deploy.isPending} onClick={() => deploy.mutate()}>
            <Icon name="deploy" />
            {deploying ? "Deploying…" : "Redeploy"}
          </Button>
        </div>
      </section>

      <ErrorBox error={deploy.error ?? action.error ?? a.error} />

      {/* Where the app can be reached */}
      <section aria-label="Addresses" className="grid grid-cols-[repeat(auto-fit,minmax(260px,1fr))] overflow-hidden rounded-bp border border-line bg-panel">
        <Endpoint
          label="On this PC"
          href={running ? (a.localUrl ?? url) : undefined}
          text={a.localUrl ?? url}
          alt={a.localUrl ? { href: running ? url : undefined, text: url } : undefined}
        />
        {a.pathUrl || a.remoteUrl ? (
          <Endpoint
            label="Your devices · tailnet"
            href={a.pathUrl ?? a.remoteUrl}
            text={(a.pathUrl ?? a.remoteUrl)!}
            alt={a.pathUrl && a.remoteUrl ? { href: a.remoteUrl, text: a.remoteUrl } : undefined}
            note={
              a.pathUrl
                ? "Tailscale devices only. If the app breaks under /" + a.name + "/ (it expects to run at /), use the port address above."
                : "Tailscale devices only"
            }
          />
        ) : (
          <div className="flex flex-col gap-1 border-line px-4 py-3">
            <span className="label-mono text-[10px] text-ink-faint">Your devices · tailnet</span>
            <span className="text-[13px] text-ink-muted">
              Turn on <Link to="/remote">remote access</Link>
            </span>
          </div>
        )}
        {a.publicUrl ? (
          <Endpoint label="Anyone · public link" href={a.publicUrl} text={a.publicUrl} warn />
        ) : (
          <div className="flex flex-col gap-1 px-4 py-3">
            <span className="label-mono text-[10px] text-ink-faint">Anyone · public link</span>
            <span className="text-[13px] text-ink-muted">Off</span>
          </div>
        )}
      </section>

      {/* Deployments / Dockerfile / Logs */}
      <section className="relative rounded-bp border border-line bg-panel">
        <CornerMarks />
        <Tabs
          label="App views"
          tabs={[
            { id: "deployments", label: "Deployments" },
            { id: "dockerfile", label: inVPS ? "Run setup" : "Dockerfile" },
            { id: "env", label: "Environment" },
            { id: "logs", label: inVPS ? "App logs" : "Container logs" },
          ]}
          value={tab}
          onChange={setTab}
          right={
            tab === "logs" ? (
              <Button variant="secondary" size="sm" onClick={() => logs.refetch()}>
                Refresh
              </Button>
            ) : undefined
          }
        />
        {tab === "deployments" && (
          <div className="flex flex-wrap">
            <ol aria-label="Deployments" className="m-0 max-h-[26rem] min-w-0 flex-[1_1_240px] list-none overflow-y-auto border-r border-line p-0">
              {deployments.data?.length === 0 && <li className="p-4 text-ink-muted">No deployments yet.</li>}
              {deployments.data?.map((d) => {
                const on = shown?.id === d.id;
                return (
                  <li key={d.id}>
                    <button
                      type="button"
                      onClick={() => setSelected(d.id)}
                      aria-current={on}
                      className={`flex w-full cursor-pointer flex-col gap-0.5 border-0 border-b border-l-[3px] border-b-line-soft px-4 py-3 text-left text-ink ${
                        on ? "border-l-cyan bg-cyan/10" : "border-l-transparent bg-transparent hover:bg-white/5"
                      }`}
                    >
                      <span className="flex items-center justify-between gap-2">
                        <b>#{d.id}</b>
                        <StateBadge state={d.status} />
                      </span>
                      <span className="text-xs text-ink-muted">{new Date(d.startedAt).toLocaleString()}</span>
                    </button>
                  </li>
                );
              })}
            </ol>
            <div className="flex min-w-0 flex-[999_1_520px] flex-col gap-2 p-3">
              {shown && (
                <div className="flex flex-wrap justify-between gap-2 font-mono text-[11px] text-ink-faint uppercase">
                  <span>
                    Build log · #{shown.id}
                    {shown.image && <> · {shown.image}</>}
                  </span>
                  <span>{shown.status}</span>
                </div>
              )}
              <LogView text={shown?.log} empty="No deployments yet." className="!max-h-[24rem] min-h-[24rem]" />
            </div>
          </div>
        )}
        {tab === "dockerfile" && (
          <div className="p-4">
            {inVPS ? <RunConfigCard app={a} deploying={deploying} /> : <DockerfileCard appId={a.id} deploying={deploying} bare />}
          </div>
        )}
        {tab === "env" && (
          <div className="p-4">
            <EnvCard appId={a.id} inVPS={inVPS} deploying={deploying} />
          </div>
        )}
        {tab === "logs" && (
          <div className="p-3">
            <ErrorBox error={logs.error} />
            <LogView text={logs.data?.logs} className="!max-h-[26rem] min-h-[26rem]" />
          </div>
        )}
      </section>

      <section className="flex flex-wrap items-start gap-5">
        <div className="min-w-0 flex-[999_1_520px]">
          <UsageCard kind="apps" id={a.id} running={running} note={inVPS ? `Counts the app's own processes inside VPS ${a.vpsName}.` : undefined} />
        </div>
        <Card title="Activity" flush className="flex-[1_1_320px]">
          <ActivityList subject="app" id={a.id} limit={8} showLinks={false} empty="Nothing has happened to this app yet." />
        </Card>
      </section>

      <section className="flex flex-wrap items-start gap-5">
        <Card title="Public link" className="flex-[1_1_380px]">
          {!remote.data?.enabled ? (
            <p className="m-0 text-ink-muted">
              Turn on <Link to="/remote">remote access</Link> to reach this app from your devices, or to share it with a public link.
            </p>
          ) : (
            <div className="flex flex-col gap-3 text-[13px]">
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
              {a.publicPort > 0 && !a.publicUrl && <p className="m-0 text-ink-muted">Setting up the public link…</p>}
              {setPublic.error instanceof ApiError && setPublic.error.needsAction ? (
                <ActionBox message={setPublic.error.needsAction.message} url={setPublic.error.needsAction.url} linkText="How to enable" />
              ) : (
                <ErrorBox error={setPublic.error} />
              )}
              <Callout intent="warning">Uses Tailscale Funnel. A PC can have at most 2 public apps at a time.</Callout>
            </div>
          )}
        </Card>

        <Card title="Danger zone" tone="danger" className="flex-[1_1_300px]">
          <div className="flex flex-col gap-3 text-[13px]">
            <p className="m-0 text-ink-2">
              {inVPS ? "The app is stopped and its relay removed. Its folder in the VPS is kept." : "The container is removed. Your project folder is not touched."}
            </p>
            <Button
              variant="danger"
              className="self-start"
              disabled={del.isPending || deploying}
              onClick={() => {
                if (confirm(`Delete app "${a.name}"? ${inVPS ? "Its folder in the VPS is kept." : "The container is removed; your project folder is not touched."}`)) del.mutate();
              }}
            >
              Delete app
            </Button>
            <ErrorBox error={del.error} />
          </div>
        </Card>
      </section>
    </>
  );
}

function Endpoint({
  label,
  href,
  text,
  note,
  warn,
  alt,
}: {
  label: string;
  href?: string;
  text: string;
  note?: string;
  warn?: boolean;
  /** A second address for the same place (the port-based one). */
  alt?: { href?: string; text: string };
}) {
  return (
    <div className={`flex min-w-0 flex-col gap-1 border-r border-line px-4 py-3 last:border-r-0 ${warn ? "bg-warning/6" : ""}`}>
      <span className={`label-mono text-[10px] ${warn ? "text-warning-ink" : "text-ink-faint"}`}>{label}</span>
      {href ? (
        <a href={href} target="_blank" rel="noreferrer" className={`font-mono text-[13px] break-all ${warn ? "text-warning-ink hover:text-white" : ""}`}>
          {text}
        </a>
      ) : (
        <span className="font-mono text-[13px] break-all text-ink-muted">{text}</span>
      )}
      {alt &&
        (alt.href ? (
          <a href={alt.href} target="_blank" rel="noreferrer" className="font-mono text-[11px] break-all text-ink-muted hover:text-ink">
            {alt.text}
          </a>
        ) : (
          <span className="font-mono text-[11px] break-all text-ink-faint">{alt.text}</span>
        ))}
      {note && <span className="text-[11px] text-ink-faint">{note}</span>}
    </div>
  );
}
