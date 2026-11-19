import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, type AppItem } from "../api";
import { RunSetupFields } from "../pages/NewApp";
import { Button, ErrorBox } from "./ui";

/** Edits how an app that runs inside a VPS is installed and started. */
export function RunConfigCard({ app, deploying }: { app: AppItem; deploying?: boolean }) {
  const qc = useQueryClient();
  const saved = { packages: app.packages, install: app.installCmd, start: app.startCmd };
  const [value, setValue] = useState(saved);
  const [editing, setEditing] = useState(false);

  useEffect(() => {
    if (!editing) setValue({ packages: app.packages, install: app.installCmd, start: app.startCmd });
  }, [app.packages, app.installCmd, app.startCmd, editing]);

  const save = useMutation({
    mutationFn: () => api.setAppRunConfig(app.id, { packages: value.packages, installCmd: value.install, startCmd: value.start }),
    onSuccess: () => {
      setEditing(false);
      qc.invalidateQueries({ queryKey: ["apps", app.id] });
    },
  });
  const dirty = value.packages !== saved.packages || value.install !== saved.install || value.start !== saved.start;

  return (
    <form
      className="flex flex-col gap-4 text-sm"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <p className="m-0 text-ink-muted">
        {app.sourceKind === "vps" ? (
          <>
            Runs from <code>{app.vpsPath}</code> in VPS {app.vpsName}. After you change the code there (e.g. <code>git pull</code>), press <b>Redeploy</b>.
          </>
        ) : (
          <>
            Copied from <code>{app.sourcePath}</code> to <code>~/apps/{app.name}</code> in VPS {app.vpsName} on every deploy.
          </>
        )}{" "}
        Output of the start command is in <b>App logs</b>.
      </p>
      <RunSetupFields
        value={value}
        onChange={(v) => {
          setEditing(true);
          setValue({ ...value, ...v });
        }}
      />
      <div className="flex flex-wrap gap-2">
        <Button type="submit" disabled={!dirty || save.isPending || deploying}>
          {save.isPending ? "Saving…" : "Save & redeploy"}
        </Button>
        {editing && (
          <Button variant="secondary" onClick={() => setEditing(false)}>
            Discard changes
          </Button>
        )}
      </div>
      <ErrorBox error={save.error} />
    </form>
  );
}
