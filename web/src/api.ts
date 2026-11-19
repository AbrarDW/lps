// Typed client for the lpsd REST API.

export type State = "running" | "stopped" | "missing" | "provisioning" | "error";

export interface User { id: number; username: string; createdAt: string }

export interface VPS {
  id: number;
  name: string;
  sshUser: string;
  sshPort: number;
  sshPublicKey: string;
  cpus: number;
  memoryMb: number;
  containerId: string;
  createdAt: string;
  state: State;
  error?: string;
  provisionLog?: string;
  remoteSsh?: string;
  apps: { id: number; name: string; hostPort: number }[];
}

export interface Deployment {
  id: number;
  appId: number;
  status: "building" | "running" | "failed" | "replaced";
  image: string;
  log: string;
  startedAt: string;
  finishedAt: string | null;
}

export interface AppItem {
  id: number;
  name: string;
  sourcePath: string;
  containerPort: number;
  hostPort: number;
  cpus: number;
  memoryMb: number;
  containerId: string;
  createdAt: string;
  state: State;
  error?: string;
  deploying: boolean;
  lastDeploy?: Deployment;
  publicPort: number;
  remoteUrl?: string;
  publicUrl?: string;
  dockerfile: string;
  dockerfileSource: "lps" | "project" | "none";
  /** 0 = the app runs in its own container. */
  vpsId: number;
  vpsName?: string;
  sourceKind: "pc" | "vps";
  vpsPath: string;
  packages: string;
  installCmd: string;
  startCmd: string;
  wantRunning: boolean;
}

/** How to install and start an app directly inside a VPS. */
export interface RunSetup {
  packages: string;
  install: string;
  start: string;
  port: number;
  notes: string[] | null;
}

export interface Detection {
  type: string;
  label: string;
  port: number;
  dockerfile: string;
  notes: string[] | null;
  run: RunSetup;
}

export interface DetectResult {
  hasDockerfile: boolean;
  projectDockerfile?: string;
  detection: Detection;
  templates: Detection[];
}

export interface AppDockerfile {
  source: "lps" | "project";
  dockerfile: string;
  projectHasDockerfile: boolean;
  suggested?: Detection;
}

export interface ActivityEvent {
  id: number;
  at: string;
  level: "info" | "warning" | "error";
  kind: string;
  subject: "vps" | "app" | "remote" | "auth" | "";
  subjectId: number;
  name: string;
  actor: string;
  ip: string;
  message: string;
}

export type ActivitySubject = "" | "vps" | "app" | "remote" | "auth";

export interface StatPoint { t: number; cpu: number; mem: number }

/** Live usage of one VPS or app. cpu is % of one core; cpuLimit is in cores. */
export interface UsageStat { cpu: number; cpuLimit: number; mem: number; memLimit: number; points: StatPoint[] }

export interface Stats {
  host: { cpus: number; memBytes: number };
  intervalMs: number;
  vps: Record<string, UsageStat>;
  apps: Record<string, UsageStat>;
}

export interface NeedsAction { message: string; url?: string }

export interface TunnelStatus {
  installed: boolean;
  running: boolean;
  state: string;
  dnsName: string;
  hostName: string;
  tailnet: string;
  httpsEnabled: boolean;
  funnelEnabled: boolean;
  funnelPorts: number[] | null;
}

export interface ExposureView {
  proto: "https" | "tcp";
  port: number;
  localPort: number;
  public: boolean;
  label: string;
  url: string;
  state: "active" | "pending" | "error";
  error?: string;
}

export interface RemoteView {
  provider: string;
  available: boolean;
  enabled: boolean;
  status: TunnelStatus;
  dashboardUrl?: string;
  needsAction?: NeedsAction;
  error?: string;
  lastSync?: string;
  exposures: ExposureView[];
}

export interface SystemInfo {
  version: string;
  os: string;
  arch: string;
  bindIp: string;
  docker: { ok: boolean; version?: string; error?: string; hint?: string };
}

export interface SetupCheck {
  id: string;
  label: string;
  status: "ok" | "warn" | "fail";
  detail?: string;
  required: boolean;
  fixes?: { kind: "link" | "command" | "action"; label: string; value: string }[];
}

export interface SetupChecks { os: string; ready: boolean; checks: SetupCheck[] }

export interface AutostartState { supported: boolean; enabled: boolean; error?: string }

export class ApiError extends Error {
  constructor(public status: number, message: string, public needsAction?: NeedsAction) {
    super(message);
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(`/api${path}`, {
    method,
    credentials: "same-origin",
    headers: {
      "X-LPS-Request": "1", // CSRF guard, required by lpsd for writes
      ...(body !== undefined ? { "Content-Type": "application/json" } : {}),
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(res.status, data.error ?? res.statusText, data.needsAction);
  return data as T;
}

export const api = {
  setupStatus: () => request<{ needsSetup: boolean }>("GET", "/setup"),
  setup: (username: string, password: string) => request<User>("POST", "/setup", { username, password }),
  login: (username: string, password: string) => request<User>("POST", "/auth/login", { username, password }),
  logout: () => request<void>("POST", "/auth/logout"),
  me: () => request<User>("GET", "/auth/me"),
  system: () => request<SystemInfo>("GET", "/system"),

  listVPS: () => request<VPS[]>("GET", "/vps"),
  getVPS: (id: number) => request<VPS>("GET", `/vps/${id}`),
  createVPS: (v: {
    name: string; sshUser: string; sshPublicKey: string; password: string; cpus: number; memoryMb: number;
  }) => request<VPS>("POST", "/vps", v),
  vpsAction: (id: number, action: "start" | "stop") => request<VPS>("POST", `/vps/${id}/${action}`),
  deleteVPS: (id: number, keepData: boolean) => request<void>("DELETE", `/vps/${id}${keepData ? "?keepData=true" : ""}`),
  vpsLogs: (id: number) => request<{ logs: string }>("GET", `/vps/${id}/logs?tail=300`),

  listApps: () => request<AppItem[]>("GET", "/apps"),
  getApp: (id: number) => request<AppItem>("GET", `/apps/${id}`),
  createApp: (a: {
    name: string;
    sourcePath: string;
    containerPort: number;
    cpus: number;
    memoryMb: number;
    dockerfile?: string;
    saveToProject?: boolean;
    vpsId?: number;
    sourceKind?: "pc" | "vps";
    vpsPath?: string;
    packages?: string;
    installCmd?: string;
    startCmd?: string;
  }) => request<AppItem>("POST", "/apps", a),
  appTemplates: () => request<Detection[]>("GET", "/apps/templates"),
  setAppRunConfig: (id: number, c: { packages: string; installCmd: string; startCmd: string }) =>
    request<AppItem>("PUT", `/apps/${id}/run-config`, c),
  detectApp: (sourcePath: string) => request<DetectResult>("POST", "/apps/detect", { sourcePath }),
  appDockerfile: (id: number) => request<AppDockerfile>("GET", `/apps/${id}/dockerfile`),
  setAppDockerfile: (id: number, dockerfile: string) => request<AppItem>("PUT", `/apps/${id}/dockerfile`, { dockerfile }),
  deployApp: (id: number) => request<Deployment>("POST", `/apps/${id}/deploy`),
  appAction: (id: number, action: "start" | "stop") => request<AppItem>("POST", `/apps/${id}/${action}`),
  deleteApp: (id: number) => request<void>("DELETE", `/apps/${id}`),
  appLogs: (id: number) => request<{ logs: string }>("GET", `/apps/${id}/logs?tail=300`),
  deployments: (id: number) => request<Deployment[]>("GET", `/apps/${id}/deployments`),
  setAppPublic: (id: number, isPublic: boolean) => request<AppItem>("PUT", `/apps/${id}/public`, { public: isPublic }),

  setupChecks: () => request<SetupChecks>("GET", "/setup/checks"),
  setupAction: (action: string) => request<{ status: string }>("POST", `/setup/actions/${action}`),
  autostart: () => request<AutostartState>("GET", "/system/autostart"),
  setAutostart: (enabled: boolean) => request<AutostartState>("PUT", "/system/autostart", { enabled }),

  events: (q: { subject?: ActivitySubject; id?: number; before?: number; limit?: number } = {}) => {
    const p = new URLSearchParams();
    if (q.subject) p.set("subject", q.subject);
    if (q.id) p.set("id", String(q.id));
    if (q.before) p.set("before", String(q.before));
    if (q.limit) p.set("limit", String(q.limit));
    return request<ActivityEvent[]>("GET", `/events?${p}`);
  },
  stats: () => request<Stats>("GET", "/stats"),

  remote: () => request<RemoteView>("GET", "/remote"),
  setRemote: (enabled: boolean) => request<RemoteView>("PUT", "/remote", { enabled }),
  syncRemote: () => request<RemoteView>("POST", "/remote/sync"),
};

/** Host to show in SSH commands / URLs: the dashboard's host when ports are published beyond localhost. */
export function displayHost(bindIp: string | undefined): string {
  if (!bindIp || bindIp === "127.0.0.1" || bindIp === "::1") return "localhost";
  if (bindIp === "0.0.0.0" || bindIp === "::") return window.location.hostname;
  return bindIp;
}
