import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type EnvVarInput, type EnvVarView } from "../api";
import { Button, ErrorBox, Input, TextArea } from "./ui";

/** A row being edited. For a saved secret, value stays "" until replaced. */
interface Row {
  key: number;
  name: string;
  value: string;
  secret: boolean;
  /** A secret whose stored value is kept (hidden from the browser). */
  kept: boolean;
}

let nextKey = 1;
const toRows = (vars: EnvVarView[]): Row[] =>
  vars.map((v) => ({ key: nextKey++, name: v.name, value: v.value ?? "", secret: v.secret, kept: v.secret && v.hasValue }));

const looksSecret = (name: string) => /SECRET|TOKEN|PASSWORD|PASSWD|PRIVATE|API_?KEY|_KEY$|CREDENTIAL/i.test(name);

/** Parses KEY=VALUE lines (a .env file): comments, blank lines and "export " are skipped. */
export function parseDotEnv(text: string): { name: string; value: string }[] {
  const out: { name: string; value: string }[] = [];
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim();
    if (!line || line.startsWith("#")) continue;
    const m = line.replace(/^export\s+/, "").match(/^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$/);
    if (!m) continue;
    let value = m[2];
    const q = value[0];
    if ((q === '"' || q === "'") && value.endsWith(q) && value.length >= 2) {
      value = value.slice(1, -1);
      if (q === '"') value = value.replace(/\\n/g, "\n").replace(/\\"/g, '"');
    } else {
      value = value.replace(/\s+#.*$/, ""); // trailing comment
    }
    out.push({ name: m[1], value });
  }
  return out;
}

/** Edits an app's environment variables and secrets. Saving redeploys the app. */
export function EnvCard({ appId, inVPS, deploying }: { appId: number; inVPS: boolean; deploying?: boolean }) {
  const qc = useQueryClient();
  const env = useQuery({ queryKey: ["apps", appId, "env"], queryFn: () => api.appEnv(appId) });
  const [rows, setRows] = useState<Row[]>([]);
  const [dirty, setDirty] = useState(false);
  const [paste, setPaste] = useState<string | null>(null);

  useEffect(() => {
    if (env.data && !dirty) setRows(toRows(env.data));
  }, [env.data, dirty]);

  const save = useMutation({
    mutationFn: () =>
      api.setAppEnv(
        appId,
        rows
          .filter((r) => r.name.trim() !== "")
          .map<EnvVarInput>((r) => ({ name: r.name.trim(), value: r.value, secret: r.secret, keep: r.secret && r.kept && r.value === "" })),
      ),
    onSuccess: (vars) => {
      qc.setQueryData(["apps", appId, "env"], vars);
      setRows(toRows(vars));
      setDirty(false);
      qc.invalidateQueries({ queryKey: ["apps", appId] });
    },
  });

  const update = (key: number, patch: Partial<Row>) => {
    setDirty(true);
    setRows((rs) => rs.map((r) => (r.key === key ? { ...r, ...patch } : r)));
  };

  function importPaste() {
    const parsed = parseDotEnv(paste ?? "");
    if (!parsed.length) return;
    setDirty(true);
    setRows((rs) => {
      const next = [...rs];
      for (const p of parsed) {
        const i = next.findIndex((r) => r.name === p.name);
        if (i >= 0) next[i] = { ...next[i], value: p.value, kept: false };
        else next.push({ key: nextKey++, name: p.name, value: p.value, secret: looksSecret(p.name), kept: false });
      }
      return next;
    });
    setPaste(null);
  }

  if (env.isLoading) return <p className="label-mono m-0 text-ink-faint">Loading…</p>;

  return (
    <form
      className="flex flex-col gap-4 text-sm"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <p className="m-0 text-ink-muted">
        Variables your app reads from its environment, such as <code>DATABASE_URL</code> or API keys.{" "}
        {inVPS ? (
          <>
            They're given to the install and start commands in the VPS. LPS sets <code>PORT</code>, <code>HOST</code> and <code>HOME</code> itself.
          </>
        ) : (
          <>They're set on the app's container.</>
        )}{" "}
        Saving redeploys the app. Values marked <b>secret</b> are hidden here after saving and never shown in logs or the activity feed; they're
        stored in LPS's database on this PC.
      </p>
      <ErrorBox error={env.error} />

      {rows.length === 0 ? (
        <p className="m-0 rounded-[2px] border border-dashed border-line px-3 py-3 text-[13px] text-ink-muted">No variables yet.</p>
      ) : (
        <div role="table" aria-label="Environment variables" className="flex flex-col gap-2">
          <div role="row" className="hidden grid-cols-[minmax(0,2fr)_minmax(0,3fr)_auto_auto] gap-2 sm:grid">
            <span role="columnheader" className="label-mono text-[10px] text-ink-faint">Name</span>
            <span role="columnheader" className="label-mono text-[10px] text-ink-faint">Value</span>
            <span role="columnheader" className="label-mono text-[10px] text-ink-faint">Secret</span>
            <span role="columnheader" className="sr-only">Remove</span>
          </div>
          {rows.map((r) => (
            <div role="row" key={r.key} className="grid grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-2 sm:grid-cols-[minmax(0,2fr)_minmax(0,3fr)_auto_auto]">
              <Input
                role="cell"
                aria-label="Variable name"
                value={r.name}
                placeholder="NAME"
                spellCheck={false}
                autoCapitalize="characters"
                className="col-span-3 font-mono sm:col-span-1"
                onChange={(e) => update(r.key, { name: e.target.value })}
              />
              <Input
                role="cell"
                aria-label={`Value of ${r.name || "variable"}`}
                value={r.value}
                type={r.secret ? "password" : "text"}
                autoComplete="off"
                spellCheck={false}
                placeholder={r.kept ? "•••••• saved; type to replace" : "value"}
                className="font-mono"
                onChange={(e) => update(r.key, { value: e.target.value })}
              />
              <label role="cell" className="flex items-center gap-1.5 text-xs text-ink-2" title="Hide the value after saving">
                <input type="checkbox" className="accent-[#2d72d2]" checked={r.secret} onChange={(e) => update(r.key, { secret: e.target.checked, kept: false })} />
                <span className="sm:sr-only">Secret</span>
              </label>
              <Button variant="minimal" size="sm" aria-label={`Remove ${r.name || "variable"}`} onClick={() => {
                setDirty(true);
                setRows((rs) => rs.filter((x) => x.key !== r.key));
              }}>
                ✕
              </Button>
            </div>
          ))}
        </div>
      )}

      <div className="flex flex-wrap gap-2">
        <Button variant="secondary" size="sm" onClick={() => {
          setDirty(true);
          setRows((rs) => [...rs, { key: nextKey++, name: "", value: "", secret: false, kept: false }]);
        }}>
          Add variable
        </Button>
        <Button variant="secondary" size="sm" onClick={() => setPaste(paste === null ? "" : null)}>
          {paste === null ? "Paste a .env file" : "Cancel paste"}
        </Button>
      </div>

      {paste !== null && (
        <div className="flex flex-col gap-2">
          <TextArea aria-label="Paste .env contents" rows={6} value={paste} spellCheck={false} placeholder={"NODE_ENV=production\nDATABASE_URL=postgres://…"} onChange={(e) => setPaste(e.target.value)} />
          <span className="text-xs text-ink-muted">Existing names are updated; names that look like secrets (TOKEN, PASSWORD, API_KEY…) are marked secret.</span>
          <Button size="sm" className="self-start" disabled={!parseDotEnv(paste).length} onClick={importPaste}>
            Add {parseDotEnv(paste).length || ""} variable{parseDotEnv(paste).length === 1 ? "" : "s"}
          </Button>
        </div>
      )}

      <div className="flex flex-wrap items-center gap-2 border-t border-line-soft pt-4">
        <Button type="submit" disabled={!dirty || save.isPending || deploying}>
          {save.isPending ? "Saving…" : "Save & redeploy"}
        </Button>
        {dirty && (
          <Button variant="secondary" onClick={() => {
            setDirty(false);
            if (env.data) setRows(toRows(env.data));
          }}>
            Discard changes
          </Button>
        )}
        {deploying && <span className="text-xs text-ink-muted">A deployment is running; save when it finishes.</span>}
      </div>
      <ErrorBox error={save.error} />
    </form>
  );
}
