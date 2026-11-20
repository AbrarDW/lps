import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type UpdateInfo } from "../api";
import { Button, Callout, Card, ErrorBox, Switch } from "./ui";

export function useUpdate() {
  return useQuery({
    queryKey: ["update"],
    queryFn: api.update,
    refetchInterval: (q) => {
      const d = q.state.data;
      if (d?.installing) return 1500;
      // Right after LPS starts its first check hasn't run yet: look again soon.
      if (d && d.enabled && d.comparable && !d.checkedAt) return 15000;
      return 5 * 60 * 1000;
    },
  });
}

const dismissKey = (v: string) => `lps.update.dismissed.${v}`;
function dismissed(v: string) {
  try {
    return localStorage.getItem(dismissKey(v)) === "1";
  } catch {
    return false;
  }
}

/** Turns GitHub's Markdown release notes into readable plain text. */
export function plainNotes(md: string) {
  return md
    .replace(/\r/g, "")
    .replace(/^#{1,6}\s+/gm, "") // headings
    .replace(/\*\*(.+?)\*\*/g, "$1") // bold
    .replace(/`([^`]+)`/g, "$1") // code
    .replace(/\[([^\]]+)\]\((?:[^)]+)\)/g, "$1") // [text](url) -> text
    .replace(/^\* /gm, "• ")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
}

/** Release notes as plain text (never rendered as HTML). */
function Notes({ text }: { text: string }) {
  const [all, setAll] = useState(false);
  const lines = plainNotes(text).split("\n");
  const short = lines.length > 12 && !all;
  return (
    <div className="flex flex-col gap-1">
      <pre className="m-0 max-h-80 overflow-auto rounded-[2px] border border-line bg-ground-deep p-3 font-sans text-[13px] whitespace-pre-wrap text-ink-2">
        {(short ? lines.slice(0, 12) : lines).join("\n")}
      </pre>
      {short && (
        <button type="button" className="cursor-pointer self-start border-0 bg-transparent p-0 text-xs text-cyan" onClick={() => setAll(true)}>
          Show all
        </button>
      )}
    </div>
  );
}

/**
 * Installs the update and waits for the new version: the server downloads
 * the installer, starts it and quits; the installer starts LPS again.
 */
function useInstall(info?: UpdateInfo) {
  const qc = useQueryClient();
  const [phase, setPhase] = useState<"" | "working" | "restarting">("");
  const install = useMutation({
    mutationFn: api.installUpdate,
    onSuccess: (u) => {
      qc.setQueryData(["update"], u);
      setPhase("working");
    },
  });
  const from = info?.current;
  useEffect(() => {
    if (!phase) return;
    const t = setInterval(async () => {
      try {
        const sys = await api.system();
        if (sys.version !== from) {
          location.reload(); // the new version is up
          return;
        }
        const u = await api.update();
        qc.setQueryData(["update"], u);
        if (u.installError) setPhase("");
      } catch {
        setPhase("restarting"); // LPS quit for the installer
      }
    }, 2000);
    return () => clearInterval(t);
  }, [phase, from, qc]);
  return { install, phase };
}

function InstallStatus({ info, phase }: { info?: UpdateInfo; phase: string }) {
  if (!phase) return null;
  const text =
    phase === "restarting"
      ? "Installing… LPS restarts in a moment. This page reloads by itself."
      : info?.installing === "starting"
        ? "Starting the installer…"
        : "Downloading and checking the installer…";
  return (
    <Callout>
      <b>Updating to {info?.latest?.version}.</b> {text} Your VPS and apps keep running.
    </Callout>
  );
}

/** Top-of-page notice when a newer release exists. */
export function UpdateBanner() {
  const u = useUpdate().data;
  const [hidden, setHidden] = useState(false);
  const { install, phase } = useInstall(u);
  if (!u?.available || !u.latest || ((hidden || dismissed(u.latest.version)) && !phase)) return null;
  const v = u.latest.version;
  return (
    <div className="flex flex-col gap-2">
      {phase ? (
        <InstallStatus info={u} phase={phase} />
      ) : (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-2 rounded-bp border border-cyan/45 bg-primary/12 px-4 py-2.5 text-[13px]">
          <span className="min-w-0 flex-1">
            <b>LPS {v} is available.</b> You have {u.current.replace(/^v/, "")}.{" "}
            <a href={u.latest.url} target="_blank" rel="noreferrer">
              What's new
            </a>
          </span>
          {u.canInstall ? (
            <Button size="sm" disabled={install.isPending} onClick={() => install.mutate()}>
              Install update
            </Button>
          ) : (
            <a className="text-[13px]" href={u.latest.url} target="_blank" rel="noreferrer">
              Download
            </a>
          )}
          <button
            type="button"
            className="cursor-pointer border-0 bg-transparent p-0 text-ink-muted hover:text-ink"
            aria-label="Hide until the next version"
            onClick={() => {
              try {
                localStorage.setItem(dismissKey(v), "1");
              } catch {
                /* private mode: hide for this page only */
              }
              setHidden(true);
            }}
          >
            ✕
          </button>
        </div>
      )}
      <ErrorBox error={install.error ?? u.installError} />
    </div>
  );
}

/** Account page card: version, automatic checks, check now, install. */
export function UpdatesCard() {
  const qc = useQueryClient();
  const q = useUpdate();
  const u = q.data;
  const check = useMutation({ mutationFn: api.checkUpdate, onSuccess: (x) => qc.setQueryData(["update"], x) });
  const setAuto = useMutation({ mutationFn: api.setUpdateCheck, onSuccess: (x) => qc.setQueryData(["update"], x) });
  const { install, phase } = useInstall(u);
  if (!u) return null;
  return (
    <Card title="Updates">
      <div className="flex flex-col gap-3 text-[13px]">
        <p className="m-0">
          This is LPS <b>{u.current}</b>.{" "}
          {!u.comparable
            ? "It's a development build, so it isn't compared with releases."
            : u.available && u.latest
              ? `LPS ${u.latest.version} is available.`
              : u.checkedAt
                ? "It's up to date."
                : ""}
        </p>
        {u.comparable && (
          <div className="flex items-start gap-3">
            <Switch label="Check for updates automatically" checked={u.enabled} disabled={setAuto.isPending} onChange={(v) => setAuto.mutate(v)} />
            <span>
              Check for updates automatically
              <span className="block text-xs text-ink-muted">Asks GitHub for the latest LPS release twice a day. Nothing about you or this PC is sent.</span>
            </span>
          </div>
        )}
        {u.error && <span className="text-xs text-warning-ink">Last check: {u.error}</span>}
        {u.checkedAt && <span className="text-xs text-ink-faint">Last checked {new Date(u.checkedAt).toLocaleString()}</span>}
        <InstallStatus info={u} phase={phase} />
        {u.available && u.latest && !phase && (
          <div className="flex flex-col gap-2">
            <Notes text={u.latest.notes || "No release notes."} />
            <div className="flex flex-wrap items-center gap-3">
              {u.canInstall ? (
                <Button disabled={install.isPending} onClick={() => install.mutate()}>
                  Install LPS {u.latest.version}
                </Button>
              ) : (
                <a href={u.latest.url} target="_blank" rel="noreferrer">
                  Download LPS {u.latest.version} from GitHub
                </a>
              )}
              {u.canInstall && <span className="text-xs text-ink-muted">LPS restarts for a few seconds. VPS and apps keep running.</span>}
            </div>
          </div>
        )}
        <ErrorBox error={check.error ?? install.error ?? u.installError} />
        {u.comparable && !phase && (
          <Button variant="secondary" size="sm" className="self-start" disabled={check.isPending} onClick={() => check.mutate()}>
            {check.isPending ? "Checking…" : "Check now"}
          </Button>
        )}
      </div>
    </Card>
  );
}
