import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api, ApiError, displayHost, type AppItem } from "../api";
import { DockerfileCard } from "../components/DockerfileCard";
import { RunConfigCard } from "../components/RunConfigCard";
import { EnvCard } from "../components/EnvCard";
import { GitCard } from "../components/GitCard";
import { ActivityList } from "../components/Activity";
import { UsageCard } from "../components/Usage";
import {
  ActionBox,
  Button,
  Card,
  CopyField,
  CornerMarks,
  ErrorBox,
  Icon,
  Input,
  LogView,
  StateBadge,
  Switch,
  Tabs,
  Tag,
  formatLimits,
} from "../components/ui";

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
    refetchInterval: (q) => {
      const d = q.state.data;
      return d?.deploying || (d?.quickLink && d.quickState !== "active")
        ? 1500
        : 5000;
    },
  });
  const deploying = app.data?.deploying;
  const deployments = useQuery({
    queryKey: ["apps", id, "deployments"],
    queryFn: () => api.deployments(id),
    refetchInterval: deploying ? 1500 : 5000,
  });
  const logs = useQuery({
    queryKey: ["apps", id, "logs"],
    queryFn: () => api.appLogs(id),
    enabled: !!app.data?.containerId && tab === "logs",
  });

  const remote = useQuery({ queryKey: ["remote"], queryFn: api.remote });
  const refresh = () => qc.invalidateQueries({ queryKey: ["apps"] });
  const setPublic = useMutation({
    mutationFn: (p: boolean) => api.setAppPublic(id, p),
    onSuccess: () => {
      refresh();
      qc.invalidateQueries({ queryKey: ["remote"] });
    },
  });
  const setQuick = useMutation({
    mutationFn: (on: boolean) => api.setQuickLink(id, on),
    onSuccess: (x) => {
      qc.setQueryData(["apps", id], x);
      refresh();
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
  const action = useMutation({
    mutationFn: (a: "start" | "stop") => api.appAction(id, a),
    onSuccess: refresh,
  });
  const del = useMutation({
    mutationFn: () => api.deleteApp(id),
    onSuccess: () => {
      qc.removeQueries({ queryKey: ["apps", id] });
      refresh();
      nav("/apps");
    },
  });

  if (app.isLoading)
    return <p className="label-mono text-ink-faint">Loading…</p>;
  if (app.error || !app.data)
    return <ErrorBox error={app.error ?? "not found"} />;
  const a = app.data;
  const url = `http://${displayHost(system.data?.bindIp)}:${a.hostPort}`;
  const shown =
    deployments.data?.find((d) => d.id === selected) ?? deployments.data?.[0];
  const running = a.state === "running";
  const inVPS = a.vpsId > 0;
  const domainUrl = a.domain ? `https://${a.domain}` : undefined;
  const publicLinks = [domainUrl, a.quickUrl, a.publicUrl].filter(
    Boolean,
  ) as string[];
  const source =
    a.sourceKind === "git"
      ? `${a.gitUrl} (${a.gitBranch || "default branch"}${a.lastCommit ? ` @ ${a.lastCommit.slice(0, 7)}` : ""})`
      : a.sourceKind === "vps"
        ? `${a.vpsName}:${a.vpsPath}`
        : a.sourcePath;

  return (
    <>
      <nav
        aria-label="Breadcrumb"
        className="flex gap-2 font-mono text-xs text-ink-faint uppercase"
      >
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
            {(a.publicUrl || a.quickUrl || a.domain) && (
              <Tag intent="warning">Public</Tag>
            )}
            {inVPS && (
              <Link to={`/vps/${a.vpsId}`} className="no-underline">
                <Tag intent="primary">VPS {a.vpsName}</Tag>
              </Link>
            )}
          </div>
          <span className="font-mono text-xs break-all text-ink-faint">
            {inVPS ? (
              <>
                {source} · port {a.containerPort} in the VPS ·{" "}
                <span className="normal-case">{a.startCmd}</span>
              </>
            ) : (
              <>
                {source} · container port {a.containerPort} ·{" "}
                {formatLimits(a.cpus, a.memoryMb)} ·{" "}
                {a.dockerfileSource === "lps"
                  ? "Dockerfile stored in LPS"
                  : "project Dockerfile"}
              </>
            )}
          </span>
        </div>
        <div className="flex flex-wrap gap-2">
          {running ? (
            <Button
              variant="secondary"
              disabled={action.isPending}
              onClick={() => action.mutate("stop")}
            >
              <Icon name="stop" />
              Stop
            </Button>
          ) : (
            <Button
              variant="secondary"
              disabled={action.isPending || !a.containerId}
              onClick={() => action.mutate("start")}
            >
              <Icon name="play" />
              Start
            </Button>
          )}
          <Button
            disabled={deploying || deploy.isPending}
            onClick={() => deploy.mutate()}
          >
            <Icon name="deploy" />
            {deploying ? "Deploying…" : "Redeploy"}
          </Button>
        </div>
      </section>

      <ErrorBox error={deploy.error ?? action.error ?? a.error} />

      {/* Where the app can be reached */}
      <section
        aria-label="Addresses"
        className="grid grid-cols-[repeat(auto-fit,minmax(260px,1fr))] overflow-hidden rounded-bp border border-line bg-panel"
      >
        <Endpoint
          label="On this PC"
          href={running ? (a.localUrl ?? url) : undefined}
          text={a.localUrl ?? url}
          alt={
            a.localUrl
              ? { href: running ? url : undefined, text: url }
              : undefined
          }
        />
        {a.pathUrl || a.remoteUrl ? (
          <Endpoint
            label="Your devices · tailnet"
            href={a.pathUrl ?? a.remoteUrl}
            text={(a.pathUrl ?? a.remoteUrl)!}
            alt={
              a.pathUrl && a.remoteUrl
                ? { href: a.remoteUrl, text: a.remoteUrl }
                : undefined
            }
            note={
              a.pathUrl
                ? "Tailscale devices only. If the app breaks under /" +
                  a.name +
                  "/ (it expects to run at /), use the port address above."
                : "Tailscale devices only"
            }
          />
        ) : (
          <div className="flex flex-col gap-1 border-line px-4 py-3">
            <span className="label-mono text-[10px] text-ink-faint">
              Your devices · tailnet
            </span>
            <span className="text-[13px] text-ink-muted">
              Turn on <Link to="/remote">remote access</Link>
            </span>
          </div>
        )}
        {domainUrl || a.quickUrl || a.publicUrl ? (
          <Endpoint
            label="Anyone · public link"
            href={publicLinks[0]}
            text={publicLinks[0]}
            alt={
              publicLinks[1]
                ? { href: publicLinks[1], text: publicLinks[1] }
                : undefined
            }
            warn
          />
        ) : (
          <div className="flex flex-col gap-1 px-4 py-3">
            <span className="label-mono text-[10px] text-ink-faint">
              Anyone · public link
            </span>
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
              <Button
                variant="secondary"
                size="sm"
                onClick={() => logs.refetch()}
              >
                Refresh
              </Button>
            ) : undefined
          }
        />
        {tab === "deployments" && (
          <div className="flex flex-wrap">
            <ol
              aria-label="Deployments"
              className="m-0 max-h-[26rem] min-w-0 flex-[1_1_240px] list-none overflow-y-auto border-r border-line p-0"
            >
              {deployments.data?.length === 0 && (
                <li className="p-4 text-ink-muted">No deployments yet.</li>
              )}
              {deployments.data?.map((d) => {
                const on = shown?.id === d.id;
                return (
                  <li key={d.id}>
                    <button
                      type="button"
                      onClick={() => setSelected(d.id)}
                      aria-current={on}
                      className={`flex w-full cursor-pointer flex-col gap-0.5 border-0 border-b border-l-[3px] border-b-line-soft px-4 py-3 text-left text-ink ${
                        on
                          ? "border-l-cyan bg-cyan/10"
                          : "border-l-transparent bg-transparent hover:bg-white/5"
                      }`}
                    >
                      <span className="flex items-center justify-between gap-2">
                        <b>#{d.id}</b>
                        <StateBadge state={d.status} />
                      </span>
                      <span className="text-xs text-ink-muted">
                        {new Date(d.startedAt).toLocaleString()}
                      </span>
                      {d.commitHash && (
                        <span
                          className="truncate text-xs text-ink-2"
                          title={d.commitMessage}
                        >
                          <code>{d.commitHash.slice(0, 7)}</code>{" "}
                          {d.commitMessage}
                        </span>
                      )}
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
              <LogView
                text={shown?.log}
                empty="No deployments yet."
                className="!max-h-[24rem] min-h-[24rem]"
              />
            </div>
          </div>
        )}
        {tab === "dockerfile" && (
          <div className="p-4">
            {inVPS ? (
              <RunConfigCard app={a} deploying={deploying} />
            ) : (
              <DockerfileCard
                appId={a.id}
                deploying={deploying}
                bare
                fromGit={a.sourceKind === "git"}
              />
            )}
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
            <LogView
              text={logs.data?.logs}
              className="!max-h-[26rem] min-h-[26rem]"
            />
          </div>
        )}
      </section>

      <section className="flex flex-wrap items-start gap-5">
        <div className="min-w-0 flex-[999_1_520px]">
          <UsageCard
            kind="apps"
            id={a.id}
            running={running}
            note={
              inVPS
                ? `Counts the app's own processes inside VPS ${a.vpsName}.`
                : undefined
            }
          />
        </div>
        <Card title="Activity" flush className="flex-[1_1_320px]">
          <ActivityList
            subject="app"
            id={a.id}
            limit={8}
            showLinks={false}
            empty="Nothing has happened to this app yet."
          />
        </Card>
      </section>

      <section className="flex flex-wrap items-start gap-5">
        {a.sourceKind === "git" && (
          <GitCard app={a} className="flex-[1_1_380px]" />
        )}
        <Card title="Public link" className="flex-[1_1_380px]">
          <div className="flex flex-col gap-5 text-[13px]">
            <DomainSection app={a} />
            <QuickLinkSection
              app={a}
              pending={setQuick.isPending}
              error={setQuick.error}
              onChange={(v) => setQuick.mutate(v)}
            />
            <div className="flex flex-col gap-3 border-t border-line-soft pt-4">
              <span className="label-mono text-[10px] text-ink-faint">
                Tailscale Funnel · fixed address
              </span>
              {!remote.data?.enabled ? (
                <p className="m-0 text-ink-muted">
                  Turn on <Link to="/remote">remote access</Link> to reach this
                  app from your devices, or to share it at a fixed address on
                  your Tailscale name.
                </p>
              ) : (
                <>
                  <div className="flex items-center gap-3">
                    <Switch
                      label="Tailscale public link"
                      checked={a.publicPort > 0}
                      disabled={setPublic.isPending}
                      onChange={(v) => {
                        if (
                          !v ||
                          confirm(
                            "Anyone on the internet who has the link will be able to open this app. Continue?",
                          )
                        ) {
                          setPublic.mutate(v);
                        }
                      }}
                    />
                    <span>
                      {a.publicPort > 0
                        ? "Anyone with the link can open this app"
                        : "Only your Tailscale devices can open this app"}
                    </span>
                  </div>
                  {a.publicPort > 0 && !a.publicUrl && (
                    <p className="m-0 text-ink-muted">
                      Setting up the public link…
                    </p>
                  )}
                  {setPublic.error instanceof ApiError &&
                  setPublic.error.needsAction ? (
                    <ActionBox
                      message={setPublic.error.needsAction.message}
                      url={setPublic.error.needsAction.url}
                      linkText="How to enable"
                    />
                  ) : (
                    <ErrorBox error={setPublic.error} />
                  )}
                  <span className="text-xs text-ink-muted">
                    Uses Tailscale Funnel. A PC can have at most 2 public apps
                    at a time.
                  </span>
                </>
              )}
            </div>
          </div>
        </Card>

        <Card title="Danger zone" tone="danger" className="flex-[1_1_300px]">
          <div className="flex flex-col gap-3 text-[13px]">
            <p className="m-0 text-ink-2">
              {inVPS
                ? "The app is stopped and its relay removed. Its folder in the VPS is kept."
                : a.sourceKind === "git"
                  ? "The container and LPS's copy of the repository are removed. The repository itself is not touched."
                  : "The container is removed. Your project folder is not touched."}
            </p>
            <Button
              variant="danger"
              className="self-start"
              disabled={del.isPending || deploying}
              onClick={() => {
                if (
                  confirm(
                    `Delete app "${a.name}"? ${inVPS ? "Its folder in the VPS is kept." : "The container is removed; your project folder is not touched."}`,
                  )
                )
                  del.mutate();
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
    <div
      className={`flex min-w-0 flex-col gap-1 border-r border-line px-4 py-3 last:border-r-0 ${warn ? "bg-warning/6" : ""}`}
    >
      <span
        className={`label-mono text-[10px] ${warn ? "text-warning-ink" : "text-ink-faint"}`}
      >
        {label}
      </span>
      {href ? (
        <a
          href={href}
          target="_blank"
          rel="noreferrer"
          className={`font-mono text-[13px] break-all ${warn ? "text-warning-ink hover:text-white" : ""}`}
        >
          {text}
        </a>
      ) : (
        <span className="font-mono text-[13px] break-all text-ink-muted">
          {text}
        </span>
      )}
      {alt &&
        (alt.href ? (
          <a
            href={alt.href}
            target="_blank"
            rel="noreferrer"
            className="font-mono text-[11px] break-all text-ink-muted hover:text-ink"
          >
            {alt.text}
          </a>
        ) : (
          <span className="font-mono text-[11px] break-all text-ink-faint">
            {alt.text}
          </span>
        ))}
      {note && <span className="text-[11px] text-ink-faint">{note}</span>}
    </div>
  );
}

/** Cloudflare quick link: a public trycloudflare.com address, no account needed. */
function QuickLinkSection({
  app: a,
  pending,
  error,
  onChange,
}: {
  app: AppItem;
  pending: boolean;
  error: unknown;
  onChange: (on: boolean) => void;
}) {
  return (
    <div className="flex flex-col gap-3">
      <span className="label-mono text-[10px] text-ink-faint">
        Cloudflare quick link · no account needed
      </span>
      <div className="flex items-center gap-3">
        <Switch
          label="Cloudflare quick link"
          checked={a.quickLink}
          disabled={pending}
          onChange={(v) => {
            if (
              !v ||
              confirm(
                "Anyone on the internet who has the link will be able to open this app. Continue?",
              )
            )
              onChange(v);
          }}
        />
        <span>
          {a.quickLink ? "Anyone with the link can open this app" : "Off"}
        </span>
      </div>
      {a.quickLink && a.quickState === "active" && a.quickUrl && (
        <div className="flex flex-col gap-1.5">
          <CopyField value={a.quickUrl} />
          <a
            href={a.quickUrl}
            target="_blank"
            rel="noreferrer"
            className="self-start text-xs"
          >
            Open
          </a>
          {a.state !== "running" && (
            <span className="text-xs text-warning-ink">
              The app isn't running, so the link shows an error page until it
              starts.
            </span>
          )}
        </div>
      )}
      {a.quickLink && a.quickState === "starting" && (
        <p className="m-0 text-ink-muted">
          {a.quickError ===
          "the link starts after the app's first successful deploy"
            ? "The link starts after the app's first successful deploy."
            : "Getting an address from Cloudflare… The first time, Docker also downloads cloudflared, which can take a minute."}
        </p>
      )}
      {a.quickLink && a.quickState === "error" && (
        <ErrorBox error={a.quickError} />
      )}
      {a.quickLink &&
        a.quickState === "starting" &&
        a.quickError &&
        a.quickError !==
          "the link starts after the app's first successful deploy" && (
          <span className="text-xs text-warning-ink">{a.quickError}</span>
        )}
      <ErrorBox error={error} />
      <span className="text-xs text-ink-muted">
        A random <code>https://….trycloudflare.com</code> address. Nothing to
        set up, and no ports open on this PC. The address changes when the link
        is turned off and on, or after Docker or this PC restarts. Meant for
        sharing and testing: Cloudflare gives no uptime guarantee.
      </span>
    </div>
  );
}

/** The app's address on the user's own domain (Cloudflare Tunnel). */
function DomainSection({ app: a }: { app: AppItem }) {
  const qc = useQueryClient();
  const cf = useQuery({ queryKey: ["cloudflare"], queryFn: api.cloudflare });
  const [host, setHost] = useState("");
  const done = (x: AppItem) => {
    qc.setQueryData(["apps", a.id], x);
    qc.invalidateQueries({ queryKey: ["apps"] });
    qc.invalidateQueries({ queryKey: ["cloudflare"] });
    setHost("");
  };
  const set = useMutation({
    mutationFn: () => api.setAppDomain(a.id, host),
    onSuccess: done,
  });
  const remove = useMutation({
    mutationFn: () => api.removeAppDomain(a.id),
    onSuccess: done,
  });
  const zones = cf.data?.zones ?? [];
  return (
    <div className="flex flex-col gap-3">
      <span className="label-mono text-[10px] text-ink-faint">
        Your domain · Cloudflare
      </span>
      {!cf.data?.connected && !a.domain ? (
        <p className="m-0 text-ink-muted">
          Give this app an address like <code>blog.yourdomain.com</code>:{" "}
          <Link to="/remote">connect your Cloudflare account</Link> first.
        </p>
      ) : (
        <>
          {a.domain && (
            <div className="flex flex-wrap items-center gap-3">
              <a
                href={`https://${a.domain}`}
                target="_blank"
                rel="noreferrer"
                className="font-mono break-all"
              >
                https://{a.domain}
              </a>
              <Button
                variant="minimal"
                size="sm"
                disabled={remove.isPending}
                onClick={() => {
                  if (
                    confirm(
                      `Remove https://${a.domain}? The address stops working and LPS deletes its DNS record.`,
                    )
                  )
                    remove.mutate();
                }}
              >
                Remove
              </Button>
            </div>
          )}
          {cf.data?.connected && (
            <form
              className="flex flex-col gap-1.5"
              onSubmit={(e) => {
                e.preventDefault();
                if (
                  confirm(
                    `Anyone on the internet will be able to open this app at https://${host.trim()}. Continue?`,
                  )
                )
                  set.mutate();
              }}
            >
              <div className="flex flex-wrap gap-2">
                <Input
                  aria-label="Address on your domain"
                  value={host}
                  onChange={(e) => setHost(e.target.value)}
                  placeholder={
                    zones.length
                      ? `${a.name}.${zones[0].name}`
                      : "blog.example.com"
                  }
                  list={`zones-${a.id}`}
                  spellCheck={false}
                  className="min-w-0 flex-[1_1_220px] font-mono"
                />
                <datalist id={`zones-${a.id}`}>
                  {zones.map((z) => (
                    <option key={z.id} value={`${a.name}.${z.name}`} />
                  ))}
                </datalist>
                <Button
                  type="submit"
                  size="sm"
                  disabled={set.isPending || !host.trim()}
                >
                  {set.isPending
                    ? "Saving…"
                    : a.domain
                      ? "Change address"
                      : "Use this address"}
                </Button>
              </div>
              <span className="text-xs text-ink-muted">
                Any name on{" "}
                {zones.length
                  ? zones.map((z) => z.name).join(", ")
                  : "your Cloudflare domains"}
                . LPS adds the DNS record; HTTPS works within a minute.
              </span>
            </form>
          )}
          <ErrorBox error={set.error ?? remove.error} />
        </>
      )}
    </div>
  );
}
