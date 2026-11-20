import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { Button, Card, ErrorBox, Field, Input } from "../components/ui";

export function AuthPage({ mode }: { mode: "setup" | "login" }) {
  const qc = useQueryClient();
  return (
    <div className="grid min-h-screen place-items-center px-4">
      <div className="w-full max-w-sm space-y-4">
        <div className="text-center">
          <div className="mx-auto mb-3 grid size-13 place-items-center rounded-bp border-2 border-cyan font-mono text-[15px] font-medium text-cyan">LPS</div>
          <h1 className="text-xl font-semibold">{mode === "setup" ? "Welcome to LPS" : "Sign in"}</h1>
          <p className="text-[13px] text-ink-muted">
            {mode === "setup"
              ? "Create the admin account. Use a strong password: this dashboard can be reached from the internet."
              : "Local Personal Server dashboard"}
          </p>
        </div>
        <Card>
          <CredentialsForm mode={mode} onDone={() => qc.invalidateQueries()} />
        </Card>
      </div>
    </div>
  );
}

/** Username/password form used for login and for creating the first admin. */
export function CredentialsForm({ mode, onDone }: { mode: "setup" | "login"; onDone: () => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [localError, setLocalError] = useState("");
  const [ticket, setTicket] = useState("");

  const m = useMutation({
    mutationFn: () => (mode === "setup" ? api.setup(username, password) : api.login(username, password)),
    onSuccess: (res) => {
      if ("twoFactorRequired" in res && res.twoFactorRequired) {
        setTicket(res.ticket);
        return;
      }
      onDone();
    },
  });

  if (ticket) {
    return (
      <TwoFactorStep
        ticket={ticket}
        onDone={onDone}
        onRestart={() => {
          setTicket("");
          setPassword("");
          m.reset();
        }}
      />
    );
  }

  return (
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          setLocalError("");
          if (mode === "setup" && password !== confirm) {
            setLocalError("Passwords do not match");
            return;
          }
          m.mutate();
        }}
      >
        <Field label="Username">
          <Input autoFocus autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} required />
        </Field>
        <Field label="Password" hint={mode === "setup" ? "At least 10 characters." : undefined}>
          <Input
            type="password"
            autoComplete={mode === "setup" ? "new-password" : "current-password"}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
        </Field>
        {mode === "setup" && (
          <Field label="Confirm password">
            <Input type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required />
          </Field>
        )}
        <ErrorBox error={localError || m.error} />
        <Button type="submit" className="w-full" disabled={m.isPending}>
          {m.isPending ? "Please wait…" : mode === "setup" ? "Create admin account" : "Sign in"}
        </Button>
      </form>
  );
}

/** Second sign-in step: a code from the authenticator app, or a recovery code. */
function TwoFactorStep({ ticket, onDone, onRestart }: { ticket: string; onDone: () => void; onRestart: () => void }) {
  const [code, setCode] = useState("");
  const [recovery, setRecovery] = useState(false);
  const m = useMutation({ mutationFn: () => api.login2fa(ticket, code), onSuccess: onDone });
  const expired = m.error instanceof Error && /expired|password again/.test(m.error.message);
  return (
    <form
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault();
        m.mutate();
      }}
    >
      <p className="m-0 text-[13px] text-ink-2">
        {recovery
          ? "Enter one of the recovery codes you saved when you turned on two-factor sign-in. Each code works once."
          : "Open your authenticator app and enter the 6-digit code for LPS."}
      </p>
      <Field label={recovery ? "Recovery code" : "Code"}>
        <Input
          key={recovery ? "recovery" : "totp"}
          autoFocus
          value={code}
          onChange={(e) => setCode(e.target.value)}
          autoComplete="one-time-code"
          inputMode={recovery ? "text" : "numeric"}
          placeholder={recovery ? "xxxx-xxxx" : "123 456"}
          className="font-mono text-base tracking-[0.2em]"
          required
        />
      </Field>
      <ErrorBox error={m.error} />
      {expired ? (
        <Button type="button" className="w-full" onClick={onRestart}>
          Start again
        </Button>
      ) : (
        <Button type="submit" className="w-full" disabled={m.isPending || !code.trim()}>
          {m.isPending ? "Checking…" : "Verify"}
        </Button>
      )}
      <div className="flex justify-between text-[13px]">
        <button
          type="button"
          className="cursor-pointer border-0 bg-transparent p-0 text-cyan hover:text-cyan-soft"
          onClick={() => {
            setRecovery(!recovery);
            setCode("");
            m.reset();
          }}
        >
          {recovery ? "Use the app instead" : "Use a recovery code"}
        </button>
        <button type="button" className="cursor-pointer border-0 bg-transparent p-0 text-ink-muted hover:text-ink" onClick={onRestart}>
          Back
        </button>
      </div>
    </form>
  );
}
