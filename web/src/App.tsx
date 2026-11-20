import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, NavLink, Navigate, Route, Routes } from "react-router-dom";
import { api, ApiError } from "./api";
import { AuthPage } from "./pages/Auth";
import { AppListPage, Dashboard, VPSListPage } from "./pages/Dashboard";
import { NewVPS } from "./pages/NewVPS";
import { VPSDetail } from "./pages/VPSDetail";
import { NewApp } from "./pages/NewApp";
import { AppDetail } from "./pages/AppDetail";
import { Remote } from "./pages/Remote";
import { ActivityPage } from "./pages/Activity";
import { AccountPage } from "./pages/Account";
import { SetupWizard, wizardActive } from "./pages/SetupWizard";
import { Tag } from "./components/ui";

export default function App() {
  const setup = useQuery({ queryKey: ["setup"], queryFn: api.setupStatus });
  const me = useQuery({ queryKey: ["me"], queryFn: api.me, enabled: setup.data?.needsSetup === false });

  if (setup.isLoading || (setup.data?.needsSetup === false && me.isLoading)) {
    return <div className="label-mono p-10 text-center text-ink-faint">Loading…</div>;
  }
  if (setup.error) {
    return <div className="p-10 text-center text-danger-ink">Cannot reach lpsd: {String(setup.error)}</div>;
  }
  if (setup.data?.needsSetup || wizardActive()) return <SetupWizard />;
  if (me.error instanceof ApiError && me.error.status === 401) return <AuthPage mode="login" />;

  return (
    <div className="min-h-screen">
      <Header username={me.data?.username} />
      <main className="mx-auto flex max-w-7xl flex-col gap-6 px-4 pt-6 pb-12 sm:px-6">
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/vps" element={<VPSListPage />} />
          <Route path="/vps/new" element={<NewVPS />} />
          <Route path="/vps/:id" element={<VPSDetail />} />
          <Route path="/apps" element={<AppListPage />} />
          <Route path="/apps/new" element={<NewApp />} />
          <Route path="/apps/:id" element={<AppDetail />} />
          <Route path="/remote" element={<Remote />} />
          <Route path="/activity" element={<ActivityPage />} />
          <Route path="/account" element={<AccountPage />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>
    </div>
  );
}

const navItems = [
  { to: "/", label: "Dashboard", end: true },
  { to: "/vps", label: "VPS", end: false },
  { to: "/apps", label: "Apps", end: false },
  { to: "/remote", label: "Remote access", end: false },
  { to: "/activity", label: "Activity", end: false },
];

function Header({ username }: { username?: string }) {
  const qc = useQueryClient();
  const system = useQuery({ queryKey: ["system"], queryFn: api.system, refetchInterval: 15000 });
  const remote = useQuery({ queryKey: ["remote"], queryFn: api.remote, refetchInterval: 15000 });
  const docker = system.data?.docker;

  return (
    <header className="border-b border-line bg-ground-deep">
      <div className="mx-auto flex min-h-13 max-w-7xl flex-wrap items-center gap-x-5 gap-y-2 px-4 py-2 sm:px-6">
        <Link to="/" className="flex items-center gap-2.5 text-ink no-underline hover:text-ink">
          <span className="grid size-7 place-items-center rounded-[2px] border-[1.5px] border-cyan font-mono text-[10px] font-medium text-cyan">
            LPS
          </span>
          <span className="flex flex-col leading-tight">
            <span className="text-[15px] font-semibold">LPS</span>
            <span className="hidden font-mono text-[10px] tracking-[0.12em] text-ink-faint sm:inline">LOCAL PERSONAL SERVER</span>
          </span>
        </Link>

        <nav aria-label="Main" className="flex flex-auto flex-wrap gap-0.5">
          {navItems.map((n) => (
            <NavLink
              key={n.to}
              to={n.to}
              end={n.end}
              className={({ isActive }) =>
                `flex h-8 items-center rounded-bp px-3 no-underline ${
                  isActive ? "bg-cyan/16 font-medium text-white hover:text-white" : "text-ink-2 hover:bg-white/5 hover:text-white"
                }`
              }
            >
              {n.label}
            </NavLink>
          ))}
        </nav>

        <div className="flex flex-wrap items-center gap-2">
          {docker && (
            <Tag intent={docker.ok ? "success" : "danger"} dot>
              {docker.ok ? `Docker ${docker.version ?? ""}` : "Docker offline"}
            </Tag>
          )}
          {remote.data?.available && remote.data.enabled && (
            <Link to="/remote" className="no-underline">
              <Tag intent={remote.data.needsAction ? "warning" : "primary"} dot>
                Tailnet {remote.data.needsAction ? "needs attention" : "on"}
              </Tag>
            </Link>
          )}
          <Link to="/account" title="Account and two-factor sign-in" className="inline-flex items-center gap-2 text-ink-2 no-underline hover:text-ink">
            <span className="grid size-5 place-items-center rounded-[2px] bg-[#1c4a7a] text-[11px] font-semibold uppercase">{username?.[0] ?? "?"}</span>
            <span className="hidden sm:inline">{username}</span>
            <span className="sr-only sm:hidden">Account</span>
          </Link>
          <button
            type="button"
            className="h-8 cursor-pointer rounded-bp border border-[#2f5f92] bg-transparent px-2.5 text-ink hover:bg-white/5"
            onClick={async () => {
              await api.logout().catch(() => {});
              // A full reload drops every cached query and open connection
              // (terminals, live stats), then shows the sign-in page.
              qc.clear();
              window.location.replace("/");
            }}
          >
            Log out
          </button>
        </div>
      </div>
    </header>
  );
}
