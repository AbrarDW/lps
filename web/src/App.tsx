import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, Navigate, Route, Routes, useNavigate } from "react-router-dom";
import { api, ApiError } from "./api";
import { AuthPage } from "./pages/Auth";
import { Dashboard } from "./pages/Dashboard";
import { NewVPS } from "./pages/NewVPS";
import { VPSDetail } from "./pages/VPSDetail";
import { NewApp } from "./pages/NewApp";
import { AppDetail } from "./pages/AppDetail";
import { Remote } from "./pages/Remote";

export default function App() {
  const setup = useQuery({ queryKey: ["setup"], queryFn: api.setupStatus });
  const me = useQuery({ queryKey: ["me"], queryFn: api.me, enabled: setup.data?.needsSetup === false });

  if (setup.isLoading || (setup.data?.needsSetup === false && me.isLoading)) {
    return <div className="p-10 text-center text-slate-500">Loading…</div>;
  }
  if (setup.error) {
    return <div className="p-10 text-center text-rose-600">Cannot reach lpsd: {String(setup.error)}</div>;
  }
  if (setup.data?.needsSetup) return <AuthPage mode="setup" />;
  if (me.error instanceof ApiError && me.error.status === 401) return <AuthPage mode="login" />;

  return (
    <div className="min-h-screen">
      <Header username={me.data?.username} />
      <main className="mx-auto max-w-5xl space-y-6 px-4 py-6">
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/vps/new" element={<NewVPS />} />
          <Route path="/vps/:id" element={<VPSDetail />} />
          <Route path="/apps/new" element={<NewApp />} />
          <Route path="/apps/:id" element={<AppDetail />} />
          <Route path="/remote" element={<Remote />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>
    </div>
  );
}

function Header({ username }: { username?: string }) {
  const qc = useQueryClient();
  const nav = useNavigate();
  return (
    <header className="border-b border-slate-200 bg-white dark:border-slate-800 dark:bg-slate-900">
      <div className="mx-auto flex max-w-5xl items-center justify-between px-4 py-3">
        <Link to="/" className="flex items-center gap-2 font-semibold">
          <span className="grid h-7 w-7 place-items-center rounded-md bg-indigo-600 text-xs font-bold text-white">LPS</span>
          <span className="hidden sm:inline">Local Personal Server</span>
        </Link>
        <div className="flex items-center gap-4 text-sm">
          <Link to="/remote" className="text-indigo-600 hover:underline dark:text-indigo-400">
            Remote access
          </Link>
          <span className="hidden text-slate-500 sm:inline dark:text-slate-400">{username}</span>
          <button
            className="text-indigo-600 hover:underline dark:text-indigo-400"
            onClick={async () => {
              await api.logout();
              qc.clear();
              nav("/");
              qc.invalidateQueries();
            }}
          >
            Log out
          </button>
        </div>
      </div>
    </header>
  );
}
