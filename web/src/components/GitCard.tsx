import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api, type AppItem } from "../api";
import { Button, Card, ErrorBox, Field, Input, Switch } from "./ui";

/** Branch, access token and auto-deploy for an app deployed from git. */
export function GitCard({ app, className = "" }: { app: AppItem; className?: string }) {
  const qc = useQueryClient();
  const [branch, setBranch] = useState(app.gitBranch);
  const [token, setToken] = useState("");
  const [clearToken, setClearToken] = useState(false);
  const [autoDeploy, setAutoDeploy] = useState(app.autoDeploy);
  useEffect(() => {
    setBranch(app.gitBranch);
    setAutoDeploy(app.autoDeploy);
  }, [app.gitBranch, app.autoDeploy]);

  const save = useMutation({
    mutationFn: () => api.setAppGit(app.id, { branch: branch.trim(), token: token.trim() || undefined, clearToken, autoDeploy }),
    onSuccess: (a) => {
      setToken("");
      setClearToken(false);
      qc.setQueryData(["apps", app.id], a);
      qc.invalidateQueries({ queryKey: ["apps", app.id] });
    },
  });
  const dirty = branch.trim() !== app.gitBranch || autoDeploy !== app.autoDeploy || token.trim() !== "" || clearToken;

  return (
    <Card title="Git" className={className}>
      <form
        className="flex flex-col gap-3 text-[13px]"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}
      >
        <div className="flex flex-col gap-1">
          <span className="label-mono text-[10px] text-ink-faint">Repository</span>
          <a href={app.gitUrl.replace(/\.git$/, "")} target="_blank" rel="noreferrer" className="font-mono break-all">
            {app.gitUrl}
          </a>
          {app.lastCommit && (
            <span className="text-xs text-ink-muted">
              Running commit <code>{app.lastCommit.slice(0, 7)}</code>
            </span>
          )}
        </div>
        <Field label="Branch" hint="Empty = the repository's default branch. Applies from the next deploy.">
          <Input aria-label="Branch" value={branch} onChange={(e) => setBranch(e.target.value)} placeholder="default branch" className="font-mono" />
        </Field>
        <Field
          label="Access token"
          hint={app.hasGitToken && !clearToken ? "A token is saved (hidden). Type a new one to replace it." : "Only needed for private repositories."}
        >
          <div className="flex gap-2">
            <Input
              aria-label="Access token"
              type="password"
              autoComplete="off"
              value={token}
              disabled={clearToken}
              onChange={(e) => setToken(e.target.value)}
              placeholder={app.hasGitToken && !clearToken ? "•••••• saved" : ""}
              className="min-w-0 flex-1"
            />
            {app.hasGitToken && (
              <Button variant="secondary" size="sm" className="self-center" onClick={() => setClearToken(!clearToken)}>
                {clearToken ? "Keep token" : "Remove"}
              </Button>
            )}
          </div>
        </Field>
        <div className="flex items-start gap-3">
          <Switch label="Deploy new commits automatically" checked={autoDeploy} onChange={setAutoDeploy} />
          <span>
            Deploy new commits automatically
            <span className="block text-xs text-ink-muted">LPS checks the branch every few minutes. A commit that fails to deploy isn't retried until you press Redeploy.</span>
          </span>
        </div>
        <ErrorBox error={save.error} />
        <Button type="submit" size="sm" className="self-start" disabled={!dirty || save.isPending}>
          {save.isPending ? "Saving…" : "Save"}
        </Button>
      </form>
    </Card>
  );
}
