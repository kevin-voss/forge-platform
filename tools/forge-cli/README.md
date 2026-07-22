# Forge CLI

`forge` is the Go command-line client for Forge Control. It manages CLI-side
endpoint profiles and creates and reads projects, environments, applications,
services, and desired deployments through the Control HTTP API.

## Build and test

```bash
make build
make test
```

Install the binary with `make install` (set `PREFIX` to change the destination).

## Profiles

Configuration is stored at `$XDG_CONFIG_HOME/forge/config.yaml`, or
`~/.config/forge/config.yaml` when `XDG_CONFIG_HOME` is not set. The file is
created with `0600` permissions.

```bash
forge config set endpoint http://127.0.0.1:4001 --profile local
forge config use local
forge config get endpoint
forge config list
```

An endpoint must be an absolute `http` or `https` URL. The effective endpoint
and profile use this precedence: command-line flag, environment variable,
profile file, then the built-in local endpoint `http://127.0.0.1:4001`.

## Global flags

```text
--endpoint URL       Control endpoint URL
--profile NAME       Named configuration profile
--output table|json  Output format (default: table)
--timeout DURATION   HTTP timeout (default: 30s)
--verbose            Emit resolved configuration diagnostics to stderr
```

`FORGE_ENDPOINT`, `FORGE_PROFILE`, `FORGE_OUTPUT`, and `FORGE_TIMEOUT` provide
environment defaults. Command-line flags take precedence over their
corresponding environment variables.

## Output, errors, and timeouts

Resource commands write only results to stdout. `--output json` emits the
Control resource object unchanged in shape for creates and reads, and a JSON
array of resource objects for lists. This makes output safe to pipe:

```bash
forge project list --output json | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))'
```

Table output is the default and uses aligned columns. Diagnostics, including
Control request IDs, are always written to stderr so they never contaminate
JSON output. `--verbose` also writes each successful HTTP method, status,
duration, and `requestId` to stderr.

Every Control HTTP request is cancelled when `--timeout` (or `FORGE_TIMEOUT`)
expires. The default is `30s`.

| Exit code | Meaning |
|---:|---|
| 0 | Success |
| 1 | Unexpected error |
| 2 | Usage or validation error |
| 3 | Control resource not found (HTTP 404) |
| 4 | Control conflict (HTTP 409) |
| 5 | Request timeout or network failure |

## Resources

Resource commands use the resolved Control endpoint and support `--output table`
(the default) and `--output json`.

```bash
forge project create --name acme --slug acme
forge project list
forge project get <project-id>
forge env create --project <project-id> --name dev
forge env list --project <project-id>
forge app create --project <project-id> --name web
forge app list --project <project-id>
forge service create --app <app-id> --name api --port 8080
forge service list --app <app-id>
forge deployment create --service <service-id> --image localhost:5000/demo-go:latest --env <environment-id>
forge deployment status <deployment-id>
forge deployment list --service <service-id>
```

`deployment create` sends an `Idempotency-Key` for safe retries. It generates a
UUID v4 by default; scripts can reuse a value with `--idempotency-key`.

Control errors are printed to stderr with their `requestId` and result in the
documented non-zero exit status.
