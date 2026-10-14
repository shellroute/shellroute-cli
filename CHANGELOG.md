# Changelog

## v0.2.0 (unreleased)

Bring your own proxy. Route a command or the interactive shell through a proxy you already have, with no Shellroute account.

- `shellroute upstream add <name> <url> [--country CC] [--note TEXT]`, `upstream list`, `upstream remove <name>`, `upstream test <name|url>`
- `shellroute run <name> -- <cmd>`, `shellroute run --proxy <url> -- <cmd>`, `SHELLROUTE_PROXY=<url>` for CI
- `shellroute proxy --upstream <name|url>` for a persistent local proxy over your own
- `/connect <name>` in the interactive shell; `/rotate` and the account commands answer "not applicable" on an own route
- `http`, `https` (TLS to the proxy), `socks5` and `socks5h` with optional username and password; SOCKS5 resolves names at the proxy
- The child command still sees only the local proxy on `127.0.0.1`; credentials never reach it. Passwords are stored in `config.toml` (mode 600) and shown as `***` everywhere else
- Starting an own route checks the tunnel once with one retry (10 s each) and names the cause of a failure: cannot resolve host, connection refused, timeout, proxy rejected credentials, proxy returned a status. `--no-check` skips the exit IP probe
- A wrong password or three consecutive connection failures mark an own route dead and, in `run`, kill the command, as with a hosted route
- Own routes work with no login and with the Shellroute API unreachable; the version check is skipped for them
- Hosted routes are unchanged

## v0.1.0

Initial public release.

- Interactive mode with `/connect`, `/rotate`, `/disconnect`
- Direct mode: `shellroute run <country> -- <command>`
- Persistent proxy: `shellroute proxy --country <code>`
- 120+ countries, residential and datacenter exits
- Auto-reconnect on connection loss
- Kill switch: traffic blocked when tunnel dies, never leaks to real IP
- City targeting, sticky IPs, IP rotation
- Email OTP login, client-side API key generation
- Pay-per-GB billing with real-time balance tracking
