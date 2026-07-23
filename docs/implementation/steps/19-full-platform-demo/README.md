# Steps for epic 19-full-platform-demo (capstone)

Epic: [`../../epics/19-full-platform-demo.md`](../../epics/19-full-platform-demo.md) · Status: **Complete**

Capstone polyglot product exercising the whole platform. **Capstone folder: `demos/09-full-platform`** (thematic id `09`). The Identity epic demo `demos/09-platform-identity` is a **separate** folder — do not confuse or merge them.

| Step | Title | Status | Depends on |
|---|---|---|---|
| [19.01](19.01-polyglot-product-scaffold.md) | Polyglot sample product | Complete | 01 |
| [19.02](19.02-deploy-path.md) | Deploy path: Build→Runtime→Gateway→Events | Complete | 19.01, 06/02/04/05/11 |
| [19.03](19.03-identity-secrets-observe-storage-db.md) | Identity, Secrets, Observe, Storage, managed DB | Complete | 19.02, 09/10/12/13/18 |
| [19.04](19.04-models-agents-memory.md) | Models + Agents + Memory for diagnosis | Complete | 19.03, 14/15/17 |
| [19.05](19.05-failure-injection-workflow.md) | Failure injection + Workflows approval/rollback | Complete | 19.04, 16/07 |
| [19.06](19.06-acceptance-suite-and-gate.md) | `demos/09-full-platform` acceptance suite + docs | Complete | 19.05, all epics |

Epic gate: `make demo DEMO=09-full-platform` then `make demo-accept DEMO=09-full-platform`.
