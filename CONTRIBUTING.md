# Contributing to ShellRoute CLI

## Build from source

```bash
git clone https://github.com/shellroute/shellroute-cli.git
cd shellroute-cli
./scripts/rebuild-cli.sh
./shellroute version
```

## Run all checks

```bash
./scripts/run-tests.sh
```

This runs: go vet, gofmt, build, unit tests (with race detector), public audit, and cross-compile for all platforms.

## PR process

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Run `./scripts/run-tests.sh`
5. Open a pull request

## Optional live test

`test-run-signal-live.sh` verifies that `shellroute run` forwards SIGTERM to the child process, ends the API session cleanly, and then exits by SIGTERM the way the child did. It creates one real paid session and requires `--live`:

```bash
./scripts/test-run-signal-live.sh --live [COUNTRY]
```

Not part of `run-tests.sh` or CI. Run manually before releasing signal-handling changes.

## DCO

All commits must be signed off (`git commit -s`). This certifies you wrote the code or have the right to submit it under the Apache 2.0 license.

```
Signed-off-by: Your Name <your.email@example.com>
```
