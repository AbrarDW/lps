// Typed client for the lpsd REST API.

export type State = "running" | "stopped" | "missing" | "provisioning" | "restoring" | "error";

export interface User { id: number; username: string; createdAt: string; twoFactor?: boolean }

/** Password accepted, but a two-factor code is needed to finish signing in. */
export interface TwoFactorChallenge { twoFactorRequired: true; ticket: string }

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
  commitHash?: string;
  commitMessage?: string;
}

export interface GitCommit { hash: string; message: string; author: string; when: string; branch: string }

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
  /** Name-based address on this PC: http://<app>.localhost:7070 */
  localUrl?: string;
  /** Name-based address on the tailnet: https://<pc>.ts.net:8080/<app>/ */
  pathUrl?: string;
  sourceKind: "pc" | "vps" | "git";
  gitUrl: string;
  gitBranch: string;
  autoDeploy: boolean;
  lastCommit: string;
  hasGitToken?: boolean;
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

/** An app's environment variable. Secret values are never sent to the browser. */
export interface EnvVarView { name: string; value?: string; secret: boolean; hasValue: boolean }
/** keep: for a secret, keep the stored value (send value ""). */
export interface EnvVarInput { name: string; value: string; secret: boolean; keep?: boolean }

export interface UpdateInfo {
  current: string;
  comparable: boolean;
  enabled: boolean;
  latest?: { version: string; tag: string; name: string; notes: string; url: string; published: string; installer?: { name: string; size: number } };
  available: boolean;
  checkedAt?: string;
  error?: string;
  canInstall: boolean;
  installing?: "downloading" | "starting";
  installError?: string;
}

export interface Snapshot {
  id: number;
  vpsId: number;
  label: string;
  status: "creating" | "ready" | "failed";
  error?: string;
  systemBytes: number;
  homeBytes: number;
  createdAt: string;
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
  login: (username: string, password: string) => request<User | TwoFactorChallenge>("POST", "/auth/login", { username, password }),
  login2fa: (ticket: string, code: string) => request<User>("POST", "/auth/login/2fa", { ticket, code }),
  twoFactor: () => request<{ enabled: boolean; recoveryCodesLeft: number }>("GET", "/auth/2fa"),
  setup2fa: (password: string) => request<{ secret: string; uri: string; qr: string }>("POST", "/auth/2fa/setup", { password }),
  enable2fa: (code: string) => request<{ recoveryCodes: string[] }>("POST", "/auth/2fa/enable", { code }),
  disable2fa: (password: string, code: string) => request<void>("POST", "/auth/2fa/disable", { password, code }),
  newRecoveryCodes: (password: string, code: string) => request<{ recoveryCodes: string[] }>("POST", "/auth/2fa/recovery-codes", { password, code }),
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
  snapshots: (id: number) => request<Snapshot[]>("GET", `/vps/${id}/snapshots`),
  takeSnapshot: (id: number, label: string) => request<Snapshot>("POST", `/vps/${id}/snapshots`, { label }),
  restoreSnapshot: (id: number, sid: number) => request<VPS>("POST", `/vps/${id}/snapshots/${sid}/restore`),
  cloneSnapshot: (id: number, sid: number, name: string) => request<VPS>("POST", `/vps/${id}/snapshots/${sid}/clone`, { name }),
  deleteSnapshot: (id: number, sid: number) => request<void>("DELETE", `/vps/${id}/snapshots/${sid}`),
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
    sourceKind?: "pc" | "vps" | "git";
    gitUrl?: string;
    gitBranch?: string;
    gitToken?: string;
    autoDeploy?: boolean;
    vpsPath?: string;
    packages?: string;
    installCmd?: string;
    startCmd?: string;
  }) => request<AppItem>("POST", "/apps", a),
  appEnv: (id: number) => request<EnvVarView[]>("GET", `/apps/${id}/env`),
  setAppEnv: (id: number, vars: EnvVarInput[]) => request<EnvVarView[]>("PUT", `/apps/${id}/env`, { vars }),
  detectGit: (url: string, branch: string, token: string) =>
    request<DetectResult & { commit: GitCommit }>("POST", "/apps/detect-git", { url, branch, token }),
  setAppGit: (id: number, g: { branch: string; token?: string; clearToken?: boolean; autoDeploy: boolean }) => request<AppItem>("PUT", `/apps/${id}/git`, g),
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
  update: () => request<UpdateInfo>("GET", "/system/update"),
  checkUpdate: () => request<UpdateInfo>("POST", "/system/update/check"),
  setUpdateCheck: (enabled: boolean) => request<UpdateInfo>("PUT", "/system/update", { enabled }),
  installUpdate: () => request<UpdateInfo>("POST", "/system/update/install"),
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
