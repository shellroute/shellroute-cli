# Own proxy providers: what has been tested

Results of running the bring-your-own-proxy commands against real proxy providers. Each row is one provider endpoint and scheme, tested with the same five commands: `upstream add`, `upstream test`, `run … curl https://ipinfo.io/json`, `run … curl -I https://store.google.com/`, and a wrong-password run that must be refused before the command starts. A provider missing here has not been tested, which says nothing about whether it works.

Usernames, passwords and account details are not recorded. Exit IPs are not recorded either; the pool changes.

| Date | Provider | Plan | Scheme | Endpoint | Result | Notes |
|---|---|---|---|---|---|---|
| 2026-10-16 | Webshare | free plan | http | an individual proxy address from the dashboard list, port as listed | pass | Fixed exit: the probe and every request observed the same IP, GB. `store.google.com` answered 301. Wrong password: "proxy rejected credentials", command not started. The shared gateway `p.webshare.io:80` rejected this free plan's credentials; the dashboard's per-proxy addresses worked. |
| 2026-10-16 | Evomi | datacenter trial | http | `dcp.evomi.com:2000` | pass | Tunnel opened on the first attempt (about 0.4 s). Exits rotate per connection: the probe observed the US, the next request left from the Philippines. `store.google.com` answered 301. Wrong password: "proxy rejected credentials", command not started. Summary shows duration and data only. |
| 2026-10-16 | Evomi | datacenter trial | https | `dcp.evomi-proxy.com:2001` | pass | TLS to the proxy with its public certificate, no extra setup. Tunnel in about 1.2 s. Exits rotate per connection (US, then Germany). `store.google.com` answered 302. Wrong password: "proxy rejected credentials". |
| 2026-10-16 | Evomi | datacenter trial | socks5 | `dcp.evomi.com:2002` | pass | Tunnel in about 1.1 s. Exits rotate per connection (Malaysia, then GB). `store.google.com` answered 302. Wrong password: the proxy answers "general failure" at the connect step; the CLI reports "proxy returned general failure (check the username and password)" and does not start the command. |

## How to read the rotation note

With a rotating pool every connection may leave from a different exit, and often a different country. `upstream test` and the `via …` line at the start of `run` show the first exit only. If you need one country, select it the way the provider documents, usually through the username or a dedicated port, and check the result with `curl https://ipinfo.io/json` through the route.

## Retesting

```bash
shellroute upstream add work <provider url>
shellroute upstream test work
shellroute run work -- curl -sS https://ipinfo.io/json
shellroute run work -- curl -sS -I https://store.google.com/
shellroute run --proxy <same url with a wrong password> -- true    # must be refused
```

Add a row with the date, provider, plan, scheme and endpoint. Record what happened, never the credentials.
