# LPS — Local Personal Server

Turn your home PC into your own VPS host. LPS runs **Linux VPSes** (Ubuntu machines you SSH into) and **web app deployments** on your own hardware, managed from a web dashboard, so you can test your apps from anywhere without renting a server.

> Status: **early development (milestone 1)**. Linux VPSes and Dockerfile app deploys work locally. Remote access (tunnels), a reverse proxy with hostnames and the one-click installer are next. See the [roadmap](#roadmap).

## How it works

```
Browser ──► lpsd (Go, single binary: API + embedded React dashboard, SQLite)
                │ Docker Engine API
                ▼
        Docker Desktop (WSL2 on Windows) / Docker Engine (Linux)
          ├─ lps-vps-<name>   Ubuntu 24.04 + OpenSSH, own /home volume, CPU/RAM limits
          └─ lps-app-<name>   your project, built from its Dockerfile
```

- **Windows host, Linux VPS.** On Windows, Docker Desktop runs Linux containers inside a lightweight WSL2 VM, so every VPS is real Linux. You SSH in, `apt install`, `git clone` and run things just as you would on a rented VPS.
- **Each VPS** is an Ubuntu 24.04 container with `sshd`. It has its own SSH user (key or password), passwordless sudo for key-only accounts, CPU and memory limits, and a persistent `/home` volume that survives restarts and can be kept when you delete the VPS.
- **Each app** is a folder on your PC with a `Dockerfile`. LPS builds it, runs it, publishes its port, and keeps a log of every deployment. Redeploying swaps the container. A failed build leaves the running version alone.
- **Secure by default.** The dashboard and all published ports bind to `127.0.0.1`. The admin password is hashed with bcrypt, sessions are HttpOnly SameSite=Strict cookies, writes need a CSRF header, logins are rate-limited, and VPS containers are unprivileged with no Docker socket access.

### Limits compared with a real VM

A VPS here is a container, not a full VM. It shares the host's Linux kernel (WSL2's kernel on Windows). `systemd` is not running, so start services with their own commands or a process manager like `supervisord`/`pm2`. Running Docker inside a VPS is not supported yet. CPU limits are quotas: `nproc` still shows every host core. If you need full VMs, see Multipass/Hyper-V below.

## Existing solutions

| Tool | What it does | Why LPS is different |
|---|---|---|
| [Coolify](https://coolify.io), [Dokploy](https://dokploy.com), [CapRover](https://caprover.com), [Dokku](https://dokku.com) | Self-hosted Heroku-style PaaS: git push or click to deploy, builds Docker images, Traefik/Caddy proxy with Let's Encrypt. | Built for a Linux server with a public IP. They don't run on a Windows desktop and don't handle home networks behind CGNAT. Coolify is the closest; worth studying. |
| [CasaOS](https://casaos.io), [Umbrel](https://umbrel.com), [YunoHost](https://yunohost.org) | Home-server OSes with app stores on Docker. | Install catalogue apps, not "run my project folder" or "give me an SSH box". |
| [Portainer](https://www.portainer.io) | Web UI for Docker. | Raw container management, with no VPS, build or tunnel workflow. |
| [ngrok](https://ngrok.com), [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/), [Tailscale Funnel](https://tailscale.com/kb/1223/funnel) | Expose a local port to the internet without port forwarding. | Only the networking part. LPS will set these up for you (pluggable). |
| [Multipass](https://multipass.run), Hyper-V, VirtualBox | Full Ubuntu VMs. | Real VMs, but heavy and manual. No deploy, URL or remote-management layer. |

**LPS's niche:** a Windows-first, one-click "home VPS" that combines SSH-able Linux boxes, Coolify-style deploys and built-in tunnel setup, and works behind CGNAT without router changes.

## Quick start (from source)

Requirements:

- **Windows 10/11:** [Docker Desktop](https://www.docker.com/products/docker-desktop/) with the WSL2 backend (default), running.
- **Linux:** Docker Engine; your user must be able to use `/var/run/docker.sock`.
- **macOS:** Docker Desktop, OrbStack or Colima. (Linux and macOS hosts are not the main target yet, but they work.)
- To build: Go 1.24+ and Node.js 22+.

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

On Windows without `make`, run the steps directly (PowerShell):

```bash
cd web; npm ci; npm run build; cd ..
```

```bash
go build -o bin/lpsd.exe ./cmd/lpsd
```

```bash
.\bin\lpsd.exe serve
```

Open <http://127.0.0.1:7070>, create the admin account, then:

1. **New VPS** → choose a name and paste your SSH public key (`ssh-keygen -t ed25519` creates one, including in PowerShell). The first VPS builds the Ubuntu image, which takes a few minutes. After that you get a command like `ssh dev@localhost -p 2201`.
2. **New app** → enter the absolute path to a project folder with a `Dockerfile` and the port the app listens on. Try `examples/static-site` (port 80). The app is served at `http://localhost:81xx`.

### `lpsd serve` flags

| Flag | Default | Meaning |
|---|---|---|
| `-addr` | `127.0.0.1:7070` | Dashboard address. Keep it on localhost and expose it through a tunnel (coming soon). |
| `-data` | `%ProgramData%\LPS` · `~/.local/share/lps` · `~/Library/Application Support/LPS` | Database and state. |
| `-bind-ip` | `127.0.0.1` | Where VPS SSH ports (2201–2299) and app ports (8100–8199) are published. Use `0.0.0.0` to reach them from your LAN. |
| `-secure-cookies` | `false` | Set this when the dashboard is served over HTTPS. |

## Development

```bash
make test
```

- Backend: `cmd/lpsd` (entry point), `internal/api` (HTTP and handlers), `internal/docker` (Docker Engine client), `internal/store` (SQLite and migrations), `internal/auth`, `internal/builder` (build contexts with `.dockerignore`), `images/ubuntu-vps` (the VPS image, embedded in the binary).
- Frontend: `web/` (React, Vite, TypeScript, TanStack Query, Tailwind). For hot reload, run `npm run dev` in `web/` while `lpsd serve` is running; Vite proxies `/api` to it.
- Database: SQLite through the pure-Go `modernc.org/sqlite` driver, so there's no CGO and cross-compiling to Windows is easy. Migrations live in `internal/store/migrations` and are applied on start. The SQL is kept portable so PostgreSQL can be added as an option later.

## Roadmap

1. ✅ **MVP:** dashboard, admin account, Linux VPS (Ubuntu and SSH) lifecycle, Dockerfile app deploys with logs, SQLite.
2. **Routing:** a Caddy reverse proxy with a hostname per app and per VPS HTTP port, env vars, and auto-detected builds (Node, Python, static) without a Dockerfile.
3. **Remote access (pluggable):** Cloudflare Tunnel first (works behind CGNAT), then Tailscale, then port forwarding with DDNS and Let's Encrypt. Optional 2FA (TOTP) before anything is exposed.
4. **VPS feel:** a browser terminal, git-URL deploys and webhooks, and resource stats.
5. **Packaging:** run as a Windows service, plus a **one-click/step-by-step installer GUI** (checks WSL2 and Docker, creates the admin, sets up the tunnel). Then Linux (systemd, .deb) and macOS (launchd, .pkg) installers.

## License

[MIT](LICENSE)
