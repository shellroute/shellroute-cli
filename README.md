# shellroute CLI

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

Run terminal commands through country-specific residential or datacenter proxies. No VPN, no per-tool configuration. Learn more at [shellroute.com](https://shellroute.com/).

## Install

```bash
npm install -g shellroute
```

Or via Homebrew:

```bash
brew install shellroute/tap/shellroute
```

Or via Go:

```bash
go install github.com/shellroute/shellroute-cli/cmd/shellroute@latest
```

Or download binaries from [GitHub Releases](https://github.com/shellroute/shellroute-cli/releases).

Supports macOS and Linux.

## Quick start

```bash
# Log in (creates account if new)
shellroute login

# Start a proxy
shellroute proxy --country US

# In another terminal
curl -x http://127.0.0.1:41900 https://ipinfo.io/ip
```

## Commands

### Authentication

| Command | Description |
|---|---|
| `shellroute login` | Log in with email OTP |
| `shellroute logout` | Remove stored credentials |
| `shellroute reveal-key` | Print stored API key |

### Proxy

| Command | Description |
|---|---|
| `shellroute` | Interactive shell with `/connect`, `/rotate`, `/disconnect` |
| `shellroute run <country> -- <cmd>` | Run one command through the proxy |
| `shellroute proxy --country <code>` | Persistent proxy (blocks until Ctrl+C) |
| `shellroute proxy stop` | Stop running proxy sessions |

### Info

| Command | Description |
|---|---|
| `shellroute status` | Active sessions |
| `shellroute balance` | Credit balance and rates |
| `shellroute countries` | Available countries |
| `shellroute cities <country>` | Cities in a country |

All info commands support `--format json`.

## Build from source

```bash
git clone https://github.com/shellroute/shellroute-cli.git
cd shellroute-cli
./scripts/rebuild-cli.sh
./shellroute version
```

Run all checks (lint, tests, audit, cross-compile): `./scripts/run-tests.sh`. Requires Go 1.22+.

## How it works

```
Your terminal -> shellroute CLI (local proxy) -> shellroute API -> Gateway -> Exit IP -> Internet
```

The CLI runs a local HTTP proxy on `127.0.0.1`. It sets `HTTP_PROXY`/`HTTPS_PROXY` so tools like curl, Python requests, and Node fetch route through it automatically. Traffic exits through residential or datacenter IPs in 120+ countries.

## Important

The shellroute CLI is open source. It connects to the shellroute service, which requires a paid account. See [shellroute.com](https://shellroute.com/) for pricing and [acceptable use policy](https://shellroute.com/acceptable-use).

## Privacy

- Config stored in `~/.shellroute/config.toml` (mode 600)
- API key generated locally, only the hash is sent to the server
- No analytics or telemetry collected
- Health probe sends a CONNECT to `httpbin.org:443` through the proxy to verify upstream connectivity

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache 2.0. See [LICENSE](LICENSE).

"shellroute" is a trademark. This license covers the code, not the brand. See [NOTICE](NOTICE).
