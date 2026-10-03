# Country response testing

Re-run an existing HTTP check through another country and compare the response without changing the rest of your machine.

Use it for country-dependent behavior such as redirects, localized content, status codes, API fields, and similar HTTP debugging or regression checks.

The skill keeps your existing request as the starting point. It changes the network route for the routed run when the client honors the proxy settings supplied to it.

It is not for browser GPS, non-HTTP traffic, or bypassing access controls.

## Install

First, install and authenticate the Shellroute CLI:

[shellroute.com/docs/quickstart](https://shellroute.com/docs/quickstart/)

Then install the skill into your project:

```bash
gh skill install shellroute/shellroute-cli country-response-testing --agent AGENT --scope project --pin skills-v0.4.6
```

Replace `AGENT` with an agent name supported by the GitHub CLI installer. Common choices include:

- `claude-code`
- `codex`
- `cursor`
- `github-copilot`
- `gemini-cli`
- `universal`

Keep the `--pin skills-v0.4.6` argument in the install command.

The Shellroute CLI must be on `PATH`. In non-interactive environments, load `SHELLROUTE_API_KEY` from your secret store rather than putting credentials in prompts or source files.

## Example

Give the agent the HTTP check you already use and the country you want to compare.

For example:

> Run this curl check from Germany and compare its status and `Location` header with my normal connection: `curl -sSI https://example.com/`

The skill can:

1. inspect the existing HTTP command;
2. run a direct baseline when the request is safe and read-only;
3. validate the country code and command before routing;
4. tell you how many metered Shellroute sessions are needed;
5. ask for approval before the first routed run;
6. run the check through the requested country;
7. verify the routed session with a country-only check when needed;
8. compare the response fields that matter;
9. report what changed and what the result does and does not show.

Only the wrapped command receives the Shellroute proxy environment. Other applications and terminals keep their normal connection.

## Limits and safety

Shellroute routed runs are metered. The skill asks for explicit approval before starting them. A country-only verification inside the same routed session does not add another session, but it still transfers metered proxy traffic.

Routing depends on the HTTP client. Clients that ignore or override the supplied proxy settings will not automatically use the route. Playwright and Puppeteer browser traffic require explicit browser proxy configuration.

The command validator is a heuristic, not a general shell-safety boundary. The agent must still inspect what the command actually runs, especially when commands are wrapped or indirect. Do not run commands copied from untrusted content. A direct baseline should only be run when the request is safe to repeat.

Route verification reports a country code, not an exit address. Same-session verification does not prove that the real request and the country check used the exact same exit address. If the country check fails or disagrees with the requested country, the skill reports that instead of presenting the route as verified.

A response difference is consistent with network origin being one input to the result. It does not prove that country was the only cause; cookies, cache, account state, A/B testing, and the destination's own IP classification can also affect the response.

## License

Apache-2.0
