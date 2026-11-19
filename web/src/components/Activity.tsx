import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, type ActivityEvent, type ActivitySubject } from "../api";
import { Button, ErrorBox } from "./ui";

const sameDay = (a: Date, b: Date) => a.toDateString() === b.toDateString();

function when(at: string) {
  const d = new Date(at);
  const time = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  return sameDay(d, new Date()) ? time : `${d.toLocaleDateString([], { month: "short", day: "numeric" })} ${time}`;
}

const levelText: Record<ActivityEvent["level"], string> = {
  info: "text-ink",
  warning: "text-warning-ink",
  error: "text-danger-ink",
};

/** Link to what an event is about, while it still exists in the lists. */
function subjectLink(e: ActivityEvent) {
  if (e.subject === "vps" && e.subjectId) return `/vps/${e.subjectId}`;
  if (e.subject === "app" && e.subjectId) return `/apps/${e.subjectId}`;
  if (e.subject === "remote") return "/remote";
  return undefined;
}

function Row({ e, showLinks }: { e: ActivityEvent; showLinks: boolean }) {
  const href = showLinks && !/deleted$/.test(e.kind) ? subjectLink(e) : undefined;
  const remote = e.ip && e.ip !== "127.0.0.1" && e.ip !== "::1" ? `from ${e.ip}` : "";
  // Events without a user: LPS acted by itself, or (sign-ins) nobody was signed in yet.
  const who = e.actor ? `by ${e.actor}` : e.subject === "auth" ? (remote ? "" : "from this PC") : "by LPS";
  const by = [who, remote].filter(Boolean).join(" ");
  return (
    <li className="grid grid-cols-[minmax(52px,auto)_1fr] gap-x-2.5 gap-y-0.5 text-[13px]">
      <time dateTime={e.at} title={new Date(e.at).toLocaleString()} className="pt-0.5 font-mono text-[11px] whitespace-nowrap text-ink-faint">
        {when(e.at)}
      </time>
      <span className={`min-w-0 break-words ${levelText[e.level]}`}>
        {e.level !== "info" && <span className="sr-only">{e.level}: </span>}
        {href ? (
          <Link to={href} className={`no-underline hover:underline ${levelText[e.level]}`}>
            {e.message}
          </Link>
        ) : (
          e.message
        )}
        <span className="block text-[11px] text-ink-faint">{by}</span>
      </span>
    </li>
  );
}

/**
 * The activity feed: newest first, refreshed every few seconds.
 * With subject/id it shows only one VPS's or app's events.
 */
export function ActivityList({
  subject = "",
  id,
  limit = 8,
  paged,
  showLinks = true,
  empty = "Nothing has happened yet.",
}: {
  subject?: ActivitySubject;
  id?: number;
  limit?: number;
  /** Show a "Show older" button that loads more. */
  paged?: boolean;
  showLinks?: boolean;
  empty?: string;
}) {
  const [older, setOlder] = useState<ActivityEvent[]>([]);
  const [olderDone, setOlderDone] = useState(false);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [olderError, setOlderError] = useState<unknown>(null);
  const latest = useQuery({
    queryKey: ["events", subject, id ?? 0, limit],
    queryFn: () => api.events({ subject, id, limit }),
    refetchInterval: 5000,
  });
  const recent = latest.data ?? [];
  const lastRecent = recent[recent.length - 1];
  // Older pages continue from the oldest event shown.
  const seen = new Set(recent.map((e) => e.id));
  const all = [...recent, ...older.filter((e) => !seen.has(e.id) && (!lastRecent || e.id < lastRecent.id))];

  async function loadOlder() {
    const oldest = all[all.length - 1];
    if (!oldest) return;
    setLoadingOlder(true);
    setOlderError(null);
    try {
      const page = await api.events({ subject, id, before: oldest.id, limit: 50 });
      setOlder((o) => [...o, ...page]);
      if (page.length < 50) setOlderDone(true);
    } catch (err) {
      setOlderError(err);
    } finally {
      setLoadingOlder(false);
    }
  }

  return (
    <div className="flex flex-col gap-3 p-4">
      <ErrorBox error={latest.error} />
      {latest.isLoading ? (
        <p className="label-mono m-0 text-ink-faint">Loading…</p>
      ) : all.length === 0 ? (
        <p className="m-0 text-[13px] text-ink-muted">{empty}</p>
      ) : (
        <ol aria-label="Activity" className="m-0 flex list-none flex-col gap-3 p-0">
          {all.map((e) => (
            <Row key={e.id} e={e} showLinks={showLinks} />
          ))}
        </ol>
      )}
      <ErrorBox error={olderError} />
      {paged && all.length >= limit && !olderDone && (
        <Button variant="secondary" size="sm" className="self-start" disabled={loadingOlder} onClick={loadOlder}>
          {loadingOlder ? "Loading…" : "Show older"}
        </Button>
      )}
    </div>
  );
}
