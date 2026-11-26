# Reference

## `lpsd serve` flags

| Flag | Default | Meaning |
|---|---|---|
| `-addr` | `127.0.0.1:7070` | Dashboard address. Keep it on localhost and use [remote access](remote-access.md) to reach it from elsewhere. |
| `-data` | `%ProgramData%\LPS` · `~/.local/share/lps` · `~/Library/Application Support/LPS` | Folder for the database and state. |
| `-bind-ip` | `127.0.0.1` | IP that VPS SSH ports and app ports are published on. Use `0.0.0.0` to reach them from your LAN. |
| `-secure-cookies` | `false` | Always mark cookies Secure. You don't need this for Tailscale: LPS detects HTTPS through it automatically. |
| `-no-remote` | `false` | Turn off the Tailscale remote-access integration. |
| `-no-update-check` | `false` | Turn off checking GitHub for new releases. |

## Other `lpsd` commands

| Command | What it does |
|---|---|
| `lpsd disable-2fa` | Turns off two-factor sign-in (see [Two-factor sign-in](remote-access.md#two-factor-sign-in)) |
| `lpsd autostart off` | Stops LPS from starting when you sign in (Linux, macOS) |

## Ports at a glance

| Port range | Used for | Bound to (default) |
|---|---|---|
| `7070` | Dashboard and API | `127.0.0.1` |
| `7071` | Apps by name (`/<app>/`), published on the tailnet as `:8080` | `127.0.0.1` (the port after the dashboard's, otherwise any free port) |
| `2201–2299` | SSH into each VPS | `127.0.0.1` (`-bind-ip`) |
| `8100–8199` | Each app's HTTP port (for VPS apps, through a relay) | `127.0.0.1` (`-bind-ip`) |

## Security defaults

- The dashboard and all published ports bind to `127.0.0.1`.
- The admin password is hashed with bcrypt.
- Sessions use HttpOnly, SameSite=Strict cookies.
- Write requests need a CSRF header.
- Logins are rate-limited.
- VPS containers are unprivileged and have no access to the Docker socket.
