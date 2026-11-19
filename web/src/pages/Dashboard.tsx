import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, type AppItem, type VPS } from "../api";
import { Button, Callout, Card, ErrorBox, Icon, Input, PageHeader, StateBadge, Switch, Tag, TitleBlock, buttonClass, formatLimits } from "../components/ui";

const th = "border-b border-line px-4 py-2.5 text-left font-mono text-[11px] font-medium tracking-[0.08em] text-ink-faint uppercase";
const td = "border-b border-line-soft px-4 py-3 align-middle";

/** Table of VPSes (dashboard and /vps). */
export function VPSTable({ title = "Linux VPSes", action }: { title?: string; action?: React.ReactNode }) {
  const vps = useQuery({ queryKey: ["vps"], queryFn: api.listVPS, refetchInterval: 5000 });
  const [filter, setFilter] = useState("");
  const list = (vps.data ?? []).filter((v) => v.name.includes(filter.trim().toLowerCase()));
  return (
    <Card
      title={title}
      marks
      flush
      actions={
        <>
          <label className="relative flex items-center">
            <span className="sr-only">Filter VPSes</span>
            <span className="pointer-events-none absolute left-2.5 text-ink-faint">
              <Icon name="search" />
            </span>
            <Input type="search" placeholder="Filter…" value={filter} onChange={(e) => setFilter(e.target.value)} className="!h-[30px] w-44 pl-8 text-[13px]" />
          </label>
          {action}
        </>
      }
    >
      <ErrorBox error={vps.error} />
      {vps.data?.length === 0 ? (
        <p className="m-0 p-4 text-ink-muted">
          No VPS yet. A VPS is an Ubuntu 24.04 machine you can SSH into, running in an isolated container on this PC.{" "}
          <Link to="/vps/new">Create one</Link>.
        </p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[720px] border-collapse">
            <thead>
              <tr>
                <th className={th}>Name</th>
                <th className={th}>State</th>
                <th className={th}>SSH</th>
                <th className={th}>Limits</th>
                <th className={th}>Remote</th>
                <th className={th}>
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {list.map((v, i) => (
                <VPSRow key={v.id} v={v} striped={i % 2 === 1} />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Card>
  );
}

function VPSRow({ v, striped }: { v: VPS; striped: boolean }) {
  const qc = useQueryClient();
  const start = useMutation({ mutationFn: () => api.vpsAction(v.id, "start"), onSuccess: () => qc.invalidateQueries({ queryKey: ["vps"] }) });
  return (
    <tr className={striped ? "bg-white/[0.02]" : ""}>
      <td className={td}>
        <Link to={`/vps/${v.id}`} className="font-semibold text-ink no-underline hover:text-cyan-soft">
          {v.name}
        </Link>
        <div className="text-xs text-ink-muted">Ubuntu 24.04 · user {v.sshUser}</div>
      </td>
      <td className={td}>
        <StateBadge state={v.state} />
      </td>
      <td className={`${td} font-mono text-xs`}>:{v.sshPort}</td>
      <td className={`${td} text-[13px] text-ink-2`}>{formatLimits(v.cpus, v.memoryMb)}</td>
      <td className={`${td} text-[13px] ${v.remoteSsh ? "text-cyan-soft" : "text-ink-faint"}`}>{v.remoteSsh ? "tailnet" : "—"}</td>
      <td className={`${td} text-right whitespace-nowrap`}>
        {v.state === "running" ? (
          <Link to={`/vps/${v.id}`} className={buttonClass("secondary", "sm")}>
            <Icon name="terminal" size={13} />
            Terminal
          </Link>
        ) : v.state === "stopped" ? (
          <Button variant="secondary" size="sm" disabled={start.isPending} onClick={() => start.mutate()}>
            <Icon name="play" size={13} />
            Start
          </Button>
        ) : null}
      </td>
    </tr>
  );
}

/** List of apps (dashboard and /apps). */
export function AppList({ title = "Apps", action, limit }: { title?: string; action?: React.ReactNode; limit?: number }) {
  const apps = useQuery({ queryKey: ["apps"], queryFn: api.listApps, refetchInterval: 5000 });
  const list = limit ? (apps.data ?? []).slice(0, limit) : (apps.data ?? []);
  return (
    <Card title={title} flush actions={action}>
      <ErrorBox error={apps.error} />
      {apps.data?.length === 0 && (
        <p className="m-0 p-4 text-ink-muted">
          No apps yet. Point LPS at a project folder and it builds and runs it for you (it writes a Dockerfile if there isn't one).{" "}
          <Link to="/apps/new">Add an app</Link>.
        </p>
      )}
      <ul className="m-0 list-none p-0">
        {list.map((a, i) => (
          <AppRow key={a.id} a={a} last={i === list.length - 1} />
        ))}
      </ul>
    </Card>
  );
}

function AppRow({ a, last }: { a: AppItem; last: boolean }) {
  return (
    <li className={`flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3 ${last ? "" : "border-b border-line-soft"}`}>
      <div className="min-w-0 flex-[1_1_220px]">
        <Link to={`/apps/${a.id}`} className="font-semibold text-ink no-underline hover:text-cyan-soft">
          {a.name}
        </Link>
        <div className="truncate font-mono text-xs text-ink-muted">{a.sourcePath}</div>
      </div>
      {a.publicUrl && <Tag intent="warning">Public</Tag>}
      {a.deploying ? <Tag intent="primary" dot>Building</Tag> : a.lastDeploy?.status === "failed" && a.state !== "running" ? <Tag intent="danger" dot>Build failed</Tag> : <StateBadge state={a.state} />}
      <span className="font-mono text-xs text-ink-2">:{a.hostPort}</span>
    </li>
  );
}

function Stat({ label, children, foot }: { label: string; children: React.ReactNode; foot?: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1 rounded-bp border border-line bg-panel px-4 py-3.5">
      <span className="label-mono text-ink-faint">{label}</span>
      <div className="text-[26px] font-semibold">{children}</div>
      {foot && <div className="text-xs text-ink-muted">{foot}</div>}
    </div>
  );
}

export function Dashboard() {
  const system = useQuery({ queryKey: ["system"], queryFn: api.system, refetchInterval: 15000 });
  const vps = useQuery({ queryKey: ["vps"], queryFn: api.listVPS, refetchInterval: 5000 });
  const apps = useQuery({ queryKey: ["apps"], queryFn: api.listApps, refetchInterval: 5000 });
  const remote = useQuery({ queryKey: ["remote"], queryFn: api.remote, refetchInterval: 15000 });
  const docker = system.data?.docker;

  const vpsRunning = vps.data?.filter((v) => v.state === "running").length ?? 0;
  const appsRunning = apps.data?.filter((a) => a.state === "running").length ?? 0;
  const appsPublic = apps.data?.filter((a) => a.publicUrl).length ?? 0;
  const r = remote.data;

  return (
    <>
      <PageHeader
        sheet="Sheet 01 — Dashboard"
        title="Home server overview"
        actions={
          <>
            <Link to="/apps/new" className={buttonClass("secondary")}>
              <Icon name="app" />
              New app
            </Link>
            <Link to="/vps/new" className={buttonClass("primary")}>
              <Icon name="plus" />
              New VPS
            </Link>
          </>
        }
      />

      {docker && !docker.ok && (
        <Callout intent="warning">
          <b>Docker is not reachable.</b> {docker.hint}
          <div className="mt-1 font-mono text-xs opacity-75">{docker.error}</div>
        </Callout>
      )}

      <section aria-label="Summary" className="grid grid-cols-[repeat(auto-fit,minmax(230px,1fr))] gap-3">
        <Stat label="VPSes" foot={vps.data?.length ? `SSH ports ${vps.data.map((v) => v.sshPort).join(", ")}` : "None yet"}>
          {vpsRunning} <span className="text-sm font-normal text-ink-muted">running of {vps.data?.length ?? 0}</span>
        </Stat>
        <Stat label="Apps" foot={appsPublic ? `${appsPublic} with a public link` : "Private to this PC and your devices"}>
          {appsRunning} <span className="text-sm font-normal text-ink-muted">running of {apps.data?.length ?? 0}</span>
        </Stat>
        <div className="flex flex-col gap-1 rounded-bp border border-line bg-panel px-4 py-3.5">
          <span className="label-mono text-ink-faint">Remote access</span>
          <span className="pt-1.5 text-base font-semibold">{r?.available ? (r.enabled ? "On · Tailscale" : "Off") : "Unavailable"}</span>
          {r?.dashboardUrl ? (
            <a href={r.dashboardUrl} className="font-mono text-xs break-all">
              {r.dashboardUrl.replace("https://", "")}
            </a>
          ) : (
            <Link to="/remote" className="text-xs">
              {r?.enabled ? "Needs attention" : "Set up"}
            </Link>
          )}
        </div>
        <Stat label="Docker" foot={docker?.ok ? `${system.data?.os}/${system.data?.arch}` : docker?.hint}>
          <span className="text-base">{docker ? (docker.ok ? `Running · ${docker.version}` : "Not reachable") : "…"}</span>
        </Stat>
      </section>

      <VPSTable />
      <AppList
        limit={6}
        action={
          <Link to="/apps" className="text-[13px]">
            View all
          </Link>
        }
      />

      <div className="flex flex-wrap items-center justify-between gap-4">
        <AutostartToggle />
        <TitleBlock cells={[`LPS ${system.data?.version ?? ""}`, `${system.data?.os ?? ""}/${system.data?.arch ?? ""}`.toUpperCase(), "Sheet 01"]} />
      </div>
    </>
  );
}

export function VPSListPage() {
  return (
    <>
      <PageHeader
        sheet="Sheet 02 — VPSes"
        title="Linux VPSes"
        subtitle="Ubuntu machines you SSH into, each in its own container with CPU/RAM limits and a persistent /home."
        actions={
          <Link to="/vps/new" className={buttonClass("primary")}>
            <Icon name="plus" />
            New VPS
          </Link>
        }
      />
      <VPSTable title="All VPSes" />
    </>
  );
}

export function AppListPage() {
  return (
    <>
      <PageHeader
        sheet="Sheet 03 — Apps"
        title="Apps"
        subtitle="Project folders built and run as containers. Redeploy any time; a failed build leaves the running version alone."
        actions={
          <Link to="/apps/new" className={buttonClass("primary")}>
            <Icon name="plus" />
            New app
          </Link>
        }
      />
      <AppList title="All apps" />
    </>
  );
}

function AutostartToggle() {
  const qc = useQueryClient();
  const auto = useQuery({ queryKey: ["autostart"], queryFn: api.autostart });
  const set = useMutation({ mutationFn: api.setAutostart, onSuccess: (v) => qc.setQueryData(["autostart"], v) });
  if (!auto.data?.supported) return <span />;
  return (
    <div className="flex items-center gap-2 text-[13px] text-ink-muted">
      <Switch label="Start with Windows" checked={auto.data.enabled} disabled={set.isPending} onChange={(v) => set.mutate(v)} />
      Start LPS when I sign in to Windows
    </div>
  );
}
