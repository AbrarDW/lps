import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router-dom";
import { api, type Detection } from "../api";
import { Button, Card, ErrorBox, Field, Input, PageHeader, TextArea } from "../components/ui";

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
    <>
      <PageHeader sheet="Sheet 03 — New app" title="Deploy an app" />
      <Card title="App details" marks>
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          m.mutate();
        }}
      >
        <p className="text-[13px] text-ink-muted">
          LPS builds your project folder and runs it as a Linux container. If the folder has no <code>Dockerfile</code>,
          LPS detects the project type and writes one for you to review.
        </p>
        <Field label="Name">
          <Input value={form.name} onChange={set("name")} placeholder="my-site" required pattern="[a-z0-9][a-z0-9\-]{1,30}" />
        </Field>
        <Field label="Project folder on this PC" hint="Absolute path, e.g. C:\Users\you\projects\my-site or /home/you/my-site">
          <Input value={form.sourcePath} onChange={set("sourcePath")} required />
        </Field>

        {detect.isFetching && <p className="text-[13px] text-ink-muted">Looking at the folder…</p>}
        {detect.error && <ErrorBox error={detect.error} />}
        {detect.data?.hasDockerfile && (
          <p className="rounded-[2px] border border-success/45 bg-success/12 px-3 py-2 text-[13px] text-success-ink">
            ✓ Found a Dockerfile in this folder. LPS will use it.
          </p>
        )}
        {needsDockerfile && template && (
          <div className="relative space-y-3 rounded-bp border border-cyan/45 bg-primary/8 p-4">
            <div>
              <div className="font-medium">No Dockerfile found: LPS made one for you</div>
              <div className="text-[13px] text-ink-2">
                Detected: <b>{detect.data!.detection.label}</b>. Review it, change the template, or edit it below.
              </div>
            </div>
            <Field label="Template">
              <select
                aria-label="Template"
                className="h-[34px] w-full rounded-bp border border-[#2f5f92] bg-ground-deep px-2 text-ink"
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
              <ul className="list-disc space-y-1 pl-5 text-xs text-ink-2">
                {template.notes.map((n) => (
                  <li key={n}>{n}</li>
                ))}
              </ul>
            )}
            <label className="flex items-start gap-2 text-sm">
              <input type="checkbox" className="mt-1" checked={saveToProject} onChange={(e) => setSaveToProject(e.target.checked)} />
              <span>
                Also save this Dockerfile into the project folder
                <span className="block text-xs text-ink-muted">
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
          <Link to="/" className="inline-flex h-9 items-center px-3 text-ink-muted no-underline hover:text-ink">
            Cancel
          </Link>
        </div>
      </form>
    </Card>
    </>
  );
}
