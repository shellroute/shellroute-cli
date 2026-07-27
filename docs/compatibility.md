# Compatibility Matrix

Last tested: 2026-07-27

Shellroute version: 0.1.0
Platform: macOS 26.4 (darwin/arm64)

## How shellroute routes traffic

`shellroute run` and `shellroute` (interactive mode) start a local HTTP proxy and set these environment variables for the child process or session:

```
HTTP_PROXY
HTTPS_PROXY
http_proxy
https_proxy
```

A tool is routed only when it reads and uses these variables, or when it is explicitly configured to use the local proxy. Shellroute does not intercept arbitrary TCP, UDP, DNS, or raw-socket traffic.

## Matrix

Tested 2026-07-27 on macOS 26.4 (darwin/arm64) with shellroute 0.1.0. Each tested row verified: command exit code 0, exit IP differs from direct control, exit country = US, session ended cleanly.

| Client | Version | Outcome | Test command | Condition |
|---|---|---|---|---|
| curl | 8.7.1 | automatic | `shellroute run US -- curl -s https://ipinfo.io/json` | Reads proxy env vars by default. |
| wget | 1.25.0 | automatic | `shellroute run US -- wget -qO- https://ipinfo.io/json` | Reads proxy env vars by default. |
| Python Requests | 2.32.5 | automatic | `shellroute run US -- python3 -c "import requests; print(requests.get('https://ipinfo.io/json').text)"` | Reads proxy env vars by default. `Session.proxies` can override. |
| Python HTTPX (default) | 0.28.1 | automatic | `shellroute run US -- python3 -c "import httpx; print(httpx.get('https://ipinfo.io/json').text)"` | Default `trust_env=True`. |
| Python HTTPX (`trust_env=False`) | 0.28.1 | not transparent | `shellroute run US -- python3 -c "import httpx; print(httpx.get('https://ipinfo.io/json', trust_env=False).text)"` | Bypassed proxy. Returned direct IP. |
| Python urllib | 3.9.6 | automatic | `shellroute run US -- python3 -c "import urllib.request; print(urllib.request.urlopen('https://ipinfo.io/json').read().decode())"` | Default handlers read proxy env vars. |
| aiohttp (default) | 3.13.5 | not transparent | Tested internally: `aiohttp.ClientSession()` without `trust_env` | Did not use proxy env vars. Returned direct IP. |
| aiohttp (`trust_env=True`) | 3.13.5 | explicit configuration | Tested internally: `aiohttp.ClientSession(trust_env=True)` | Requires `trust_env=True` or explicit proxy. |
| Node fetch (default) | v25.8.2 | not transparent | `shellroute run US -- node -e "fetch('https://ipinfo.io/json').then(r=>r.json()).then(d=>console.log(JSON.stringify(d)))"` | Did not use proxy env vars. Returned direct IP. |
| Node fetch (env opt-in) | v25.8.2 | explicit configuration | `shellroute run US -- env NODE_USE_ENV_PROXY=1 node -e "fetch('https://ipinfo.io/json').then(r=>r.json()).then(console.log)"` | Requires `NODE_USE_ENV_PROXY=1` or `--use-env-proxy`. |
| Go `http.Client` (default) | go1.26.2 | automatic | Tested internally: `http.Get(url)` with default transport | Default transport reads proxy env vars. |
| Go `http.Client` (custom) | go1.26.2 | not transparent | Tested internally: `Transport{Proxy: nil}` | Custom transport bypassed proxy. Returned direct IP. |
| Playwright | 1.60.0 | explicit configuration | Tested internally: `npx playwright test` with proxy in config | Requires `proxy: { server: process.env.HTTP_PROXY }` in playwright.config.ts. |
| Puppeteer | 25.1.0 | explicit configuration | Tested internally: Puppeteer with `--proxy-server` arg | Requires `--proxy-server=${process.env.HTTP_PROXY}` in launch args. |
| SSH | — | not tested | `/ssh user@host` in interactive mode | Direct `ssh` does not read proxy env vars. Use shellroute's `/ssh` helper. Not harness-testable (requires interactive mode + SSH server). |
| npm test runner | — | not tested | — | Conditional on the test suite's HTTP clients. Not harness-testable (no single representative fixture). |

## Outcome definitions

- **automatic**: the client uses the injected `HTTP_PROXY`/`HTTPS_PROXY` environment without additional shellroute-specific application configuration.
- **explicit configuration**: works only after documented client option or environment opt-in.
- **conditional**: default transports or child clients work, but the named umbrella command is not sufficient to predict routing.
- **not transparent**: shellroute's HTTP proxy environment does not route this protocol/client by itself.
- **not tested**: no current executable evidence. Expected outcome noted.

## Methodology

Each tested combination runs through this procedure:

1. Capture a direct control request (without shellroute) to `https://ipinfo.io/json`. The direct IP is redacted and not committed.
2. Run the same request inside `shellroute run <country> -- <command>`.
3. A passing result requires:
   - The child command succeeded.
   - The observed public exit IP differs from the redacted direct control.
   - The endpoint reports the selected country.
   - The session shuts down cleanly.
4. Negative tests (e.g., `trust_env=False`, Node fetch default) verify the request bypasses the proxy without publishing the direct IP.
5. Provider failures are retried. A client is not labeled incompatible because of an upstream failure.

## Limitations

- Results apply to the tested versions on the tested platform. Other versions or platforms may differ.
- A successful proxy route proves the request used the expected exit IP. It does not prove target-side localized content, region selection, or anti-bot bypass.
- An HTTP CONNECT proxy tunnels TLS bytes. It does not replace the client's TLS fingerprint.

## Retesting

To retest, log in with `shellroute login`, then run each client through `shellroute run <country> -- <command>` against `https://ipinfo.io/json`. Compare the exit IP and country to a direct control.
