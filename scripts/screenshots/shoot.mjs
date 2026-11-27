// Sets up a demo in a running lpsd (a VPS, a container app and an app inside
// the VPS) and takes the screenshots in docs/images. Start it through run.sh,
// which builds lpsd and starts it with a fake Tailscale CLI.
//
//   node shoot.mjs <dashboard url> <output folder> <demo projects folder>
//
// Needs Playwright (require("playwright") must work, e.g. through NODE_PATH).
import { createRequire } from "node:module";
import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";

const require = createRequire(import.meta.url);
const { chromium, devices } = require("playwright");

const [base, out, projects] = process.argv.slice(2);
if (!base || !out || !projects) {
  console.error("usage: node shoot.mjs <dashboard url> <output folder> <demo projects folder>");
  process.exit(2);
}
const user = "admin";
const password = "demo-password-123";

const log = (...a) => console.log("[screenshots]", ...a);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function until(what, fn, timeoutMs = 15 * 60_000) {
  const end = Date.now() + timeoutMs;
  for (;;) {
    const v = await fn();
    if (v) return v;
    if (Date.now() > end) throw new Error(`timed out waiting for ${what}`);
    await sleep(2000);
  }
}

// --- API client -------------------------------------------------------------

let cookie = "";
async function api(method, url, body) {
  const res = await fetch(base + "/api" + url, {
    method,
    headers: { "X-LPS-Request": "1", "Content-Type": "application/json", Cookie: cookie },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const set = res.headers.get("set-cookie");
  if (set) cookie = set.split(";")[0];
  const text = await res.text();
  const data = text ? JSON.parse(text) : null;
  if (!res.ok) throw new Error(`${method} ${url}: ${res.status} ${text}`);
  return data;
}

// An OpenSSH ed25519 public key, for the demo VPS and the New VPS form.
function sshPublicKey() {
  const { publicKey } = crypto.generateKeyPairSync("ed25519");
  const raw = publicKey.export({ format: "der", type: "spki" }).subarray(-32);
  // SSH wire format: each field is a 4-byte big-endian length, then the bytes.
  const str = (b) => {
    const len = Buffer.alloc(4);
    len.writeUInt32BE(b.length);
    return Buffer.concat([len, b]);
  };
  const blob = Buffer.concat([str(Buffer.from("ssh-ed25519")), str(raw)]);
  return `ssh-ed25519 ${blob.toString("base64")} you@laptop`;
}

// The current TOTP code for a base32 secret (RFC 6238, SHA-1, 6 digits).
function totp(secret) {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = "";
  for (const c of secret.replace(/[\s=]/g, "").toUpperCase()) bits += alphabet.indexOf(c).toString(2).padStart(5, "0");
  const key = Buffer.from(bits.match(/.{8}/g).map((b) => parseInt(b, 2)));
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30000)));
  const h = crypto.createHmac("sha1", key).update(counter).digest();
  const o = h[h.length - 1] & 15;
  return String((h.readUInt32BE(o) & 0x7fffffff) % 1_000_000).padStart(6, "0");
}

// --- Demo data --------------------------------------------------------------

function writeProjects() {
  const portfolio = path.join(projects, "portfolio");
  const notes = path.join(projects, "notes-api");
  fs.mkdirSync(portfolio, { recursive: true });
  fs.mkdirSync(notes, { recursive: true });
  fs.writeFileSync(
    path.join(portfolio, "index.html"),
    `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Portfolio</title></head>
<body style="font-family:sans-serif;max-width:40rem;margin:4rem auto"><h1>My portfolio</h1><p>Served from a container on my home PC by LPS.</p></body></html>
`,
  );
  fs.writeFileSync(path.join(notes, "package.json"), `{"name":"notes-api","version":"1.0.0","scripts":{"start":"node server.js"}}\n`);
  fs.writeFileSync(
    path.join(notes, "server.js"),
    `const http = require("http");
const port = process.env.PORT || 3000;
http
  .createServer((req, res) => {
    res.setHeader("content-type", "application/json");
    res.end(JSON.stringify({ notes: [], env: process.env.NODE_ENV }));
  })
  .listen(port, "0.0.0.0", () => console.log(\`notes-api listening on \${port}\`));
`,
  );
  return { portfolio, notes };
}

const lastDeploy = async (id) => (await api("GET", `/apps/${id}/deployments`))[0];
const waitDeployed = (id, what) =>
  until(what, async () => {
    const d = await lastDeploy(id);
    if (d?.status === "failed") throw new Error(`${what} failed:\n${d.log}`);
    return d?.status === "running";
  });

async function setUpDemo(pubKey) {
  const dirs = writeProjects();
  log("creating the admin account and turning on remote access");
  await api("POST", "/setup", { username: user, password });
  await api("POST", "/auth/login", { username: user, password });
  await api("PUT", "/remote", { enabled: true });

  log("creating VPS dev-box (the first one builds the Ubuntu image: a few minutes)");
  const vps = await api("POST", "/vps", { name: "dev-box", sshUser: "dev", sshPublicKey: pubKey, cpus: 2, memoryMb: 2048 });
  await until("dev-box to start", async () => {
    const v = await api("GET", `/vps/${vps.id}`);
    if (v.state === "error") throw new Error(`dev-box: ${v.error}`);
    return v.state === "running";
  });
  await api("PUT", `/vps/${vps.id}/snapshot-schedule`, { frequency: "daily", hour: 3, keep: 7 });

  log("deploying portfolio (its own container) and notes-api (inside dev-box)");
  const det = await api("POST", "/apps/detect", { sourcePath: dirs.portfolio });
  const portfolio = await api("POST", "/apps", {
    name: "portfolio", sourceKind: "pc", sourcePath: dirs.portfolio, containerPort: 80, dockerfile: det.detection.dockerfile,
  });
  const notes = await api("POST", "/apps", {
    name: "notes-api", vpsId: vps.id, sourceKind: "pc", sourcePath: dirs.notes, containerPort: 3000,
    packages: "nodejs npm", installCmd: "npm install", startCmd: "npm start",
  });
  await waitDeployed(portfolio.id, "portfolio to deploy");
  await waitDeployed(notes.id, "notes-api to deploy");

  // Saving variables redeploys the app.
  await api("PUT", `/apps/${notes.id}/env`, {
    vars: [
      { name: "NODE_ENV", value: "production" },
      { name: "DATABASE_URL", value: "postgres://notes:s3cret@db.internal:5432/notes", secret: true },
      { name: "LOG_LEVEL", value: "info" },
    ],
  });
  await sleep(1000);
  await waitDeployed(notes.id, "notes-api to redeploy");
  await api("PUT", `/apps/${portfolio.id}/public`, { public: true });
  // A second build of portfolio comes from Docker's cache, so its log is short.
  await api("POST", `/apps/${portfolio.id}/deploy`);
  await sleep(1000);
  await waitDeployed(portfolio.id, "portfolio to redeploy");

  log("taking a snapshot of dev-box");
  await api("POST", `/vps/${vps.id}/snapshots`, { label: "Before upgrading Node" });
  await until("the snapshot", async () => {
    const list = await api("GET", `/vps/${vps.id}/snapshots`);
    const snaps = Array.isArray(list) ? list : list.snapshots;
    return snaps.some((s) => s.status === "ready");
  });
  return { vps, portfolio, notes };
}

async function tearDown(demo) {
  for (const a of [demo.notes, demo.portfolio]) await api("DELETE", `/apps/${a.id}`).catch(() => {});
  await sleep(2000);
  await api("DELETE", `/vps/${demo.vps.id}`).catch((e) => log("could not delete dev-box:", e.message));
}

// --- Screenshots ------------------------------------------------------------

async function newContext(browser, mobile) {
  const opts = mobile ? { ...devices["iPhone 13"] } : { viewport: { width: 1280, height: 860 }, deviceScaleFactor: 2 };
  const ctx = await browser.newContext({ ...opts, colorScheme: "dark" });
  const res = await ctx.request.post(base + "/api/auth/login", { headers: { "X-LPS-Request": "1" }, data: { username: user, password } });
  if (!res.ok()) throw new Error(`browser login: ${res.status()}`);
  return ctx;
}

async function open(ctx, url, waitText) {
  const p = await ctx.newPage();
  await p.goto(base + url, { waitUntil: "networkidle" });
  if (waitText) await p.getByText(waitText).first().waitFor({ timeout: 30_000 });
  return p;
}

const file = (name) => path.join(out, name + ".png");
const card = (p, title) => p.locator("h2", { hasText: new RegExp(`^${title}$`, "i") }).locator("xpath=ancestor::section[1]");

async function shoot(p, name, opts = {}) {
  await p.waitForTimeout(800);
  await p.screenshot({ path: file(name), ...opts });
  log("saved", file(name));
  await p.close();
}

async function shootCard(p, name, title) {
  const c = card(p, title);
  await c.scrollIntoViewIfNeeded();
  await p.waitForTimeout(500);
  await c.screenshot({ path: file(name) });
  log("saved", file(name));
  await p.close();
}

async function takeScreenshots(demo, pubKey) {
  const browser = await chromium.launch();
  const desk = await newContext(browser, false);
  const phone = await newContext(browser, true);
  let p;

  p = await open(desk, "/vps/new", "SSH username");
  await p.locator('input[placeholder="dev-box"]').first().fill("web-01");
  await p.locator("textarea").first().fill(pubKey);
  await p.mouse.click(5, 5);
  await shoot(p, "new-vps");

  p = await open(desk, "/apps/new", "Code from");
  await p.getByLabel("Name", { exact: true }).fill("portfolio");
  const folder = p.getByLabel("Project folder on this PC");
  await folder.fill(path.join(projects, "portfolio"));
  await folder.blur();
  await p.getByText("Detected:").waitFor();
  await shoot(p, "new-app-dockerfile", { fullPage: true });

  await shoot(await open(desk, "/", "portfolio"), "dashboard");
  await shoot(await open(desk, `/vps/${demo.vps.id}`, "Snapshots"), "vps");
  await shootCard(await open(desk, `/vps/${demo.vps.id}`, "Snapshots"), "snapshots", "Snapshots");

  p = await open(desk, `/vps/${demo.vps.id}`, "Open terminal");
  await p.getByRole("button", { name: "Open terminal" }).click();
  await p.locator(".xterm").waitFor();
  await p.waitForTimeout(1500);
  await p.locator(".xterm").click();
  for (const c of ["clear", "cat /etc/os-release | head -2", "free -h", "curl -s localhost:3000; echo"]) {
    await p.keyboard.type(c, { delay: 5 });
    await p.keyboard.press("Enter");
    await p.waitForTimeout(1200);
  }
  await shoot(p, "terminal");

  await shoot(await open(desk, `/apps/${demo.portfolio.id}`, "Build log"), "app");
  await shootCard(await open(desk, `/apps/${demo.portfolio.id}`, "Public link"), "remote-app", "Public link");

  p = await open(desk, `/apps/${demo.notes.id}`, "Deployments");
  await p.getByText("Environment", { exact: true }).first().click();
  await p.getByText("DATABASE_URL").first().waitFor();
  await shoot(p, "app-env");

  // Two-factor setup, before it is turned on below.
  p = await open(desk, "/account", "Two-factor");
  await p.getByRole("button", { name: /Turn on two-factor/i }).click();
  await p.locator("input[type=password]").first().fill(password);
  await p.locator("form button[type=submit]").first().click();
  await p.getByAltText("QR code for your authenticator app").waitFor();
  await shoot(p, "two-factor");

  // With two-factor on, the remote-access page shows no warning. The browser
  // contexts are already signed in, so they stay signed in.
  const setup = await api("POST", "/auth/2fa/setup", { password });
  await api("POST", "/auth/2fa/enable", { code: totp(setup.secret) });

  await shoot(await open(desk, "/remote", "Last checked"), "remote", { fullPage: true });
  await shoot(await open(phone, "/", "portfolio"), "remote-mobile");
  await browser.close();
}

const pubKey = sshPublicKey();
const demo = await setUpDemo(pubKey);
try {
  await takeScreenshots(demo, pubKey);
} finally {
  if (process.env.LPS_SHOTS_KEEP !== "1") {
    log("deleting the demo VPS and apps");
    await tearDown(demo);
  }
}
log("done");
