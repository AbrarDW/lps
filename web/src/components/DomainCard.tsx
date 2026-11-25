import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, ApiError, type CloudflareAccount } from "../api";
import {
  Button,
  Callout,
  Card,
  ErrorBox,
  Field,
  Input,
  Select,
  StateBadge,
} from "./ui";

/** Remote access page: connect a Cloudflare account so apps can use your own domain. */
export function DomainCard() {
  const qc = useQueryClient();
  const cf = useQuery({
    queryKey: ["cloudflare"],
    queryFn: api.cloudflare,
    refetchInterval: (q) =>
      q.state.data?.connector?.state === "connected" ? 15000 : 3000,
  });
  const [token, setToken] = useState("");
  const [accounts, setAccounts] = useState<CloudflareAccount[] | null>(null);
  const [account, setAccount] = useState("");
  const [warning, setWarning] = useState("");
  const connect = useMutation({
    mutationFn: () => api.connectCloudflare(token.trim(), account || undefined),
    onSuccess: (v) => {
      qc.setQueryData(["cloudflare"], v);
      setToken("");
      setAccounts(null);
      setAccount("");
    },
    onError: (e) => {
      const list =
        e instanceof ApiError
          ? (e.data?.accounts as CloudflareAccount[] | undefined)
          : undefined;
      if (list?.length) {
        setAccounts(list);
        setAccount(list[0].id);
      }
    },
  });
  const disconnect = useMutation({
    mutationFn: api.disconnectCloudflare,
    onSuccess: (r) => {
      qc.setQueryData(["cloudflare"], r.view);
      qc.invalidateQueries({ queryKey: ["apps"] });
      setWarning(r.warning ?? "");
    },
  });

  const v = cf.data;
  return (
    <Card
      title="Your domain · Cloudflare"
      actions={
        v?.connected &&
        v.connector && (
          <StateBadge
            state={
              v.connector.state === "missing" ? "connecting" : v.connector.state
            }
          />
        )
      }
    >
      <div className="flex flex-col gap-4 text-[13px]">
        <ErrorBox error={cf.error} />
        {warning && <Callout intent="warning">{warning}</Callout>}
        {v && !v.connected && (
          <>
            <p className="m-0 text-ink-2">
              Give apps an address on your own domain, like{" "}
              <code>https://blog.yourdomain.com</code>, that works for anyone
              and never changes. LPS sets up a Cloudflare Tunnel in your
              Cloudflare account: no ports are opened on this PC or your router,
              and Cloudflare handles HTTPS. Works with Cloudflare's free plan,
              without Tailscale.
            </p>
            <ol className="m-0 flex list-decimal flex-col gap-2 pl-5 text-ink-2">
              <li>
                Your domain must use Cloudflare for its DNS (
                <a
                  href="https://dash.cloudflare.com/"
                  target="_blank"
                  rel="noreferrer"
                >
                  add it to Cloudflare
                </a>{" "}
                if it doesn't yet).
              </li>
              <li>
                In Cloudflare, open{" "}
                <a
                  href="https://dash.cloudflare.com/profile/api-tokens"
                  target="_blank"
                  rel="noreferrer"
                >
                  My Profile → API Tokens
                </a>{" "}
                and choose <b>Create Token → Create Custom Token</b>. Give it
                these permissions:
                <ul className="mt-1.5 mb-0 list-none space-y-0.5 pl-0 font-mono text-xs text-ink">
                  <li>Account · Cloudflare Tunnel · Edit</li>
                  <li>Zone · DNS · Edit</li>
                  <li>Zone · Zone · Read</li>
                </ul>
                <span className="text-xs text-ink-muted">
                  Under Zone Resources, pick your domain (or all zones).
                </span>
              </li>
              <li>
                Paste the token here. LPS keeps it on this PC and never shows it
                again.
              </li>
            </ol>
            <form
              className="flex flex-col gap-3"
              onSubmit={(e) => {
                e.preventDefault();
                connect.mutate();
              }}
            >
              <Field label="Cloudflare API token">
                <Input
                  type="password"
                  autoComplete="off"
                  spellCheck={false}
                  value={token}
                  onChange={(e) => setToken(e.target.value)}
                  required
                  className="font-mono"
                />
              </Field>
              {accounts && (
                <Field
                  label="Cloudflare account"
                  hint="The token can see domains in more than one account. LPS uses one."
                >
                  <Select
                    value={account}
                    onChange={(e) => setAccount(e.target.value)}
                  >
                    {accounts.map((a) => (
                      <option key={a.id} value={a.id}>
                        {a.name}
                      </option>
                    ))}
                  </Select>
                </Field>
              )}
              {!(
                accounts &&
                connect.error instanceof ApiError &&
                connect.error.status === 409
              ) && <ErrorBox error={connect.error} />}
              <Button
                type="submit"
                className="self-start"
                disabled={connect.isPending || !token.trim()}
              >
                {connect.isPending ? "Connecting…" : "Connect Cloudflare"}
              </Button>
            </form>
          </>
        )}
        {v?.connected && (
          <>
            <dl className="m-0 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1.5">
              <dt className="text-ink-muted">Account</dt>
              <dd className="m-0">{v.accountName}</dd>
              <dt className="text-ink-muted">Tunnel</dt>
              <dd className="m-0 font-mono text-xs break-all">
                {v.tunnelName}
              </dd>
              <dt className="text-ink-muted">Domains</dt>
              <dd className="m-0">
                {v.zones.length ? (
                  v.zones.map((z) => z.name).join(", ")
                ) : v.zonesError ? (
                  <span className="text-danger-ink">{v.zonesError}</span>
                ) : (
                  "none"
                )}
              </dd>
            </dl>
            {v.connector?.state !== "connected" && (
              <p className="m-0 text-ink-muted">
                {v.connector?.error
                  ? `Connector: ${v.connector.error}`
                  : "Starting the connector… The first time, Docker downloads cloudflared, which can take a minute."}
              </p>
            )}
            {v.error && <Callout intent="warning">{v.error}</Callout>}
            {v.domains.length > 0 ? (
              <ul className="m-0 list-none border-y border-line-soft p-0">
                {v.domains.map((d) => (
                  <li
                    key={d.appId}
                    className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-line-soft py-2 last:border-b-0"
                  >
                    <Link to={`/apps/${d.appId}`} className="min-w-24">
                      {d.appName}
                    </Link>
                    <a
                      href={d.url}
                      target="_blank"
                      rel="noreferrer"
                      className="font-mono text-xs break-all"
                    >
                      {d.url}
                    </a>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="m-0 text-ink-muted">
                No apps use your domain yet. Open an app and set its address
                under Public link.
              </p>
            )}
            <Button
              variant="secondary"
              size="sm"
              className="self-start"
              disabled={disconnect.isPending}
              onClick={() => {
                if (
                  confirm(
                    "Disconnect Cloudflare?\n\nEvery app address on your domain stops working. LPS deletes the DNS records and the tunnel it created in your Cloudflare account.",
                  )
                ) {
                  setWarning("");
                  disconnect.mutate();
                }
              }}
            >
              {disconnect.isPending ? "Disconnecting…" : "Disconnect"}
            </Button>
            <ErrorBox error={disconnect.error} />
          </>
        )}
      </div>
    </Card>
  );
}
