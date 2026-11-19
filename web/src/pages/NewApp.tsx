import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router-dom";
import { api, type Detection } from "../api";
import { Button, Card, ErrorBox, Field, Input, TextArea } from "../components/ui";

const absPath = (p: string) => /^([a-zA-Z]:[\\/]|\\\\|\/)/.test(p.trim());

function useDebounced<T>(value: T, ms: number) {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return v;
}

export function NewApp() {
  const nav = useNavigate();
  const qc = useQueryClient();
  const [form, setForm] = useState({ name: "", sourcePath: "", containerPort: "80", cpus: "0", memoryMb: "0" });
  const [portTouched, setPortTouched] = useState(false);
  const [template, setTemplate] = useState<Detection | null>(null);
  const [dockerfile, setDockerfile] = useState("");
  const [saveToProject, setSaveToProject] = useState(false);
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });

  const path = useDebounced(form.sourcePath.trim(), 500);
  const detect = useQuery({
    queryKey: ["detect", path],
    queryFn: () => api.detectApp(path),
    enabled: absPath(path),
    retry: false,
  });
  const needsDockerfile = detect.data && !detect.data.hasDockerfile;

  // Load the detected template whenever a new folder is detected.
  useEffect(() => {
    const d = detect.data;
    if (!d || d.hasDockerfile) {
      setTemplate(null);
      return;
    }
    applyTemplate(d.detection);
  }, [detect.data]);

  function applyTemplate(t: Detection) {
    setTemplate(t);
    setDockerfile(t.dockerfile);
    if (!portTouched) setForm((f) => ({ ...f, containerPort: String(t.port) }));
  }

  const m = useMutation({
    mutationFn: () =>
      api.createApp({
        name: form.name.trim(),
        sourcePath: form.sourcePath.trim(),
        containerPort: Number(form.containerPort),
        cpus: Number(form.cpus) || 0,
        memoryMb: Number(form.memoryMb) || 0,
        ...(needsDockerfile ? { dockerfile, saveToProject } : {}),
      }),
    onSuccess: (a) => {
      qc.invalidateQueries({ queryKey: ["apps"] });
      nav(`/apps/${a.id}`);
    },
  });

  return (
    <Card title="New app">
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          m.mutate();
        }}
      >
        <p className="text-sm text-slate-500 dark:text-slate-400">
          LPS builds your project folder and runs it as a Linux container. If the folder has no <code>Dockerfile</code>,
          LPS detects the project type and writes one for you to review.
        </p>
        <Field label="Name">
          <Input value={form.name} onChange={set("name")} placeholder="my-site" required pattern="[a-z0-9][a-z0-9\-]{1,30}" />
        </Field>
        <Field label="Project folder on this PC" hint="Absolute path, e.g. C:\Users\you\projects\my-site or /home/you/my-site">
          <Input value={form.sourcePath} onChange={set("sourcePath")} required />
        </Field>

        {detect.isFetching && <p className="text-sm text-slate-500">Looking at the folder…</p>}
        {detect.error && <ErrorBox error={detect.error} />}
        {detect.data?.hasDockerfile && (
          <p className="rounded-lg bg-emerald-50 px-3 py-2 text-sm text-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-300">
            ✓ Found a Dockerfile in this folder. LPS will use it.
          </p>
        )}
        {needsDockerfile && template && (
          <div className="space-y-3 rounded-xl border border-indigo-200 bg-indigo-50/50 p-4 dark:border-indigo-900 dark:bg-indigo-950/30">
            <div>
              <div className="font-medium">No Dockerfile found: LPS made one for you</div>
              <div className="text-sm text-slate-600 dark:text-slate-300">
                Detected: <b>{detect.data!.detection.label}</b>. Review it, change the template, or edit it below.
              </div>
            </div>
            <Field label="Template">
              <select
                aria-label="Template"
                className="w-full rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm dark:border-slate-700 dark:bg-slate-950"
                value={template.type}
                onChange={(e) => {
                  const t = detect.data!.templates.find((x) => x.type === e.target.value);
                  if (t) applyTemplate(t);
                }}
              >
                {detect.data!.templates.map((t) => (
                  <option key={t.type} value={t.type}>
                    {t.label}
                    {t.type === detect.data!.detection.type ? " (detected)" : ""}
                  </option>
                ))}
              </select>
            </Field>
            <Field label="Dockerfile">
              <TextArea aria-label="Dockerfile" rows={Math.min(22, dockerfile.split("\n").length + 1)} value={dockerfile} onChange={(e) => setDockerfile(e.target.value)} spellCheck={false} />
            </Field>
            {template.notes && template.notes.length > 0 && (
              <ul className="list-disc space-y-1 pl-5 text-xs text-slate-600 dark:text-slate-300">
                {template.notes.map((n) => (
                  <li key={n}>{n}</li>
                ))}
              </ul>
            )}
            <label className="flex items-start gap-2 text-sm">
              <input type="checkbox" className="mt-1" checked={saveToProject} onChange={(e) => setSaveToProject(e.target.checked)} />
              <span>
                Also save this Dockerfile into the project folder
                <span className="block text-xs text-slate-500 dark:text-slate-400">
                  Creates <code>Dockerfile</code> (and <code>.dockerignore</code> if missing) so you can edit it in your editor
                  and commit it. Off: LPS keeps it and your folder is not changed.
                </span>
              </span>
            </label>
          </div>
        )}

        <div className="grid gap-4 sm:grid-cols-3">
          <Field label="App port in container" hint="The port your app listens on">
            <Input
              type="number"
              min={1}
              max={65535}
              value={form.containerPort}
              onChange={(e) => {
                setPortTouched(true);
                set("containerPort")(e);
              }}
              required
            />
          </Field>
          <Field label="CPU limit" hint="0 = no limit">
            <Input type="number" min={0} max={64} step={0.5} value={form.cpus} onChange={set("cpus")} />
          </Field>
          <Field label="Memory limit (MB)" hint="0 = no limit">
            <Input type="number" min={0} step={128} value={form.memoryMb} onChange={set("memoryMb")} />
          </Field>
        </div>
        <ErrorBox error={m.error} />
        <div className="flex gap-2">
          <Button type="submit" disabled={m.isPending || detect.isFetching}>
            {m.isPending ? "Creating…" : "Create and deploy"}
          </Button>
          <Link to="/" className="rounded-lg px-3.5 py-2 text-sm text-slate-600 hover:underline dark:text-slate-300">
            Cancel
          </Link>
        </div>
      </form>
    </Card>
  );
}
