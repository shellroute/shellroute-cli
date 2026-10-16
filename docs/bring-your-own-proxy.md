# Bring your own proxy

Route a command or an interactive shell through a proxy you already have. No Shellroute account is needed: nothing is logged in, metered or billed. The hosted routes stay the zero-setup option; your own proxy is the free one.

## Save a proxy and use it

```bash
# Save it once. The URL carries the credentials.
shellroute upstream add work http://user:pass@proxy.example.com:8080

# Check it: opens one tunnel and reports the exit IP and where it appears to be.
shellroute upstream test work
#   ✓ via work · exit IP 91.198.0.12 · DE (observed) · 212 ms

# Use its name wherever a country goes.
shellroute run work -- curl https://ipinfo.io/json
shellroute proxy --upstream work          # persistent local proxy on 127.0.0.1:41900
shellroute                                # interactive shell, then: /connect work

shellroute upstream list                  # passwords shown as ***
shellroute upstream remove work
```

Without saving:

```bash
shellroute run --proxy socks5://127.0.0.1:1080 -- curl https://ipinfo.io/json
SHELLROUTE_PROXY=http://user:pass@proxy.example.com:8080 shellroute run -- curl https://ipinfo.io/json   # CI
shellroute upstream test https://user:pass@proxy.example.com:443
```

## Proxy URLs

| Scheme | Meaning |
|---|---|
| `http://host:port` | HTTP proxy; HTTPS traffic goes through `CONNECT` |
| `https://host:port` | the same, with TLS to the proxy itself. The proxy's certificate must be trusted by your system |
| `socks5://host:port` | SOCKS5. Target names are resolved by the proxy |
| `socks5h://host:port` | the same as `socks5://` |

- The port is required.
- Credentials go in the URL: `scheme://user:pass@host:port`. URL-encode special characters: `@` becomes `%40`, `:` becomes `%3A`, `/` becomes `%2F`, `#` becomes `%23`.
- IPv6 hosts go in brackets: `http://[2001:db8::1]:8080`.
- Your provider's documentation gives the hostname, port and username format. Many providers select the country or a sticky session through the username; paste it into the URL as given, URL-encoded.

Saved names are 3 to 32 characters: lowercase letters, digits, `_` or `-`. A name can never look like a two-letter country code, so `shellroute run work` and `shellroute run DE` never collide.

## How `shellroute run` picks the route

In this order:

1. `--proxy <url>`: your own proxy, this run only.
2. `--country CC`: a hosted route.
3. A two-letter word before `--`: a hosted country, as before.
4. Any other word before `--`: one of your saved upstreams.
5. Nothing given: `SHELLROUTE_PROXY` from the environment if set, else `default_country` from the config.

There is no country setting for an own proxy. Shellroute cannot know where a third-party proxy exits, so it looks instead: when a route starts, the exit IP service that returns the IP also reports a country for it, and that is shown as `DE (observed)` in `upstream test`, `run`, `status` and the shell prompt. It is a measurement from a public geo-IP database, usually right and occasionally wrong, and it is empty when the service does not report one.

## What stays the same

The child command only ever sees the local proxy on `127.0.0.1`, the same as with a hosted route. `HTTP_PROXY`, `HTTPS_PROXY`, their lowercase twins, `NO_PROXY` and `NODE_USE_ENV_PROXY` are set the same way, so the [compatibility matrix](compatibility.md) and the Playwright and Puppeteer guidance apply unchanged.

Your proxy's password never reaches the child process, its environment, your shell history or your CI logs. It is stored in `~/.shellroute/config.toml` (mode 600) and shown as `user:***` everywhere else: `upstream list`, `status`, the session file and error messages.

## What is different

- No login, no balance, no cost line. The end-of-run summary shows duration and data only.
- The exit IP and its observed country come from a probe through your proxy (ipinfo.io, then api.ipify.org). `--no-check` skips it; the route then starts without an exit IP line. On a rotating provider later requests may leave from other exits; the probe shows the first one only.
- Rotation is your provider's job. In the shell, `/rotate` says so and does nothing; `/iptype`, `/sticky`, `/cities`, `/countries` and `/balance` answer "not applicable to your own upstream".
- An `https://` proxy must present a certificate your system trusts. A self-signed certificate is refused; on Linux you can point `SSL_CERT_FILE` at the CA file for the run, on macOS the system keychain decides.

## Failure behavior

Starting a route makes one connection attempt with a 10 second timeout and one retry, then fails with the cause:

| Message | Meaning |
|---|---|
| `cannot resolve host proxy.example.com` | the proxy's hostname does not resolve |
| `connection refused` | nothing listens on that host and port |
| `timeout` | the proxy did not answer within 10 seconds |
| `proxy rejected credentials` | wrong username or password (HTTP 407 or SOCKS5 authentication failure) |
| `proxy requires a username and password` | the SOCKS5 proxy wants credentials and the URL has none |
| `proxy returned 403 Forbidden for CONNECT` | the proxy refused the target; its status is forwarded |
| `proxy returned general failure (check the username and password)` | a SOCKS5 proxy that accepted the login exchange refused the connection; some providers answer a wrong password this way |
| `not a SOCKS5 proxy` | the port speaks something else, often HTTP |
| `proxy did not answer the CONNECT request` | the port speaks something else, often SOCKS |

A wrong password is final and not retried. If the tunnel opens but the exit IP service does not answer, the route starts with a warning.

While a route runs, each client request opens its own tunnel through your proxy. One failed connection answers that client with `502` and nothing else changes. A credentials error, or three consecutive connection failures with no success in between, marks the route dead:

- `shellroute run`: active connections are closed and the command is killed, with "Connection lost during execution. Command killed.", exactly as with a hosted route. There is no retry that could make a half-run script look successful.
- `shellroute proxy --upstream`: clients get `503` until you restart it.
- the interactive shell: the prompt shows "Connection lost through work. Run /connect." and blocks commands until you reconnect.

A `4xx` from the proxy for one target is forwarded to the client and does not count towards route death.

## Troubleshooting

- `unknown upstream "work"`: it is not saved. `shellroute upstream list` shows what is; `shellroute upstream add work <url>` saves it.
- `proxy URL needs a port`: add `:8080`, `:1080` or whatever your provider gives.
- `proxy URL is not valid`: URL-encode special characters in the username or password (see above).
- `upstream test` passes but a program is not routed: the program does not read the proxy environment variables. See the [compatibility matrix](compatibility.md).
- `upstream test` reports an unexpected country: the exit is the provider's choice. Check the username or port format in the provider's documentation. Geo-IP databases also misplace some addresses.
