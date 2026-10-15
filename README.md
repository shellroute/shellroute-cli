# shellroute CLI

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

**Run terminal commands through a proxy.**

Choose a country or city and route a shell session or a single command through a proxy. Your other apps and terminals keep their normal connection. Learn more at [shellroute.com](https://shellroute.com/).

Need to reproduce country-specific behavior? Run the same command through another country while the rest of your machine keeps its normal connection.

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
# Log in. A new account is created automatically.
shellroute login

# Shellroute opens after login. Run these inside it:
/connect US
curl https://ipinfo.io/json
```

### Or route one command

```bash
shellroute run DE -- curl https://ipinfo.io/json
```

[Read the full quickstart](https://shellroute.com/docs/quickstart?utm_source=github&utm_medium=readme&utm_campaign=exp_ghconv_01).

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
| `shellroute run <upstream> -- <cmd>` | Run one command through your own proxy |
| `shellroute upstream add\|list\|remove\|test` | Manage your own proxies |

#### Local proxy mode

For tools configured with an explicit proxy URL:

```bash
shellroute proxy --country US

# In another terminal
curl -x http://127.0.0.1:41900 https://ipinfo.io/ip
```

### Use your own proxy

Already pay a proxy provider, or run one? Save it and use its name wherever a country goes. No account needed.

```bash
shellroute upstream add work http://user:pass@proxy.example.com:8080
shellroute upstream test work                     # via work · exit IP 91.198.0.12 · DE (observed) · 212 ms
shellroute run work -- curl https://ipinfo.io/json
shellroute proxy --upstream work
shellroute run --proxy socks5://127.0.0.1:1080 -- curl https://ipinfo.io/json
```

The command only ever sees the local proxy on `127.0.0.1`, so your proxy's credentials never reach it, your shell history or your CI logs. `http`, `https`, `socks5` and `socks5h` proxies work. [Details.](docs/bring-your-own-proxy.md)

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
