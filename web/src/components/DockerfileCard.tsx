import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { Button, Card, ErrorBox, LogView, TextArea } from "./ui";

/** Shows the Dockerfile an app builds with, and lets the user edit an LPS-managed one. */
export function DockerfileCard({ appId, deploying, bare }: { appId: number; deploying?: boolean; bare?: boolean }) {
  const qc = useQueryClient();
  const df = useQuery({ queryKey: ["apps", appId, "dockerfile"], queryFn: () => api.appDockerfile(appId) });
  const [text, setText] = useState("");
  const [editing, setEditing] = useState(false);

  useEffect(() => {
    if (df.data && !editing) setText(df.data.source === "lps" ? df.data.dockerfile : df.data.suggested?.dockerfile ?? "");
  }, [df.data, editing]);

  const save = useMutation({
    mutationFn: (value: string) => api.setAppDockerfile(appId, value),
    onSuccess: () => {
      setEditing(false);
      qc.invalidateQueries({ queryKey: ["apps", appId] });
    },
  });

  if (!df.data) return null;
  const d = df.data;
  const lps = d.source === "lps";
  const dirty = lps ? text !== d.dockerfile : true;

  const body = (
      <div className="space-y-3 text-sm">
        {lps ? (
          <>
            <p className="text-ink-muted">
              Stored in LPS (your project folder is not changed). Edit it and press <b>Save &amp; redeploy</b>.
            </p>
            <TextArea
              aria-label="Dockerfile"
              rows={Math.min(24, text.split("\n").length + 1)}
              value={text}
              spellCheck={false}
              onChange={(e) => {
                setEditing(true);
                setText(e.target.value);
              }}
            />
            <div className="flex flex-wrap gap-2">
              <Button disabled={!dirty || save.isPending || deploying} onClick={() => save.mutate(text)}>
                {save.isPending ? "Saving…" : "Save & redeploy"}
              </Button>
              {editing && (
                <Button variant="secondary" onClick={() => setEditing(false)}>
                  Discard changes
                </Button>
              )}
              {d.projectHasDockerfile && (
                <Button
                  variant="secondary"
                  disabled={save.isPending || deploying}
                  onClick={() => {
                    if (confirm("Build with the Dockerfile in the project folder instead? The one stored in LPS will be removed.")) save.mutate("");
                  }}
                >
                  Use the project's Dockerfile instead
                </Button>
              )}
            </div>
          </>
        ) : d.projectHasDockerfile ? (
          <>
            <p className="text-ink-muted">
              Using the <code>Dockerfile</code> in your project folder. Edit it in your editor, then press <b>Redeploy</b>.
            </p>
            <LogView text={d.dockerfile} className="!max-h-[28rem]" />
          </>
        ) : (
          <>
            <p className="text-warning-ink">
              The project folder no longer has a Dockerfile. Save this generated one in LPS to keep deploying.
            </p>
            <TextArea aria-label="Dockerfile" rows={12} value={text} spellCheck={false} onChange={(e) => setText(e.target.value)} />
            <Button disabled={save.isPending || !text.trim()} onClick={() => save.mutate(text)}>
              Save &amp; redeploy
            </Button>
          </>
        )}
        <ErrorBox error={save.error} />
      </div>
  );
  return bare ? body : <Card title="Dockerfile">{body}</Card>;
}
