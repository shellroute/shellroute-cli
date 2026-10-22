# Changelog

Dates are the npm publish dates. The same notes are at https://shellroute.com/changelog.

## v0.2.1

- Proxy credentials are taken exactly as written: a password with `/`, `#`, `?`, `%`, spaces or `@` no longer needs percent-encoding. Entries saved by 0.2.0 keep working.
- When a proxy rejects the login, or the shell splits an unquoted URL, the error says what to do about it.

## v0.2.0 (2026-10-21)

Bring your own proxy. Route a command or the interactive shell through a proxy you already have, with no Shellroute account.

- `shellroute upstream add <name> <url> [--note TEXT]`, `upstream list`, `upstream remove <name>`, `upstream test <name|url>`
- `shellroute run <name> -- <cmd>`, `shellroute run --proxy <url> -- <cmd>`, `SHELLROUTE_PROXY=<url>` for CI
- `shellroute proxy --upstream <name|url>` for a persistent local proxy over your own
- `/connect <name>` in the interactive shell; `/rotate` and the account commands answer "not applicable" on an own route
- `http`, `https` (TLS to the proxy), `socks5` and `socks5h` with optional username and password; SOCKS5 resolves names at the proxy
- The child command still sees only the local proxy on `127.0.0.1`; credentials never reach it. Passwords are stored in `config.toml` (mode 600) and shown as `***` everywhere else
- Starting an own route checks the tunnel once with one retry (10 s each), reports the exit IP and the country the exit IP service observes for it, and names the cause of a failure: cannot resolve host, connection refused, timeout, proxy rejected credentials, proxy returned a status. `--no-check` skips the probe
- A wrong password or three consecutive connection failures mark an own route dead and, in `run`, kill the command, as with a hosted route
- Own routes work with no login and with the Shellroute API unreachable; the version check is skipped for them
- Nothing is metered or billed on an own route. Hosted routes are unchanged
- A build whose version is not a release number (a developer build) is no longer blocked by the minimum version check

## v0.1.6 (2026-09-30)

API key from the environment works everywhere.

- `SHELLROUTE_API_KEY` was ignored on machines that had never run `shellroute login`, so CI runners and containers reported "Not authenticated". The variable is now read whether or not a config file exists
- `shellroute run` no longer fails when the config directory cannot be created, for example in a container without a home directory. The session bookkeeping file is skipped instead

## v0.1.5 (2026-09-17)

Help text correction.

- The `/help` list inside a session named a command that no longer exists. It now shows `shellroute proxy --country` for the persistent proxy
- Clearer package descriptions on npm and Homebrew, and consistent spelling of the product name in messages and docs

## v0.1.4 (2026-09-09)

Clean shutdown for scripts and servers.

- Signal handling in direct mode: Ctrl+C, SIGTERM and terminal close now reach the command run by `shellroute run`, and the session ends with its summary once the command stops. Suitable for systemd services
- A command that ignores the stop signal is force-stopped after five seconds, or immediately on a second Ctrl+C
- `shellroute run` now exits with the command's exit code, or ends by the same signal the command did, so shells and systemd see the real result instead of status 255
- Interrupting `shellroute run` or `shellroute proxy` while still connecting now ends the session instead of leaving it open

## v0.1.3 (2026-08-18)

Starter credit, logout fix.

- Starter credit on signup: new accounts receive $1 credit automatically, shown after login
- Fixed `/logout` not working correctly in some configurations
- Low balance warning now triggers earlier so sessions don't fail unexpectedly

## v0.1.2 (2026-08-12)

npm install fix.

- Fixed `npm install -g shellroute` failing on some Node versions

## v0.1.1 (2026-08-12)

Node.js support, compatibility matrix.

- Node.js proxy support: Node's built-in fetch, http and https modules now use the proxy automatically inside Shellroute sessions
- Compatibility matrix: tested results for curl, wget, Python, Node.js, Go, Playwright and more, in `docs/compatibility.md`
- Local traffic (localhost, 127.0.0.1) no longer routes through the proxy

## v0.1.0 (2026-06-02)

Initial release.

- Interactive mode: open a proxied shell with `/connect`, `/rotate`, `/disconnect`
- Direct mode: `shellroute run US -- curl ...` for scripts, CI and agents
- Persistent proxy: `shellroute proxy --country US` for tools with explicit proxy URLs
- 120+ countries with residential and datacenter exits
- City targeting and sticky IPs for consistent sessions
- Kill switch: traffic fails instead of leaking when the tunnel drops
- Auto-reconnect on connection loss
- Pay per GB with real-time balance and usage tracking
- Email OTP login, API key support for CI, macOS and Linux
