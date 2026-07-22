# Repository layout

```text
forge-platform/
├── specs.md                 # product + coarse roadmap
├── Makefile                 # root developer interface
├── compose.yaml             # local foundation (+ later services)
├── .env.example
├── docs/                    # structured documentation (source of truth for delivery)
│   ├── README.md
│   ├── architecture/
│   ├── concepts/
│   ├── contracts/
│   ├── development/
│   ├── operations/
│   ├── testing/
│   ├── decisions/
│   └── implementation/      # epics, atomic steps, agent prompts
├── contracts/               # OpenAPI / Protobuf / events
├── services/                # platform services (one dir each)
├── tools/                   # CLI and developer tools
├── packages/                # optional SDKs
├── infrastructure/          # Compose dependency configs
├── demos/                   # numbered acceptance demos
├── templates/               # service scaffold
├── scripts/                 # wait/smoke/reset helpers
└── tests/                   # repo-level suites
```

## Delivery docs vs specs

| Document | Role |
|---|---|
| `specs.md` | vision, principles, coarse epic roadmap |
| `docs/implementation/epics/` | capability plans |
| `docs/implementation/steps/` | atomic implementable units |
| `docs/implementation/progress.md` | live status board |

Services are **not** “one commit / one step”. Plan each service epic into multiple steps before implementing.
