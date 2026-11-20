import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router-dom";
import { api, type Snapshot, type VPS } from "../api";
import { Button, Card, ErrorBox, Input, StateBadge } from "./ui";
import { formatBytes } from "./Usage";

/** Take, restore, copy and delete snapshots of a VPS. */
export function SnapshotsCard({ vps }: { vps: VPS }) {
  const qc = useQueryClient();
  const nav = useNavigate();
  const key = ["vps", vps.id, "snapshots"];
  const list = useQuery({
    queryKey: key,
    queryFn: () => api.snapshots(vps.id),
    refetchInterval: (q) => (q.state.data?.some((s) => s.status === "creating") || vps.state === "restoring" ? 1500 : 10000),
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
        <ErrorBox error={take.error ?? restore.error ?? remove.error} />
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
  cloneForm,
}: {
  s: Snapshot;
  busy: boolean;
  onRestore: () => void;
  onClone: () => void;
  onDelete: () => void;
  cloneForm: React.ReactNode;
}) {
  const ready = s.status === "ready";
  return (
    <li className="border-b border-line-soft px-4 py-3 last:border-b-0">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <div className="min-w-0 flex-[1_1_200px]">
          <div className="font-medium break-words">{s.label}</div>
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
          <Button size="sm" variant="minimal" disabled={s.status === "creating" || busy} onClick={onDelete} aria-label={`Delete snapshot ${s.label}`}>
            Delete
          </Button>
        </div>
      </div>
      {cloneForm}
    </li>
  );
}
