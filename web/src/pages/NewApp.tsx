import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { api, type Detection, type RunSetup } from "../api";
import { Button, Callout, Card, ErrorBox, Field, Input, PageHeader, Select, TextArea } from "../components/ui";

const absPath = (p: string) => /^([a-zA-Z]:[\\/]|\\\\|\/)/.test(p.trim());

function useDebounced<T>(value: T, ms: number) {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return v;
}

type CodeFrom = "pc" | "git" | "vps";

/** Template labels name the Docker images they use; drop that when running in a VPS. */
const vpsLabel = (label: string) => label.replace(/ \((nginx|Apache)\)$/, "");

export function NewApp() {
  const nav = useNavigate();
  const qc = useQueryClient();
  const [params] = useSearchParams();
  const [form, setForm] = useState({ name: "", sourcePath: "", containerPort: "80", cpus: "0", memoryMb: "0" });
  const [portTouched, setPortTouched] = useState(false);
  const [template, setTemplate] = useState<Detection | null>(null);
  const [dockerfile, setDockerfile] = useState("");
  const [saveToProject, setSaveToProject] = useState(false);
  const set = (k: keyof typeof form) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });

  // Where the app runs: "" = its own container, else a VPS id.
  const [target, setTarget] = useState(params.get("vps") ?? "");
  const [codeFrom, setCodeFrom] = useState<CodeFrom>("pc");
  const [vpsPath, setVpsPath] = useState("~/");
  const [run, setRun] = useState({ type: "", packages: "", install: "", start: "" });
  const vpsList = useQuery({ queryKey: ["vps"], queryFn: api.listVPS });
  const inVPS = target !== "";
  const vps = vpsList.data?.find((v) => String(v.id) === target);
  const usePCFolder = codeFrom === "pc";
  const useGit = codeFrom === "git";
  // A folder inside a VPS only makes sense for apps that run in one.
  useEffect(() => {
    if (!inVPS && codeFrom === "vps") setCodeFrom("pc");
  }, [inVPS, codeFrom]);

  const [git, setGit] = useState({ url: "", branch: "", token: "", autoDeploy: false });
  const gitKey = `${git.url.trim()}|${git.branch.trim()}|${git.token.trim()}`;
  const gitCheck = useMutation({ mutationFn: () => api.detectGit(git.url.trim(), git.branch.trim(), git.token.trim()) });
  const [checkedKey, setCheckedKey] = useState("");
  const gitChecked = gitCheck.isSuccess && checkedKey === gitKey;

  const path = useDebounced(form.sourcePath.trim(), 500);
  const detect = useQuery({
    queryKey: ["detect", path],
    queryFn: () => api.detectApp(path),
    enabled: usePCFolder && absPath(path),
    retry: false,
  });
  const templates = useQuery({ queryKey: ["app-templates"], queryFn: api.appTemplates, enabled: inVPS && codeFrom === "vps" });
  // What we know about the code: from the folder, or from the checked repository.
  const det = useGit ? (gitChecked ? gitCheck.data : undefined) : usePCFolder ? detect.data : undefined;
  const needsDockerfile = !inVPS && det && !det.hasDockerfile;

  function applyRun(type: string, r: RunSetup) {
    setRun({ type, packages: r.packages, install: r.install, start: r.start });
    if (!portTouched) setForm((f) => ({ ...f, containerPort: String(r.port) }));
  }

  // Load the detected setup whenever a new folder is detected or the target changes.
  useEffect(() => {
    const d = det;
    if (!d) return;
    if (inVPS) {
      applyRun(d.detection.type, d.detection.run);
      return;
    }
    if (d.hasDockerfile) {
      setTemplate(null);
      return;
    }
    applyTemplate(d.detection);
  }, [det, inVPS]);

  function applyTemplate(t: Detection) {
    setTemplate(t);
    setDockerfile(t.dockerfile);
    if (!portTouched) setForm((f) => ({ ...f, containerPort: String(t.port) }));
  }

  const runChoices = codeFrom === "vps" ? templates.data : det?.templates;
  const runNotes = runChoices?.find((t) => t.type === run.type)?.run.notes;

  const m = useMutation({
    mutationFn: () =>
      api.createApp({
        name: form.name.trim(),
        sourcePath: usePCFolder ? form.sourcePath.trim() : "",
        containerPort: Number(form.containerPort),
        cpus: inVPS ? 0 : Number(form.cpus) || 0,
        memoryMb: inVPS ? 0 : Number(form.memoryMb) || 0,
        ...(needsDockerfile ? { dockerfile, saveToProject: saveToProject && usePCFolder } : {}),
        ...(useGit
          ? { sourceKind: "git" as const, gitUrl: git.url.trim(), gitBranch: git.branch.trim(), gitToken: git.token.trim(), autoDeploy: git.autoDeploy }
          : {}),
        ...(inVPS
          ? { vpsId: Number(target), sourceKind: codeFrom, vpsPath: codeFrom === "vps" ? vpsPath.trim() : "", packages: run.packages, installCmd: run.install, startCmd: run.start }
          : {}),
      }),
    onSuccess: (a) => {
      qc.invalidateQueries({ queryKey: ["apps"] });
      qc.invalidateQueries({ queryKey: ["vps"] });
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
            {inVPS ? (
              <>
                LPS puts the code in <b>{vps?.name ?? "the VPS"}</b>, installs what it needs, runs your start command and keeps it running. The app gets
                its own links, like any app.
              </>
            ) : (
              <>
                LPS builds your project folder and runs it as a Linux container. If the folder has no <code>Dockerfile</code>, LPS detects the
                project type and writes one for you to review.
              </>
            )}
          </p>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Name">
              <Input value={form.name} onChange={set("name")} placeholder="my-site" required pattern="[a-z0-9][a-z0-9\-]{1,30}" />
            </Field>
            <Field label="Run on" hint={inVPS ? "Runs as a process inside the VPS, next to anything you run there yourself." : "A fresh container built from a Dockerfile."}>
              <Select aria-label="Run on" value={target} onChange={(e) => setTarget(e.target.value)}>
                <option value="">Its own container</option>
                {vpsList.data?.map((v) => (
                  <option key={v.id} value={String(v.id)}>
                    VPS {v.name}
                    {v.state !== "running" ? ` (${v.state})` : ""}
                  </option>
                ))}
              </Select>
            </Field>
          </div>
          {inVPS && vps && vps.state !== "running" && <Callout intent="warning">VPS {vps.name} is {vps.state}. Start it before deploying.</Callout>}

          {(
            <fieldset className="flex flex-wrap gap-x-6 gap-y-2 border-0 p-0">
              <legend className="mb-1.5 text-[13px] font-medium">Code from</legend>
              {(
                [
                  ["pc", "A folder on this PC", inVPS ? "Copied into the VPS on every deploy" : "Built on every deploy"],
                  ["git", "A git repository", "GitHub, GitLab… cloned on every deploy"],
                  ...(inVPS ? ([["vps", "A folder inside the VPS", "e.g. your own clone; Redeploy re-runs install and start"]] as const) : []),
                ] as const
              ).map(([value, label, hint]) => (
                <label key={value} className="flex items-start gap-2 text-sm">
                  <input type="radio" name="codeFrom" className="mt-1 accent-[#2d72d2]" checked={codeFrom === value} onChange={() => setCodeFrom(value)} />
                  <span>
                    {label}
                    <span className="block text-xs text-ink-muted">{hint}</span>
                  </span>
                </label>
              ))}
            </fieldset>
          )}

          {usePCFolder ? (
            <Field label="Project folder on this PC" hint="Absolute path, e.g. C:\Users\you\projects\my-site or /home/you/my-site">
              <Input value={form.sourcePath} onChange={set("sourcePath")} required />
            </Field>
          ) : useGit ? (
            <div className="flex flex-col gap-3">
              <Field label="Repository URL" hint="The HTTPS clone URL, e.g. https://github.com/you/my-site.git">
                <Input
                  aria-label="Repository URL"
                  value={git.url}
                  onChange={(e) => setGit({ ...git, url: e.target.value })}
                  placeholder="https://github.com/you/my-site.git"
                  className="font-mono"
                  required
                  spellCheck={false}
                />
              </Field>
              <div className="grid gap-4 sm:grid-cols-2">
                <Field label="Branch" hint="Empty = the repository's default branch">
                  <Input aria-label="Branch" value={git.branch} onChange={(e) => setGit({ ...git, branch: e.target.value })} placeholder="default branch" className="font-mono" spellCheck={false} />
                </Field>
                <Field label="Access token (private repositories)" hint="A token with read access, e.g. a GitHub fine-grained token with Contents: read. Stored on this PC, never shown again.">
                  <Input aria-label="Access token" type="password" autoComplete="off" value={git.token} onChange={(e) => setGit({ ...git, token: e.target.value })} />
                </Field>
              </div>
              <label className="flex items-start gap-2 text-sm">
                <input type="checkbox" className="mt-1 accent-[#2d72d2]" checked={git.autoDeploy} onChange={(e) => setGit({ ...git, autoDeploy: e.target.checked })} />
                <span>
                  Deploy new commits automatically
                  <span className="block text-xs text-ink-muted">LPS checks the branch every few minutes; nothing needs to reach this PC from the internet.</span>
                </span>
              </label>
              <div className="flex flex-wrap items-center gap-3">
                <Button
                  variant="secondary"
                  disabled={!git.url.trim() || gitCheck.isPending}
                  onClick={() => {
                    setCheckedKey(gitKey);
                    gitCheck.mutate();
                  }}
                >
                  {gitCheck.isPending ? "Checking…" : "Check repository"}
                </Button>
                {gitChecked && gitCheck.data && (
                  <span className="text-[13px] text-success-ink">
                    ✓ {gitCheck.data.commit.branch} at <code>{gitCheck.data.commit.hash.slice(0, 7)}</code>: {gitCheck.data.commit.message}
                  </span>
                )}
                {!gitChecked && !gitCheck.isPending && git.url.trim() && <span className="text-xs text-ink-muted">Check it to see how LPS will build it.</span>}
              </div>
              {checkedKey === gitKey && <ErrorBox error={gitCheck.error} />}
            </div>
          ) : (
            <Field label={`Folder inside VPS ${vps?.name ?? ""}`} hint={<>Absolute, or relative to your home folder with <code>~/</code>, e.g. <code>~/my-site</code></>}>
              <Input aria-label="Folder inside the VPS" value={vpsPath} onChange={(e) => setVpsPath(e.target.value)} required className="font-mono" />
            </Field>
          )}

          {usePCFolder && detect.isFetching && <p className="text-[13px] text-ink-muted">Looking at the folder…</p>}
          {usePCFolder && detect.error && <ErrorBox error={detect.error} />}
          {!inVPS && det?.hasDockerfile && (
            <p className="rounded-[2px] border border-success/45 bg-success/12 px-3 py-2 text-[13px] text-success-ink">
              ✓ Found a Dockerfile {useGit ? "in the repository" : "in this folder"}. LPS will use it.
            </p>
          )}
          {needsDockerfile && template && (
            <div className="relative space-y-3 rounded-bp border border-cyan/45 bg-primary/8 p-4">
              <div>
                <div className="font-medium">No Dockerfile found: LPS made one for you</div>
                <div className="text-[13px] text-ink-2">
                  Detected: <b>{det!.detection.label}</b>. Review it, change the template, or edit it below.
                </div>
              </div>
              <Field label="Template">
                <Select
                  aria-label="Template"
                  value={template.type}
                  onChange={(e) => {
                    const t = det!.templates.find((x) => x.type === e.target.value);
                    if (t) applyTemplate(t);
                  }}
                >
                  {det!.templates.map((t) => (
                    <option key={t.type} value={t.type}>
                      {t.label}
                      {t.type === det!.detection.type ? " (detected)" : ""}
                    </option>
                  ))}
                </Select>
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
              {usePCFolder ? (
                <label className="flex items-start gap-2 text-sm">
                  <input type="checkbox" className="mt-1" checked={saveToProject} onChange={(e) => setSaveToProject(e.target.checked)} />
                  <span>
                    Also save this Dockerfile into the project folder
                    <span className="block text-xs text-ink-muted">
                      Creates <code>Dockerfile</code> (and <code>.dockerignore</code> if missing) so you can edit it in your editor and commit it. Off: LPS
                      keeps it and your folder is not changed.
                    </span>
                  </span>
                </label>
              ) : (
                <p className="m-0 text-xs text-ink-muted">
                  LPS keeps this Dockerfile and uses it for every commit. To manage it in git instead, commit a <code>Dockerfile</code> to the repository.
                </p>
              )}
            </div>
          )}

          {inVPS && (
            <div className="relative space-y-3 rounded-bp border border-cyan/45 bg-primary/8 p-4">
              <div>
                <div className="font-medium">How to run it in the VPS</div>
                <div className="text-[13px] text-ink-2">
                  {det ? (
                    <>
                      Detected: <b>{vpsLabel(det.detection.label)}</b>. Check the commands below.
                    </>
                  ) : (
                    "Pick a project type to fill these in, or type your own."
                  )}
                </div>
              </div>
              {runChoices && (
                <Field label="Project type">
                  <Select
                    aria-label="Project type"
                    value={run.type}
                    onChange={(e) => {
                      const t = runChoices.find((x) => x.type === e.target.value);
                      if (t) applyRun(t.type, t.run);
                    }}
                  >
                    {!run.type && <option value="">Choose…</option>}
                    {runChoices.map((t) => (
                      <option key={t.type} value={t.type}>
                        {vpsLabel(t.label)}
                        {t.type === det?.detection.type ? " (detected)" : ""}
                      </option>
                    ))}
                  </Select>
                </Field>
              )}
              <RunSetupFields value={run} onChange={(r) => setRun({ ...run, ...r })} />
              {runNotes && runNotes.length > 0 && (
                <ul className="list-disc space-y-1 pl-5 text-xs text-ink-2">
                  {runNotes.map((n) => (
                    <li key={n}>{n}</li>
                  ))}
                </ul>
              )}
              {(usePCFolder || useGit) && (
                <p className="m-0 text-xs text-ink-muted">
                  The {useGit ? "repository" : "folder"} is copied to <code>~/apps/{form.name || "<name>"}</code> and replaced on every deploy, so keep data the app writes somewhere else (e.g.{" "}
                  <code>~/data</code>).
                </p>
              )}
            </div>
          )}

          <div className={`grid gap-4 ${inVPS ? "sm:grid-cols-2" : "sm:grid-cols-3"}`}>
            <Field label={inVPS ? "App port in the VPS" : "App port in container"} hint={inVPS ? "The port your app listens on; also given to it as $PORT" : "The port your app listens on"}>
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
            {!inVPS && (
              <>
                <Field label="CPU limit" hint="0 = no limit">
                  <Input type="number" min={0} max={64} step={0.5} value={form.cpus} onChange={set("cpus")} />
                </Field>
                <Field label="Memory limit (MB)" hint="0 = no limit">
                  <Input type="number" min={0} step={128} value={form.memoryMb} onChange={set("memoryMb")} />
                </Field>
              </>
            )}
          </div>
          <ErrorBox error={m.error} />
          <div className="flex gap-2">
            <Button type="submit" disabled={m.isPending || (usePCFolder && detect.isFetching) || (useGit && !gitChecked)}>
              {m.isPending ? "Creating…" : inVPS ? "Create and deploy to VPS" : "Create and deploy"}
            </Button>
            <Link to="/apps" className="inline-flex h-9 items-center px-3 text-ink-muted no-underline hover:text-ink">
              Cancel
            </Link>
          </div>
        </form>
      </Card>
    </>
  );
}

/** Packages / install / start inputs for an app that runs inside a VPS. */
export function RunSetupFields({
  value,
  onChange,
}: {
  value: { packages: string; install: string; start: string };
  onChange: (v: Partial<{ packages: string; install: string; start: string }>) => void;
}) {
  return (
    <>
      <Field label="System packages" hint="Ubuntu packages, installed with apt if missing. Leave empty if the VPS already has what the app needs.">
        <Input aria-label="System packages" value={value.packages} onChange={(e) => onChange({ packages: e.target.value })} placeholder="nodejs npm" className="font-mono" />
      </Field>
      <Field label="Install command" hint="Runs in the app folder on every deploy, as your VPS user. Optional.">
        <Input aria-label="Install command" value={value.install} onChange={(e) => onChange({ install: e.target.value })} placeholder="npm ci" className="font-mono" />
      </Field>
      <Field label="Start command" hint="Keeps running; LPS restarts it if it stops or the VPS restarts. It should listen on 0.0.0.0 and $PORT.">
        <Input aria-label="Start command" value={value.start} onChange={(e) => onChange({ start: e.target.value })} placeholder="npm start" className="font-mono" required />
      </Field>
    </>
  );
}
