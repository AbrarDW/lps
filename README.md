# LPS — Local Personal Server

Turn your home PC into your own VPS host. LPS runs **Linux VPSes** (Ubuntu machines you SSH into) and **web app deployments** on your own hardware, managed from a web dashboard, so you can test your apps from anywhere without renting a server.

> Status: **early development**. Linux VPSes, Dockerfile app deploys and [remote access with Tailscale](#remote-access-with-tailscale) work. A reverse proxy with hostnames and the one-click installer are next. See the [roadmap](#roadmap).

![LPS dashboard](docs/images/dashboard.png)

## Install on Windows

1. Download **`LPS-Setup-<version>.exe`** from the [Releases page](https://github.com/AbrarDW/lps/releases) (or, for a development build, from the latest successful **Windows installer** run under the repository's *Actions* tab → *Artifacts*).
2. Run it. It installs for your user only (no administrator rights needed) into `%LOCALAPPDATA%\Programs\LPS`. Because the installer isn't code-signed yet, Windows SmartScreen may warn: choose **More info → Run anyway**.
3. LPS starts with an icon in the system tray and opens the **setup wizard** in your browser.

```mermaid
flowchart LR
    A["LPS-Setup.exe<br/>(no admin needed)"] --> B["LPS starts<br/>tray icon + browser"]
    B --> C["1. Check this PC<br/>WSL2 · Docker · disk"]
    C --> D["2. Create admin"]
    D --> E["3. Remote access<br/>(optional, Tailscale)"]
    E --> F["4. Start with Windows<br/>→ Dashboard"]
```

The wizard checks everything LPS needs and tells you how to fix what's missing: the exact command to install WSL2, a link to Docker Desktop, and a **Start Docker Desktop** button. The list refreshes by itself while you fix things.

![Setup wizard: checking this PC](docs/images/setup-wizard.png)

**Day to day:** right-click the **LPS tray icon** for *Open dashboard*, *Start with Windows* and *Quit LPS*. Quitting LPS doesn't stop running VPSes or apps; they keep running in Docker Desktop.

**Uninstall:** Windows Settings → Apps → *LPS - Local Personal Server*. Your data (`%ProgramData%\LPS`) is kept so a reinstall picks up your VPSes and apps; delete that folder to remove everything.

## How it works

```mermaid
flowchart LR
    you(["You<br/>browser / SSH client"])

    subgraph pc["Your home PC (Windows 10/11)"]
        lpsd["<b>lpsd</b><br/>single Go binary<br/>API + React dashboard"]
        db[("SQLite<br/>lps.db")]
        subgraph docker["Docker Desktop · WSL2 Linux VM"]
            vps1["lps-vps-dev-box<br/>Ubuntu 24.04 + SSH"]
            vps2["lps-vps-…<br/>Ubuntu 24.04 + SSH"]
            app1["lps-app-my-site<br/>built from your Dockerfile"]
            vol[("/home volumes")]
        end
        src[/"Project folders<br/>C:\projects\…"/]
    end

    you -- "dashboard :7070" --> lpsd
    you -- "ssh :2201…2299" --> vps1
    you -- "http :8100…8199" --> app1
    lpsd --- db
    lpsd -- "Docker Engine API" --> docker
    src -. "build context" .-> lpsd
    vps1 --- vol
    vps2 --- vol
```

- **Windows host, Linux VPS.** On Windows, Docker Desktop runs Linux containers inside a lightweight WSL2 VM, so every VPS is real Linux. You SSH in, `apt install`, `git clone` and run things just as you would on a rented VPS.
- **Each VPS** is an Ubuntu 24.04 container with `sshd`. It has its own SSH user (key or password), passwordless sudo for key-only accounts, CPU and memory limits, and a persistent `/home` volume that survives restarts and can be kept when you delete the VPS.
- **Each app** is a project folder on your PC. If it has no `Dockerfile`, LPS detects the project type and writes one for you to review. LPS builds it, runs it, publishes its port, and keeps a log of every deployment.
- **Secure by default.** The dashboard and all published ports bind to `127.0.0.1`. The admin password is hashed with bcrypt, sessions are HttpOnly SameSite=Strict cookies, writes need a CSRF header, logins are rate-limited, and VPS containers are unprivileged with no Docker socket access.

### Terminal in the browser

Every running VPS has a **Terminal** card: click **Open terminal** for a shell as the VPS's user, right in the dashboard. No SSH client or keys are needed, and it works from your phone too over [remote access](#remote-access-with-tailscale). It supports colours, full screen and resizing (`vim`, `htop` and friends work), and `sudo` behaves exactly as it does over SSH.

![Terminal in the browser](docs/images/terminal.png)

```mermaid
sequenceDiagram
    participant B as Browser (xterm.js)
    participant L as lpsd
    participant D as Docker
    B->>L: WebSocket /api/vps/{id}/terminal (session cookie, same-origin only)
    L->>D: exec bash -l as the VPS user, with a TTY
    B-->>L: keystrokes (binary) · resize (JSON)
    L-->>D: stdin · TTY resize
    D-->>L: shell output
    L-->>B: output (binary)
```

Only the signed-in admin can open a terminal. The WebSocket refuses cross-site requests (Origin check plus the SameSite=Strict session cookie), and each session is logged.

### Creating a VPS

```mermaid
sequenceDiagram
    autonumber
    actor U as You
    participant L as lpsd
    participant D as Docker
    participant V as VPS container

    U->>L: New VPS (name, SSH user, key and/or password, limits)
    L->>L: Validate, reserve SSH port 22xx, save to SQLite
    L-->>U: 202 Accepted · state "provisioning"
    L->>D: Build lps/ubuntu-vps image (first time only)
    L->>D: Create /home volume + container (CPU/RAM limits, 127.0.0.1:22xx → 22)
    L->>D: Copy one-time init file (user, key, password)
    L->>D: Start
    D->>V: entrypoint: create user, install key, set password
    V->>V: Delete init file, start sshd
    U->>V: ssh dev@localhost -p 22xx
```

### Deploying an app

```mermaid
flowchart TD
    A["New app"] --> B{"Dockerfile in<br/>project folder?"}
    B -- yes --> C["Pack folder<br/>(honours .dockerignore,<br/>skips .git, node_modules, .venv)"]
    B -- no --> G1["LPS detects the project type<br/>Node · Python · Go · PHP · static"]
    G1 --> G2["You review / edit the<br/>generated Dockerfile"]
    G2 -- "kept in LPS (default)" --> C
    G2 -- "or saved into the folder" --> C
    R["Redeploy"] --> C
    C --> D["docker build → lps-app-name:N<br/>(log streamed to the dashboard)"]
    D -- "build fails" --> F["Deployment marked failed<br/>running version keeps serving"]
    D -- "build OK" --> E["Remove old container"]
    E --> G["Create + start new container<br/>127.0.0.1:81xx → app port"]
    G --> H["Deployment marked running ✅"]
```

### VPS lifecycle

```mermaid
stateDiagram-v2
    direction LR
    [*] --> provisioning: create
    provisioning --> running: started
    provisioning --> error: build/create failed
    running --> stopped: stop
    stopped --> running: start
    running --> [*]: delete
    stopped --> [*]: delete
    error --> [*]: delete
```

When you delete a VPS you can keep its `/home` volume; creating a VPS with the same name later picks it up again.

### Limits compared with a real VM

A VPS here is a container, not a full VM. It shares the host's Linux kernel (WSL2's kernel on Windows). `systemd` is not running, so start services with their own commands or a process manager like `supervisord`/`pm2`. Running Docker inside a VPS is not supported yet. CPU limits are quotas: `nproc` still shows every host core.

## Remote access with Tailscale

Reach the dashboard, SSH into your VPSes and open your apps from your phone or laptop, from anywhere. No domain, no router changes, and nothing is exposed to the internet unless you turn on a public link for an app.

```mermaid
flowchart LR
    subgraph devices["Your devices (Tailscale app, same account)"]
        phone(["Phone"])
        laptop(["Laptop"])
    end
    world(["Anyone on the internet"])

    subgraph pc["Home PC"]
        ts["Tailscale app<br/>tailscale serve / funnel"]
        lpsd["lpsd :7070"]
        vps["VPS SSH :2201"]
        app["App :8100"]
    end

    phone -- "https://home-pc.tailnet.ts.net" --> ts
    laptop -- "ssh dev@home-pc.tailnet.ts.net -p 2201" --> ts
    world -. "public link (optional)<br/>https://home-pc.tailnet.ts.net:8443" .-> ts
    ts --> lpsd
    ts --> vps
    ts --> app
```

**Set up (about 5 minutes):**

1. Install the [Tailscale app](https://tailscale.com/download) on the home PC and sign in.
2. In the Tailscale admin console, open [DNS](https://login.tailscale.com/admin/dns) and enable **HTTPS Certificates**.
3. In LPS, open **Remote access** and turn it on. LPS publishes the dashboard, every VPS's SSH port and every app on your tailnet, and keeps that in sync as you add or delete things.
4. Install Tailscale on your phone or laptop, sign in with the same account, and open the dashboard address LPS shows (like `https://home-pc.your-tailnet.ts.net`).

| Remote access page | App with a public link | Phone |
|---|---|---|
| ![Remote access page](docs/images/remote.png) | ![App with private and public links](docs/images/remote-app.png) | ![Remote access on a phone](docs/images/remote-mobile.png) |

**What gets published:**

| What | Address on your tailnet | Who can reach it |
|---|---|---|
| Dashboard | `https://<pc>.<tailnet>.ts.net` | Your Tailscale devices (and you still log in to LPS) |
| VPS SSH | `ssh <user>@<pc>.<tailnet>.ts.net -p 22xx` | Your Tailscale devices |
| Apps | `https://<pc>.<tailnet>.ts.net:81xx` | Your Tailscale devices |
| App public link (opt-in) | `https://<pc>.<tailnet>.ts.net:8443` or `:10000` | **Anyone** with the link (Tailscale Funnel) |

**Good to know:**

- Public links use [Tailscale Funnel](https://tailscale.com/kb/1223/funnel), which allows only a few ports per machine, so **at most 2 apps can be public at once**. Funnel must be allowed for your tailnet; LPS links to the instructions if it isn't. The dashboard and SSH are never made public.
- LPS only adds and removes the `tailscale serve`/`funnel` settings it created itself. If a port is already used by your own Tailscale settings, LPS leaves it alone and shows a warning.
- Everything still listens on `127.0.0.1`; Tailscale forwards to it, so nothing is opened on your LAN or router.
- Start lpsd with `-no-remote` to turn the integration off completely.

### Troubleshooting

- **"Could not resolve hostname" or the site can't be reached:** the device you're using must have the Tailscale app installed, connected, and signed in to the **same account** as the home PC. `*.ts.net` addresses are private and don't exist for other devices. Check that the PC appears in the device's Tailscale device list. On a Mac, keep **Use Tailscale DNS settings** on, and turn off other VPNs or iCloud Private Relay.
- **It worked before, now it doesn't:** the home PC must be on and awake, with Docker Desktop, Tailscale and lpsd running. Press **Check again** on the Remote access page and look for errors.
- **Sharing with someone without Tailscale:** turn on the app's **Public link**. It can take about a minute to start working.

## Screenshots

| Create a VPS | VPS details |
|---|---|
| ![New VPS form](docs/images/new-vps.png) | ![VPS details with SSH command](docs/images/vps.png) |

| App with deployment log | First-run setup | Phone |
|---|---|---|
| ![App details with build log](docs/images/app.png) | ![Admin account setup](docs/images/setup.png) | ![Dashboard on a phone](docs/images/mobile.png) |

## Build from source

```mermaid
flowchart LR
    a["Install Go, Node.js,<br/>Docker Desktop"] --> b["Build<br/>.\build.ps1"] --> c["Run<br/>.\bin\lpsd.exe serve"] --> d["Open 127.0.0.1:7070<br/>create admin"] --> e["New VPS / New app"]
```

### Windows (PowerShell)

Install these first, then **open a new PowerShell window** so the new commands are found:

- [Docker Desktop](https://www.docker.com/products/docker-desktop/) with the WSL2 backend (the default), and start it.
- [Go 1.24+](https://go.dev/dl/)
- [Node.js 22 LTS](https://nodejs.org/)
- [Git](https://git-scm.com/download/win)

`make` is **not** needed on Windows. Use `build.ps1` instead:

```bash
git clone https://github.com/abrardw/lps.git
```

```bash
cd lps
```

```bash
.\build.ps1
```

```bash
.\bin\lpsd.exe serve
```

If PowerShell says **"running scripts is disabled on this system"**, run the build this way instead:

```bash
powershell -ExecutionPolicy Bypass -File .\build.ps1
```

Or allow local scripts for your user once (it only affects scripts on your PC, not downloaded ones):

```bash
Set-ExecutionPolicy -Scope CurrentUser RemoteSigned
```

Other tasks: `.\build.ps1 run` (build and start), `.\build.ps1 test`, `.\build.ps1 build-all` (release binaries for every OS), `.\build.ps1 installer` (builds `dist\LPS-Setup-<version>.exe`; needs [Inno Setup 6](https://jrsoftware.org/isdl.php)), `.\build.ps1 clean`.

### Linux / macOS

- **Linux:** Docker Engine; your user must be able to use `/var/run/docker.sock`.
- **macOS:** Docker Desktop, OrbStack or Colima.
- Go 1.24+ and Node.js 22+.

```bash
git clone https://github.com/abrardw/lps.git
```

```bash
cd lps
```

```bash
make all
```

```bash
./bin/lpsd serve
```

### First steps

Open <http://127.0.0.1:7070>, create the admin account, then:

1. **New VPS** → choose a name and paste your SSH public key (`ssh-keygen -t ed25519` creates one, including in PowerShell). The first VPS builds the Ubuntu image, which takes a few minutes. After that you get a command like `ssh dev@localhost -p 2201`.
2. **New app** → enter the absolute path to a project folder. If it has no `Dockerfile`, LPS proposes one (see below). Check the port the app listens on, then **Create and deploy**. Try `examples\static-site` (port 80). The app is served at `http://localhost:81xx`.

### Projects without a Dockerfile

LPS looks at the folder and generates a Dockerfile you can review and edit before deploying:

| LPS finds | Template | Port |
|---|---|---|
| `package.json` with `next` | Next.js (build + `npm start`) | 3000 |
| `package.json` with Vite / Create React App / Angular | Static site: build with Node, serve with nginx | 80 |
| `package.json` (other) | Node.js server (`npm start` or `node server.js`) | 3000 |
| `manage.py` | Django with gunicorn | 8000 |
| `fastapi` / `flask` in requirements | FastAPI with uvicorn / Flask with gunicorn | 8000 |
| `requirements.txt` / `pyproject.toml` | Python (`python main.py`) | 8000 |
| `go.mod` | Go (multi-stage build) | 8080 |
| `composer.json` / `index.php` | PHP 8.3 with Apache | 80 |
| `index.html` | Static website with nginx | 80 |

npm, yarn and pnpm are picked from the lockfile. You can switch templates or edit the text freely. By default the Dockerfile is **kept in LPS** and your folder isn't touched; tick **Also save this Dockerfile into the project folder** to get a real `Dockerfile` (and `.dockerignore`) you can commit. LPS never overwrites an existing one. You can edit an LPS-kept Dockerfile later on the app page (**Dockerfile → Save & redeploy**).

![Generated Dockerfile in the New app form](docs/images/new-app-dockerfile.png)

### `lpsd serve` flags

| Flag | Default | Meaning |
|---|---|---|
| `-addr` | `127.0.0.1:7070` | Dashboard address. Keep it on localhost; use [remote access](#remote-access-with-tailscale) to reach it from elsewhere. |
| `-data` | `%ProgramData%\LPS` · `~/.local/share/lps` · `~/Library/Application Support/LPS` | Database and state. |
| `-bind-ip` | `127.0.0.1` | Where VPS SSH ports (2201–2299) and app ports (8100–8199) are published. Use `0.0.0.0` to reach them from your LAN. |
| `-secure-cookies` | `false` | Always mark cookies Secure. Not needed for Tailscale: LPS detects HTTPS through it automatically. |
| `-no-remote` | `false` | Disable the Tailscale remote-access integration. |

### Ports at a glance

| Port range | Used for | Bound to (default) |
|---|---|---|
| `7070` | Dashboard and API | `127.0.0.1` |
| `2201–2299` | SSH into each VPS | `127.0.0.1` (`-bind-ip`) |
| `8100–8199` | Each app's HTTP port | `127.0.0.1` (`-bind-ip`) |

## Development

Run the checks with `.\build.ps1 test` on Windows, or:

```bash
make test
```

```mermaid
flowchart TB
    main["cmd/lpsd<br/>entry point, flags"] --> api["internal/api<br/>HTTP handlers, auth, background jobs"]
    api --> store["internal/store<br/>SQLite + migrations"]
    api --> auth["internal/auth<br/>bcrypt, sessions, rate limit"]
    api --> dock["internal/docker<br/>Engine interface + Docker client"]
    api --> build["internal/builder<br/>build context, .dockerignore"]
    api --> tun["internal/tunnel<br/>Provider interface + Tailscale CLI driver"]
    dock --> img["images/ubuntu-vps<br/>embedded VPS image"]
    main --> web["web/ (React)<br/>embedded dashboard"]
    desk["cmd/lps<br/>desktop app + tray (Windows)"] --> appk["internal/app<br/>shared server startup"]
    main --> appk
    appk --> api
    inst["installer/windows/lps.iss<br/>Inno Setup"] -.packages.-> desk
```

- Frontend: `web/` (React, Vite, TypeScript, TanStack Query, Tailwind). For hot reload, run `npm run dev` in `web/` while `lpsd serve` is running; Vite proxies `/api` to it.
- Database: SQLite through the pure-Go `modernc.org/sqlite` driver, so there's no CGO and cross-compiling to Windows is easy. Migrations live in `internal/store/migrations` and are applied on start. The SQL is kept portable so PostgreSQL can be added as an option later.
- `internal/docker.Engine` and `internal/tunnel.Provider` are interfaces; API tests use in-memory fakes, so `go test ./...` needs neither Docker nor Tailscale.

## Roadmap

```mermaid
flowchart LR
    m1["✅ 1. MVP<br/>dashboard, admin,<br/>Linux VPS + SSH,<br/>Dockerfile deploys"]
    m2["2. Routing<br/>Caddy proxy,<br/>hostname per app,<br/>env vars, auto-detect builds"]
    m3["3. Remote access<br/>✅ Tailscale →<br/>Cloudflare Quick Tunnel →<br/>Cloudflare Tunnel (own domain)"]
    m4["4. VPS feel<br/>✅ browser terminal,<br/>git-URL deploys,<br/>stats"]
    m5["5. Packaging<br/>✅ Windows installer + wizard,<br/>Linux + macOS next"]
    m1 --> m2 --> m3 --> m4 --> m5
```

1. ✅ **MVP:** dashboard, admin account, Linux VPS (Ubuntu and SSH) lifecycle, Dockerfile app deploys with logs, SQLite.
2. **Routing:** a Caddy reverse proxy with a hostname per app and per VPS HTTP port, env vars, and auto-detected builds (Node, Python, static) without a Dockerfile.
3. **Remote access (pluggable):** ✅ Tailscale (no domain needed, private access to the dashboard, SSH and apps; Funnel for public links). Next: Cloudflare Quick Tunnel (instant public demo links), then Cloudflare Tunnel with your own domain. Optional 2FA (TOTP) before anything is exposed.
4. **VPS feel:** ✅ browser terminal. Next: git-URL deploys and webhooks, and resource stats.
5. **Packaging:** ✅ Windows installer, tray app and setup wizard (checks WSL2 and Docker, creates the admin, offers remote access, start with Windows). Next: code signing, auto-update, and Linux (systemd, .deb) and macOS (launchd, .pkg) installers.

## License

[MIT](LICENSE)
