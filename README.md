# Shellroute CLI

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

**Run your command from another country.**

See what your site and API do for a visitor in Germany, the US or Japan. Shellroute runs one command or one shell through a proxy in that country; the rest of your machine stays on its normal connection. Works with any HTTP or SOCKS5 proxy you already have, or with Shellroute's built-in routes. Learn more at [shellroute.com](https://shellroute.com/).

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

## Quick start with your own proxy

```bash
shellroute upstream add your-proxy-name http://USER:PASSWORD@HOST:PORT
shellroute run your-proxy-name -- curl https://ipinfo.io/json
```

## Quick start with Shellroute's routes

```bash
shellroute login
shellroute run DE -- curl https://ipinfo.io/json
```

Start with $1 of credit, no card needed. [Read the full quickstart](https://shellroute.com/docs/quickstart?utm_source=github&utm_medium=readme&utm_campaign=exp_ghconv_01).

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
| `shellroute run your-proxy-name -- <cmd>` | Run one command through your own proxy (`your-proxy-name` is the alias you saved) |
| `shellroute proxy --upstream your-proxy-name` | Persistent local proxy over your own proxy |
| `shellroute upstream add\|list\|remove\|test` | Save, list, delete and check your own proxies |

#### Local proxy mode

For tools configured with an explicit proxy URL:

```bash
shellroute proxy --country US

# In another terminal
curl -x http://127.0.0.1:41900 https://ipinfo.io/ip
```

### Use your own proxy

Already have a proxy, from a provider or your own server? Save it under a name you choose and use that name wherever a country code goes. No Shellroute account is needed and nothing is billed.

`your-proxy-name` below is a placeholder: the alias you pick when saving the proxy, 3 to 32 characters of lowercase letters, digits, `_` or `-`. Replace `USER`, `PASSWORD`, `HOST` and `PORT` with what your provider gave you.

```bash
# 1. Save it. Schemes: http, https (TLS to the proxy), socks5, socks5h.
shellroute upstream add your-proxy-name http://USER:PASSWORD@HOST:PORT

# 2. Check it: one tunnel, exit IP, observed country, latency.
shellroute upstream test your-proxy-name
#   ✓ via your-proxy-name · exit IP 91.198.0.12 · DE (observed) · 212 ms

# 3. Use it.
shellroute run your-proxy-name -- curl https://ipinfo.io/json   # one command
shellroute                                                       # interactive shell, then: /connect your-proxy-name
shellroute proxy --upstream your-proxy-name                      # persistent local proxy on 127.0.0.1:41900

# Without saving, for example in CI:
shellroute run --proxy socks5://USER:PASSWORD@HOST:PORT -- curl https://ipinfo.io/json
SHELLROUTE_PROXY=http://USER:PASSWORD@HOST:PORT shellroute run -- curl https://ipinfo.io/json

# Manage saved proxies. Passwords are shown as ***.
shellroute upstream list
shellroute upstream remove your-proxy-name
```

The command only ever sees the local proxy on `127.0.0.1`, so your proxy's credentials never reach it, your shell history or your CI logs. Where the proxy exits is observed when the route starts, not declared; a different exit per request is normal on rotating plans. Tested against 3proxy, gost, squid, SSH tunnels and the providers listed in [docs/upstream-compatibility.md](docs/upstream-compatibility.md). [Details.](docs/bring-your-own-proxy.md)

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

Shellroute implements each active route as a local HTTP proxy and provides standard proxy environment variables to the shell or child process. Clients that use those variables send requests through the selected proxy. Each session remains independent, while shellroute manages credentials, rotation, usage, and cleanup.

[See what shellroute proxies.](docs/compatibility.md)

## Important

The shellroute CLI is open source and connects to the shellroute service. The service uses prepaid credits. See [pricing](https://shellroute.com/pricing) and the [acceptable use policy](https://shellroute.com/acceptable-use).

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
