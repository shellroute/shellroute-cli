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

The CLI is open source and connects to the shellroute service. The service uses prepaid credits.

[GitHub](https://github.com/shellroute/shellroute-cli) · [Pricing](https://shellroute.com/pricing) · [Compatibility](https://shellroute.com/docs/compatibility) · [Acceptable use](https://shellroute.com/acceptable-use)
