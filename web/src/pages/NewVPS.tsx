import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router-dom";
import { api } from "../api";
import { Button, Card, ErrorBox, Field, Input, PageHeader, TextArea } from "../components/ui";

export function NewVPS() {
  const nav = useNavigate();
  const qc = useQueryClient();
  const [form, setForm] = useState({ name: "", sshUser: "dev", sshPublicKey: "", password: "", cpus: "1", memoryMb: "1024" });
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });

  const m = useMutation({
    mutationFn: () =>
      api.createVPS({
        name: form.name.trim(),
        sshUser: form.sshUser.trim(),
        sshPublicKey: form.sshPublicKey.trim(),
        password: form.password,
        cpus: Number(form.cpus) || 0,
        memoryMb: Number(form.memoryMb) || 0,
      }),
    onSuccess: (v) => {
      qc.invalidateQueries({ queryKey: ["vps"] });
      nav(`/vps/${v.id}`);
    },
  });

  return (
    <>
      <PageHeader sheet="Sheet 02 — New VPS" title="Create a Linux VPS" />
      <Card title="VPS details" marks>
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          m.mutate();
        }}
      >
        <p className="text-[13px] text-ink-muted">
          Creates an Ubuntu 24.04 machine with SSH. On Windows it runs as a Linux container inside Docker Desktop's WSL2
          VM. Files under <code>/home</code> are kept on a persistent volume.
        </p>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Name" hint="Lowercase letters, digits and dashes. Also used as the hostname.">
            <Input value={form.name} onChange={set("name")} placeholder="dev-box" required pattern="[a-z0-9][a-z0-9\-]{1,30}" />
          </Field>
          <Field label="SSH username">
            <Input value={form.sshUser} onChange={set("sshUser")} required pattern="[a-z_][a-z0-9_\-]{0,30}" />
          </Field>
        </div>
        <Field
          label="SSH public key (recommended)"
          hint={
            <>
              Paste the contents of <code>~/.ssh/id_ed25519.pub</code>. Create one with <code>ssh-keygen -t ed25519</code>{" "}
              (works in Windows PowerShell too).
            </>
          }
        >
          <TextArea rows={3} value={form.sshPublicKey} onChange={set("sshPublicKey")} placeholder="ssh-ed25519 AAAA… you@laptop" />
        </Field>
        <Field label="Password (optional)" hint="Enables password login and sudo password. Leave empty for key-only login with passwordless sudo.">
          <Input type="password" autoComplete="new-password" value={form.password} onChange={set("password")} minLength={8} />
        </Field>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="CPU limit (cores)" hint="0 = no limit">
            <Input type="number" min={0} max={64} step={0.5} value={form.cpus} onChange={set("cpus")} />
          </Field>
          <Field label="Memory limit (MB)" hint="0 = no limit">
            <Input type="number" min={0} step={128} value={form.memoryMb} onChange={set("memoryMb")} />
          </Field>
        </div>
        <ErrorBox error={m.error} />
        <div className="flex gap-2">
          <Button type="submit" disabled={m.isPending}>
            {m.isPending ? "Creating…" : "Create VPS"}
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
