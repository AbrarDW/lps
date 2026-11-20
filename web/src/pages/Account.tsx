import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { Button, Callout, Card, CopyField, ErrorBox, Field, Input, PageHeader, Tag } from "../components/ui";

/** Shows freshly made recovery codes once, with copy and download. */
function RecoveryCodes({ codes, onDone }: { codes: string[]; onDone: () => void }) {
  const [saved, setSaved] = useState(false);
  const text = `LPS recovery codes (each works once)\n${new Date().toLocaleString()}\n\n${codes.join("\n")}\n`;
  return (
    <div className="flex flex-col gap-3">
      <Callout intent="warning">
        <b>Save these recovery codes now.</b> They're shown only once. Each one signs you in a single time if you lose your phone.
      </Callout>
      <ol aria-label="Recovery codes" className="m-0 grid list-none grid-cols-2 gap-2 rounded-bp border border-line bg-ground-deep p-3 font-mono text-[15px] sm:grid-cols-5">
        {codes.map((c) => (
          <li key={c} className="text-center">
            {c}
          </li>
        ))}
      </ol>
      <div className="flex flex-wrap gap-2">
        <Button variant="secondary" size="sm" onClick={() => navigator.clipboard?.writeText(codes.join("\n"))}>
          Copy all
        </Button>
        <a
          className="inline-flex h-[30px] items-center rounded-bp border border-[#2f5f92] px-2.5 text-[13px] text-ink no-underline hover:bg-white/5"
          href={`data:text/plain;charset=utf-8,${encodeURIComponent(text)}`}
          download="lps-recovery-codes.txt"
        >
          Download .txt
        </a>
      </div>
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" className="accent-[#2d72d2]" checked={saved} onChange={(e) => setSaved(e.target.checked)} />
        I saved my recovery codes somewhere safe
      </label>
      <Button className="self-start" disabled={!saved} onClick={onDone}>
        Done
      </Button>
    </div>
  );
}

/** Turning two-factor sign-in on: password → scan QR → confirm a code → recovery codes. */
function EnableFlow({ onDone }: { onDone: () => void }) {
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const setup = useMutation({ mutationFn: () => api.setup2fa(password) });
  const enable = useMutation({ mutationFn: () => api.enable2fa(code) });

  if (enable.data) return <RecoveryCodes codes={enable.data.recoveryCodes} onDone={onDone} />;

  if (!setup.data) {
    return (
      <form
        className="flex flex-col gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          setup.mutate();
        }}
      >
        <Field label="Your password" hint="To make sure it's you.">
          <Input type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required autoFocus />
        </Field>
        <ErrorBox error={setup.error} />
        <Button type="submit" className="self-start" disabled={setup.isPending}>
          Continue
        </Button>
      </form>
    );
  }

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        enable.mutate();
      }}
    >
      <ol className="m-0 flex list-decimal flex-col gap-4 pl-5 text-[13px] text-ink-2">
        <li>
          In an authenticator app (Google Authenticator, Microsoft Authenticator, 1Password, Bitwarden, Authy…), add an account and scan this code:
          <div className="mt-3 flex flex-wrap items-start gap-4">
            <img src={setup.data.qr} alt="QR code for your authenticator app" width={200} height={200} className="rounded-[2px] bg-white p-2" style={{ imageRendering: "pixelated" }} />
            <div className="flex min-w-0 flex-[1_1_240px] flex-col gap-1.5">
              <span className="text-xs text-ink-muted">Can't scan? Enter this key instead (time-based):</span>
              <CopyField value={setup.data.secret.replace(/(.{4})/g, "$1 ").trim()} />
              <a href={setup.data.uri} className="text-xs">
                Open in an authenticator app on this device
              </a>
            </div>
          </div>
        </li>
        <li>
          Enter the 6-digit code the app shows:
          <div className="mt-2 max-w-48">
            <Input
              aria-label="Code from the app"
              value={code}
              onChange={(e) => setCode(e.target.value)}
              inputMode="numeric"
              autoComplete="one-time-code"
              placeholder="123 456"
              className="font-mono text-base tracking-[0.2em]"
              required
            />
          </div>
        </li>
      </ol>
      <ErrorBox error={enable.error} />
      <Button type="submit" className="self-start" disabled={enable.isPending || !code.trim()}>
        Turn on two-factor sign-in
      </Button>
    </form>
  );
}

/** Password + code form for turning 2FA off or making new recovery codes. */
function VerifyForm({ action, danger, onSubmit, pending, error }: { action: string; danger?: boolean; onSubmit: (pw: string, code: string) => void; pending: boolean; error: unknown }) {
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  return (
    <form
      className="flex flex-col gap-3"
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit(password, code);
      }}
    >
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Password">
          <Input type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required autoFocus />
        </Field>
        <Field label="Code from the app, or a recovery code">
          <Input value={code} onChange={(e) => setCode(e.target.value)} autoComplete="one-time-code" className="font-mono" required />
        </Field>
      </div>
      <ErrorBox error={error} />
      <Button type="submit" variant={danger ? "danger" : "primary"} className="self-start" disabled={pending}>
        {action}
      </Button>
    </form>
  );
}

export function AccountPage() {
  const qc = useQueryClient();
  const me = useQuery({ queryKey: ["me"], queryFn: api.me });
  const tf = useQuery({ queryKey: ["2fa"], queryFn: api.twoFactor });
  const [mode, setMode] = useState<"" | "enable" | "disable" | "codes">("");
  const refresh = () => {
    setMode("");
    qc.invalidateQueries({ queryKey: ["2fa"] });
    qc.invalidateQueries({ queryKey: ["me"] });
  };
  const disable = useMutation({ mutationFn: ({ pw, code }: { pw: string; code: string }) => api.disable2fa(pw, code), onSuccess: refresh });
  const codes = useMutation({ mutationFn: ({ pw, code }: { pw: string; code: string }) => api.newRecoveryCodes(pw, code) });

  const on = tf.data?.enabled;
  return (
    <>
      <PageHeader sheet="Sheet 06 — Account" title="Account" subtitle={me.data ? `Signed in as ${me.data.username}` : undefined} />
      <Card
        title="Two-factor sign-in"
        marks
        actions={tf.data && (on ? <Tag intent="success" dot>On</Tag> : <Tag intent="warning" dot>Off</Tag>)}
      >
        <div className="flex flex-col gap-4 text-[13px]">
          <ErrorBox error={tf.error} />
          <p className="m-0 text-ink-2">
            With two-factor sign-in, your password alone isn't enough: LPS also asks for a 6-digit code from an app on your phone. Turn it on before
            you reach LPS from other devices through remote access.
          </p>

          {on && mode === "" && (
            <>
              <p className="m-0">
                Recovery codes left: <b>{tf.data?.recoveryCodesLeft}</b>
                {tf.data && tf.data.recoveryCodesLeft <= 3 && <span className="text-warning-ink"> · make new ones soon</span>}
              </p>
              <div className="flex flex-wrap gap-2">
                <Button variant="secondary" onClick={() => setMode("codes")}>
                  New recovery codes
                </Button>
                <Button variant="danger" onClick={() => setMode("disable")}>
                  Turn off
                </Button>
              </div>
            </>
          )}
          {!on && mode === "" && tf.data && (
            <Button className="self-start" onClick={() => setMode("enable")}>
              Turn on two-factor sign-in
            </Button>
          )}

          {mode === "enable" && <EnableFlow onDone={refresh} />}
          {mode === "disable" && (
            <VerifyForm action="Turn off two-factor sign-in" danger pending={disable.isPending} error={disable.error} onSubmit={(pw, code) => disable.mutate({ pw, code })} />
          )}
          {mode === "codes" &&
            (codes.data ? (
              <RecoveryCodes
                codes={codes.data.recoveryCodes}
                onDone={() => {
                  codes.reset();
                  refresh();
                }}
              />
            ) : (
              <VerifyForm action="Make new recovery codes" pending={codes.isPending} error={codes.error} onSubmit={(pw, code) => codes.mutate({ pw, code })} />
            ))}
          {mode !== "" && !(mode === "codes" && codes.data) && (
            <button type="button" className="cursor-pointer self-start border-0 bg-transparent p-0 text-ink-muted hover:text-ink" onClick={() => setMode("")}>
              Cancel
            </button>
          )}

          <p className="m-0 border-t border-line-soft pt-3 text-xs text-ink-faint">
            Lost your phone and your recovery codes? On this PC, run <code>lpsd disable-2fa</code> (in the LPS install folder) to turn two-factor sign-in
            off, then sign in with your password.
          </p>
        </div>
      </Card>
    </>
  );
}
