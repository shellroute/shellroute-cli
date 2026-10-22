# shellroute

**Run terminal commands through a proxy.**

Choose a country or city and route a shell session or a single command through a proxy. Your other apps and terminals keep their normal connection.

## Install

```bash
npm install -g shellroute
```

## First session

```bash
# Log in. A new account is created automatically.
shellroute login

# Shellroute opens after login. Run these inside it:
/connect US
curl https://ipinfo.io/json
```

Or route a single command through another country:

```bash
shellroute run DE -- curl https://ipinfo.io/json
```

Use this when you need to reproduce country or IP-sensitive behavior from a terminal command, script, or test without changing the rest of your machine.

[Follow the two-minute quickstart](https://shellroute.com/docs/quickstart?utm_source=npm&utm_medium=package-readme&utm_campaign=exp_npmconv_01).

## Use your own proxy

Already have a proxy, from a provider or your own server? Save it under a name you choose and use that name wherever a country code goes. No Shellroute account is needed and nothing is billed.

`your-proxy-name` below is a placeholder: the alias you pick when saving the proxy. Replace `USER`, `PASSWORD`, `HOST` and `PORT` with what your provider gave you.

```bash
# 1. Save it. Schemes: http, https (TLS to the proxy), socks5, socks5h.
shellroute upstream add your-proxy-name http://USER:PASSWORD@HOST:PORT

# 2. Check it: one tunnel, exit IP, observed country, latency.
shellroute upstream test your-proxy-name

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

The command only ever sees the local proxy on `127.0.0.1`, so your proxy's credentials never reach it, your shell history or your CI logs. Where the proxy exits is observed when the route starts, not declared; a different exit per request is normal on rotating plans.

The CLI is open source. Shellroute's built-in routes use prepaid credits; your own proxy needs no account.

[GitHub](https://github.com/shellroute/shellroute-cli) · [Pricing](https://shellroute.com/pricing) · [Compatibility](https://shellroute.com/docs/compatibility) · [Acceptable use](https://shellroute.com/acceptable-use)
