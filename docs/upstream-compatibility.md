# Own proxy providers: what has been tested

Results of running the bring-your-own-proxy commands against real proxy providers. Each row is one provider endpoint and scheme, tested with the same five commands: `upstream add`, `upstream test`, `run … curl https://ipinfo.io/json`, `run … curl -I https://store.google.com/`, and a wrong-password run that must be refused before the command starts. A provider missing here has not been tested, which says nothing about whether it works.

Usernames, passwords, plans and account details are not recorded, and neither are exit IPs or dates.

| Provider | Scheme | Endpoint | Result | Notes |
|---|---|---|---|---|
| Webshare | http | an individual proxy address from the dashboard list, port as listed | pass | Fixed exit: the probe and every request leave from the same IP. A wrong password is refused before the command starts. If the shared gateway address rejects your credentials, use the per-proxy addresses from the dashboard. |
| Evomi | http | `dcp.evomi.com:2000` | pass | Rotating pool: each connection may leave from a different exit and country. A wrong password is refused before the command starts. |
| Evomi | https | `dcp.evomi-proxy.com:2001` | pass | TLS to the proxy with its public certificate, no extra setup. Rotating pool as above. |
| Evomi | socks5 | `dcp.evomi.com:2002` | pass | Rotating pool as above. A wrong password comes back as "proxy returned general failure (check the username and password)" and the command does not start. |

## How to read the rotation note

With a rotating pool every connection may leave from a different exit, and often a different country. `upstream test` and the `via …` line at the start of `run` show the first exit only. If you need one country, select it the way the provider documents, usually through the username or a dedicated port, and check the result with `curl https://ipinfo.io/json` through the route.

## Retesting

```bash
shellroute upstream add your-proxy-name <provider url>     # your-proxy-name: any alias you choose
shellroute upstream test your-proxy-name
shellroute run your-proxy-name -- curl -sS https://ipinfo.io/json
shellroute run your-proxy-name -- curl -sS -I https://store.google.com/
shellroute run --proxy <same url with a wrong password> -- true    # must be refused
```

Add a row with the provider, scheme and endpoint. Record what happened, never the credentials, the plan or the exit IPs.
