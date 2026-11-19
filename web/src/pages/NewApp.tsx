import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router-dom";
import { api } from "../api";
import { Button, Card, ErrorBox, Field, Input } from "../components/ui";

export function NewApp() {
  const nav = useNavigate();
  const qc = useQueryClient();
  const [form, setForm] = useState({ name: "", sourcePath: "", containerPort: "80", cpus: "0", memoryMb: "0" });
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });

  const m = useMutation({
    mutationFn: () =>
      api.createApp({
        name: form.name.trim(),
        sourcePath: form.sourcePath.trim(),
        containerPort: Number(form.containerPort),
        cpus: Number(form.cpus) || 0,
        memoryMb: Number(form.memoryMb) || 0,
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
          LPS builds the project folder's <code>Dockerfile</code> and runs it as a Linux container. Automatic detection for
          Node, Python and static sites without a Dockerfile is planned.
        </p>
        <Field label="Name">
          <Input value={form.name} onChange={set("name")} placeholder="my-site" required pattern="[a-z0-9][a-z0-9\-]{1,30}" />
        </Field>
        <Field label="Project folder on this PC" hint="Absolute path, e.g. C:\Users\you\projects\my-site or /home/you/my-site">
          <Input value={form.sourcePath} onChange={set("sourcePath")} required />
        </Field>
        <div className="grid gap-4 sm:grid-cols-3">
          <Field label="App port in container" hint="The port your app listens on">
            <Input type="number" min={1} max={65535} value={form.containerPort} onChange={set("containerPort")} required />
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
          <Button type="submit" disabled={m.isPending}>
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
