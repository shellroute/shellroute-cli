# shellroute

**Run your command from another country.**

See what your site and API do for a visitor in Germany, the US or Japan. Shellroute runs one command or one shell through a proxy in that country; the rest of your machine stays on its normal connection. Works with any HTTP or SOCKS5 proxy you already have, or with Shellroute's built-in routes.

## Install

```bash
npm install -g shellroute
```

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

Use this when you need to reproduce country or IP-sensitive behavior from a terminal command, script, or test without changing the rest of your machine.

Start with $1 of credit, no card needed. [Follow the two-minute quickstart](https://shellroute.com/docs/quickstart?utm_source=npm&utm_medium=package-readme&utm_campaign=exp_npmconv_01).

The CLI is open source. With your own proxy no account is needed; Shellroute's built-in routes use prepaid credits.

[GitHub](https://github.com/shellroute/shellroute-cli) · [Pricing](https://shellroute.com/pricing) · [Compatibility](https://shellroute.com/docs/compatibility) · [Acceptable use](https://shellroute.com/acceptable-use)
