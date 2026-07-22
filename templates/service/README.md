# Service template

Scaffold for future Forge Platform services.

Expected layout:

```text
services/<service-name>/
├── README.md
├── Makefile
├── .env.example
├── config.example.yaml
├── Dockerfile
└── src/ or language-specific roots
```

Required Makefile targets:

```bash
make dev
make build
make run
make test
make test-unit
make test-integration
make lint
make format
make docker-build
make docker-run
make clean
```
