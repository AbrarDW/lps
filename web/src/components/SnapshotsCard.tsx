import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { api, type Snapshot, type VPS } from "../api";
import { Button, Card, CopyField, ErrorBox, Input, Select, StateBadge, Tag } from "./ui";
import { formatBytes } from "./Usage";

/** Take, restore, copy and delete snapshots of a VPS. */
export function SnapshotsCard({ vps }: { vps: VPS }) {
  const qc = useQueryClient();
  const nav = useNavigate();
  const key = ["vps", vps.id, "snapshots"];
  const list = useQuery({
    queryKey: key,
    queryFn: () => api.snapshots(vps.id),
    refetchInterval: (q) =>
      q.state.data?.some((s) => s.status === "creating" || s.export.state === "exporting") || vps.state === "restoring" ? 1500 : 10000,
  });
  const [label, setLabel] = useState("");
  const [cloneOf, setCloneOf] = useState<number | null>(null);
  const [cloneName, setCloneName] = useState("");
  const refresh = () => {
    qc.invalidateQueries({ queryKey: key });
    qc.invalidateQueries({ queryKey: ["vps"] });
  };

  const take = useMutation({ mutationFn: () => api.takeSnapshot(vps.id, label.trim()), onSuccess: () => (setLabel(""), refresh()) });
  const restore = useMutation({ mutationFn: (sid: number) => api.restoreSnapshot(vps.id, sid), onSuccess: refresh });
  const remove = useMutation({ mutationFn: (sid: number) => api.deleteSnapshot(vps.id, sid), onSuccess: refresh });
  const exportFile = useMutation({ mutationFn: (sid: number) => api.exportSnapshot(vps.id, sid), onSuccess: refresh });
  const dropFile = useMutation({ mutationFn: (sid: number) => api.deleteExport(vps.id, sid), onSuccess: refresh });
  const clone = useMutation({
    mutationFn: (sid: number) => api.cloneSnapshot(vps.id, sid, cloneName.trim()),
    onSuccess: (v) => {
      refresh();
      nav(`/vps/${v.id}`);
    },
  });

  const busy = vps.state === "restoring" || vps.state === "provisioning";
  const creating = list.data?.some((s) => s.status === "creating");
  const canSnap = !!vps.containerId && !busy && !creating;

  return (
    <Card title="Snapshots" flush>
      <div className="flex flex-col gap-3 p-4 text-[13px]">
        <p className="m-0 text-ink-2">
          A snapshot saves everything in this VPS: installed software, settings and <code>/home</code>. Restore it to undo changes, or start a new VPS
          from it. They're stored in Docker on this PC.
        </p>
        <form
          className="flex flex-wrap gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            take.mutate();
          }}
        >
          <Input
            aria-label="Snapshot name"
            value={label}
            onChange={(e) => setLabel(e.target.value)}
            placeholder="e.g. before upgrading Node"
            maxLength={80}
            className="min-w-0 flex-[1_1_200px]"
          />
          <Button type="submit" disabled={!canSnap || take.isPending}>
            {creating ? "Taking snapshot…" : "Take snapshot"}
          </Button>
        </form>
        {vps.state === "running" && (
          <span className="text-xs text-ink-faint">The VPS keeps running. For files that change during the snapshot (e.g. a database), stop the VPS first.</span>
        )}
        <ErrorBox error={take.error ?? restore.error ?? remove.error ?? exportFile.error ?? dropFile.error} />
        <ScheduleSection vps={vps} onChange={refresh} />
      </div>

      {list.data && list.data.length > 0 && (
        <ul className="m-0 list-none border-t border-line-soft p-0">
          {list.data.map((s) => (
            <SnapshotRow
              key={s.id}
              s={s}
              busy={busy || restore.isPending || remove.isPending}
              onRestore={() => {
                if (
                  confirm(
                    `Restore ${vps.name} to "${s.label}"?\n\nThe VPS restarts and everything changed since then is replaced. LPS takes a snapshot of the current state first, so you can undo this.`,
                  )
                )
                  restore.mutate(s.id);
              }}
              onClone={() => {
                setCloneOf(cloneOf === s.id ? null : s.id);
                setCloneName(`${vps.name}-copy`.slice(0, 31));
                clone.reset();
              }}
              onDelete={() => {
                if (confirm(`Delete snapshot "${s.label}"? This can't be undone.`)) remove.mutate(s.id);
              }}
              onExport={() => exportFile.mutate(s.id)}
              onDropFile={() => {
                if (confirm(`Delete the file ${s.export.file}? The snapshot itself stays.`)) dropFile.mutate(s.id);
              }}
              downloadUrl={api.exportUrl(vps.id, s.id)}
              cloneForm={
                cloneOf === s.id && (
                  <form
                    className="mt-2 flex flex-col gap-2"
                    onSubmit={(e) => {
                      e.preventDefault();
                      clone.mutate(s.id);
                    }}
                  >
                    <div className="flex flex-wrap gap-2">
                      <Input
                        aria-label="Name for the new VPS"
                        value={cloneName}
                        onChange={(e) => setCloneName(e.target.value)}
                        pattern="[a-z0-9][a-z0-9\-]{1,30}"
                        required
                        className="min-w-0 flex-[1_1_180px] font-mono"
                      />
                      <Button type="submit" size="sm" disabled={clone.isPending}>
                        {clone.isPending ? "Creating…" : "Create VPS"}
                      </Button>
                    </div>
                    <span className="text-xs text-ink-muted">Same user, password, keys and files as the snapshot; its own SSH port.</span>
                    <ErrorBox error={clone.error} />
                  </form>
                )
              }
            />
          ))}
        </ul>
      )}
      {list.data?.length === 0 && <p className="m-0 border-t border-line-soft px-4 py-3 text-[13px] text-ink-muted">No snapshots yet.</p>}
    </Card>
  );
}

function SnapshotRow({
  s,
  busy,
  onRestore,
  onClone,
  onDelete,
  onExport,
  onDropFile,
  downloadUrl,
  cloneForm,
}: {
  s: Snapshot;
  busy: boolean;
  onRestore: () => void;
  onClone: () => void;
  onDelete: () => void;
  onExport: () => void;
  onDropFile: () => void;
  downloadUrl: string;
  cloneForm: React.ReactNode;
}) {
  const ready = s.status === "ready";
  return (
    <li className="border-b border-line-soft px-4 py-3 last:border-b-0">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <div className="min-w-0 flex-[1_1_200px]">
          <div className="flex flex-wrap items-center gap-2 font-medium break-words">
            {s.label}
            {s.auto && <Tag>auto</Tag>}
          </div>
          <div className="text-xs text-ink-muted">
            {new Date(s.createdAt).toLocaleString()}
            {ready && ` · ${formatBytes(s.systemBytes + s.homeBytes)} (system ${formatBytes(s.systemBytes)}, /home ${formatBytes(s.homeBytes)})`}
          </div>
          {s.error && <div className="text-xs text-danger-ink">{s.error}</div>}
        </div>
        {s.status !== "ready" && <StateBadge state={s.status} />}
        <div className="flex flex-wrap gap-1.5">
          <Button size="sm" variant="secondary" disabled={!ready || busy} onClick={onRestore}>
            Restore
          </Button>
          <Button size="sm" variant="secondary" disabled={!ready || busy} onClick={onClone}>
            New VPS from it
          </Button>
          {s.export.state === "none" || s.export.state === "failed" ? (
            <Button size="sm" variant="secondary" disabled={!ready || busy} onClick={onExport}>
              Export to file
            </Button>
          ) : null}
          <Button size="sm" variant="minimal" disabled={s.status === "creating" || busy || s.export.state === "exporting"} onClick={onDelete} aria-label={`Delete snapshot ${s.label}`}>
            Delete
          </Button>
        </div>
      </div>
      {s.export.state === "exporting" && (
        <p className="m-0 mt-2 text-xs text-ink-muted">
          Exporting to a file… {s.export.written ? `${formatBytes(s.export.written)} written` : "saving the system files and /home first"}
        </p>
      )}
      {s.export.state === "failed" && <p className="m-0 mt-2 text-xs text-danger-ink">Export failed: {s.export.error}</p>}
      {s.export.state === "ready" && s.export.path && (
        <div className="mt-2 flex flex-col gap-1.5 rounded-[2px] border border-line-soft bg-ground-deep/60 p-2.5">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
            <span className="text-ink-muted">Saved as a file ({formatBytes(s.export.bytes ?? 0)}):</span>
            <a href={downloadUrl} download={s.export.file}>
              Download
            </a>
            <button type="button" className="cursor-pointer border-0 bg-transparent p-0 text-xs text-ink-muted hover:text-ink" onClick={onDropFile}>
              Delete file
            </button>
          </div>
          <CopyField value={s.export.path} />
          <span className="text-[11px] text-ink-faint">Copy it anywhere. On another PC, choose "Create from a snapshot file" when creating a VPS.</span>
        </div>
      )}
      {cloneForm}
    </li>
  );
}

const days = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

/** Automatic snapshots: off, daily or weekly at an hour, keeping the newest N. */
function ScheduleSection({ vps, onChange }: { vps: VPS; onChange: () => void }) {
  const qc = useQueryClient();
  const key = ["vps", vps.id, "schedule"];
  const q = useQuery({ queryKey: key, queryFn: () => api.snapshotSchedule(vps.id) });
  const [form, setForm] = useState<{ frequency: "off" | "daily" | "weekly"; hour: number; weekday: number; keep: number } | null>(null);
  const save = useMutation({
    mutationFn: () => api.setSnapshotSchedule(vps.id, form!),
    onSuccess: (v) => {
      qc.setQueryData(key, v);
      setForm(null);
      onChange();
    },
  });
  const sc = q.data?.schedule;
  const current = { frequency: sc?.frequency ?? ("off" as const), hour: sc?.hour ?? 3, weekday: sc?.weekday ?? 0, keep: sc?.keep ?? 7 };
  const f = form ?? current;
  const dirty = form !== null && JSON.stringify(form) !== JSON.stringify(current);
  const upd = (patch: Partial<typeof f>) => setForm({ ...f, ...patch });
  return (
    <div className="flex flex-col gap-2 border-t border-line-soft pt-3">
      <span className="label-mono text-[10px] text-ink-faint">Automatic snapshots</span>
      <div className="flex flex-wrap items-center gap-2">
        <Select aria-label="How often" value={f.frequency} onChange={(e) => upd({ frequency: e.target.value as typeof f.frequency })} className="w-auto!">
          <option value="off">Off</option>
          <option value="daily">Every day</option>
          <option value="weekly">Every week</option>
        </Select>
        {f.frequency === "weekly" && (
          <Select aria-label="Day" value={f.weekday} onChange={(e) => upd({ weekday: Number(e.target.value) })} className="w-auto!">
            {days.map((d, i) => (
              <option key={d} value={i}>
                on {d}
              </option>
            ))}
          </Select>
        )}
        {f.frequency !== "off" && (
          <>
            <Select aria-label="Hour" value={f.hour} onChange={(e) => upd({ hour: Number(e.target.value) })} className="w-auto!">
              {Array.from({ length: 24 }, (_, h) => (
                <option key={h} value={h}>
                  at {String(h).padStart(2, "0")}:00
                </option>
              ))}
            </Select>
            <label className="flex items-center gap-1.5">
              keep
              <Input
                aria-label="Automatic snapshots to keep"
                type="number"
                min={1}
                max={50}
                value={f.keep}
                onChange={(e) => upd({ keep: Number(e.target.value) })}
                className="w-16!"
              />
            </label>
          </>
        )}
        {dirty && (
          <Button size="sm" disabled={save.isPending} onClick={() => save.mutate()}>
            Save
          </Button>
        )}
      </div>
      <span className="text-xs text-ink-muted">
        {sc && q.data?.next
          ? `Next: ${new Date(q.data.next).toLocaleString([], { weekday: "short", day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" })} (this PC's time). `
          : ""}
        LPS takes them while it's running; if the PC was off, it catches up when LPS starts. Only automatic snapshots are deleted to keep the newest{" "}
        {f.keep}; yours stay until you delete them.
      </span>
      <ErrorBox error={save.error} />
    </div>
  );
}
