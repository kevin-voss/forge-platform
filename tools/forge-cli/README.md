# Forge CLI

`forge` is the Go command-line client for Forge Control. This initial slice
provides CLI-side endpoint profiles and global options; resource commands are
added in later steps.

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
