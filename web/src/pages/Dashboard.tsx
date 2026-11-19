import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api } from "../api";
import { Card, ErrorBox, StateBadge, formatLimits } from "../components/ui";

const linkBtn = "rounded-lg bg-indigo-600 px-3.5 py-2 text-sm font-medium text-white hover:bg-indigo-500";

export function Dashboard() {
  const system = useQuery({ queryKey: ["system"], queryFn: api.system, refetchInterval: 15000 });
  const vps = useQuery({ queryKey: ["vps"], queryFn: api.listVPS, refetchInterval: 5000 });
  const apps = useQuery({ queryKey: ["apps"], queryFn: api.listApps, refetchInterval: 5000 });
  const docker = system.data?.docker;

  return (
    <>
      {docker && !docker.ok && (
        <div className="rounded-xl border border-amber-300 bg-amber-50 p-4 text-sm text-amber-900 dark:border-amber-800 dark:bg-amber-950/40 dark:text-amber-200">
          <p className="font-semibold">Docker is not reachable</p>
          <p className="mt-1">{docker.hint}</p>
          <p className="mt-1 font-mono text-xs opacity-75">{docker.error}</p>
        </div>
      )}

      <Card title="Linux VPSes" actions={<Link to="/vps/new" className={linkBtn}>New VPS</Link>}>
        <ErrorBox error={vps.error} />
        {vps.data?.length === 0 && (
          <p className="text-sm text-slate-500 dark:text-slate-400">
            No VPS yet. A VPS is an Ubuntu 24.04 machine you can SSH into, running in an isolated container on this PC.
          </p>
        )}
        <ul className="divide-y divide-slate-200 dark:divide-slate-800">
          {vps.data?.map((v) => (
            <li key={v.id}>
              <Link to={`/vps/${v.id}`} className="flex flex-wrap items-center justify-between gap-2 py-3 hover:opacity-80">
                <div>
                  <div className="font-medium">{v.name}</div>
                  <div className="text-xs text-slate-500 dark:text-slate-400">
                    user {v.sshUser} · SSH port {v.sshPort} · {formatLimits(v.cpus, v.memoryMb)}
                  </div>
                </div>
                <StateBadge state={v.state} />
              </Link>
            </li>
          ))}
        </ul>
      </Card>

      <Card title="Apps" actions={<Link to="/apps/new" className={linkBtn}>New app</Link>}>
        <ErrorBox error={apps.error} />
        {apps.data?.length === 0 && (
          <p className="text-sm text-slate-500 dark:text-slate-400">
            No apps yet. Point LPS at a project folder with a Dockerfile and it builds and runs it for you.
          </p>
        )}
        <ul className="divide-y divide-slate-200 dark:divide-slate-800">
          {apps.data?.map((a) => (
            <li key={a.id}>
              <Link to={`/apps/${a.id}`} className="flex flex-wrap items-center justify-between gap-2 py-3 hover:opacity-80">
                <div className="min-w-0">
                  <div className="font-medium">{a.name}</div>
                  <div className="truncate text-xs text-slate-500 dark:text-slate-400">
                    {a.sourcePath} · port {a.hostPort}
                  </div>
                </div>
                <StateBadge state={a.deploying ? "building" : a.state} />
              </Link>
            </li>
          ))}
        </ul>
      </Card>

      {system.data && (
        <p className="text-center text-xs text-slate-500 dark:text-slate-400">
          lpsd {system.data.version} · {system.data.os}/{system.data.arch}
          {docker?.ok && <> · Docker {docker.version}</>}
        </p>
      )}
    </>
  );
}
