# Running flows in CI

A flow run has the same exit-code contract everywhere (CLI, MCP, HTTP):
0 pass, 1 assertion/`until` failure, 2 error, 3 blocked. That makes
`sapien flow run` a plain CI step — no wrapper script needed to turn a
failed flow into a failed build.

## GitHub Actions

Use the CI-use Docker image (`ghcr.io/gs-sinha/sapien`, distroless,
`sapien` as the entrypoint) so the job needs no Go toolchain:

```yaml
name: API flows

on:
  push:
  pull_request:

jobs:
  flows:
    runs-on: ubuntu-latest
    container:
      image: ghcr.io/gs-sinha/sapien:latest
    steps:
      - uses: actions/checkout@v4

      - name: Run flows
        env:
          SAPIEN_SECRET_STAGING_TOKEN: ${{ secrets.STAGING_TOKEN }}
        run: |
          sapien flow run order-allocation \
            --workspace . --env staging \
            --report junit --out results.xml

      - name: Upload JUnit report
        if: always()
        uses: actions/upload-artifact@v4
        with:
          name: flow-results
          path: results.xml
```

Or, without the container, using the binary directly on the runner:

```yaml
      - name: Install sapien
        run: curl -fsSL https://raw.githubusercontent.com/gs-sinha/sapien/main/scripts/install.sh | sh -s -- --prefix "$HOME/.local/bin"

      - name: Run flows
        env:
          SAPIEN_SECRET_STAGING_TOKEN: ${{ secrets.STAGING_TOKEN }}
        run: |
          export PATH="$HOME/.local/bin:$PATH"
          sapien flow run order-allocation --env staging --report junit --out results.xml
```

Most GitHub Actions JUnit-reporting actions (e.g.
`mikepenz/action-junit-report` or `dorny/test-reporter`) can annotate the
PR directly from `results.xml`; add one after the `upload-artifact` step
if you want inline failure annotations instead of just the archived file.

## Secrets

Environment `auth:` blocks reference `${secret.NAME}` (see
[`getting-started.md`](getting-started.md#environments-and-secrets)).
In CI, set them as `SAPIEN_SECRET_<NAME-upper-cased>` environment
variables — the engine checks the process environment before the OS
keychain, so nothing needs to be installed into a keychain on the
runner:

```sh
SAPIEN_SECRET_STAGING_TOKEN=...     # for ${secret.STAGING_TOKEN}
SAPIEN_SECRET_RIDER_KEY=...         # for ${secret.RIDER_KEY}
```

Name mapping: non-alphanumeric characters become `_` and the name is
upper-cased (`api.key` -> `SAPIEN_SECRET_API_KEY`). Store the actual
values as encrypted GitHub Actions secrets, never in the workflow file
or in an environment YAML.

## Production environments

`flow run` refuses an environment with `production: true` unless
`--allow-production` is passed. Leave that flag off in every workflow
that runs against staging or a mock — it exists so a CI job can't
silently start hitting production because someone typo'd `--env`.

## Multiple flows / a smoke suite

```sh
for f in flows/*.flow.yaml; do
  sapien flow run "$f" --env staging --continue-on-failure \
    --report junit --out "results/$(basename "$f" .flow.yaml).xml"
done
```

`--continue-on-failure` keeps a flow running past a failed step so one
bad assertion doesn't hide the rest of that flow's results; it's
independent of whether you run flows sequentially or in parallel jobs.

## Local equivalent

Before pushing, run the same command locally against the fixture mock
servers (`make fixtures` in one terminal — see the repo
[`README.md`](../README.md#quickstart)):

```sh
sapien flow run order-allocation --env local
```
