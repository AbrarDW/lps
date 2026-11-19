import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { Button, Card, ErrorBox, Field, Input } from "../components/ui";

export function AuthPage({ mode }: { mode: "setup" | "login" }) {
  const qc = useQueryClient();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [localError, setLocalError] = useState("");

  const m = useMutation({
    mutationFn: () => (mode === "setup" ? api.setup(username, password) : api.login(username, password)),
    onSuccess: () => qc.invalidateQueries(),
  });

  return (
    <div className="grid min-h-screen place-items-center px-4">
      <div className="w-full max-w-sm space-y-4">
        <div className="text-center">
          <div className="mx-auto mb-3 grid h-12 w-12 place-items-center rounded-xl bg-indigo-600 font-bold text-white">LPS</div>
          <h1 className="text-xl font-semibold">{mode === "setup" ? "Welcome to LPS" : "Sign in"}</h1>
          <p className="text-sm text-slate-500 dark:text-slate-400">
            {mode === "setup"
              ? "Create the admin account. Use a strong password: this dashboard can be reached from the internet."
              : "Local Personal Server dashboard"}
          </p>
        </div>
        <Card>
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
        </Card>
      </div>
    </div>
  );
}
