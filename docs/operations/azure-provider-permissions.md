# Azure provider — least-privilege RBAC

Forge Infrastructure's `azure` adapter uses only ARM compute/network primitives: Virtual Machines, managed disks, VNets/subnets/NSGs, and public IP addresses. It does **not** require AKS, App Service, Service Bus, Azure Database, or Functions.

## Credentials secret

Store a JSON secret referenced by `InfrastructureProvider.spec.credentialsSecretRef`:

```json
{
  "tenantId": "…",
  "clientId": "…",
  "clientSecret": "…",
  "subscriptionId": "…"
}
```

Local demo fallback: `FORGE_INFRA_AZURE_CREDENTIALS_JSON`.

## Recommended RBAC

Create an app registration / service principal and assign a custom role (or scoped Contributor on a dedicated resource group):

```json
{
  "Name": "Forge Infrastructure Operator",
  "IsCustom": true,
  "Description": "Least privilege for Forge azure provider (VM/disk/VNet/public IP only)",
  "Actions": [
    "Microsoft.Resources/subscriptions/resourceGroups/read",
    "Microsoft.Compute/virtualMachines/*",
    "Microsoft.Compute/disks/*",
    "Microsoft.Network/virtualNetworks/*",
    "Microsoft.Network/networkSecurityGroups/*",
    "Microsoft.Network/publicIPAddresses/*",
    "Microsoft.Network/networkInterfaces/*",
    "Microsoft.Resources/subscriptions/locations/read"
  ],
  "NotActions": [
    "Microsoft.ContainerService/*",
    "Microsoft.Web/*",
    "Microsoft.DBforPostgreSQL/*",
    "Microsoft.ServiceBus/*",
    "Microsoft.Sql/*"
  ],
  "AssignableScopes": ["/subscriptions/<subscription-id>/resourceGroups/forge"]
}
```

Prefer scoping the assignment to a single resource group (`config.resourceGroup`, default `forge`) so a leaked SP cannot create resources elsewhere.

## Tags / idempotency

Every Forge-managed resource is tagged:

| Tag | Purpose |
|---|---|
| `forge.managed=true` | Orphan reconciliation selector |
| `forge.op_id` | Idempotent retry adoption before `CreateVM` |
| `forge.nodepool` | Pool-scoped teardown |

## Example InfrastructureProvider

```yaml
apiVersion: forge.dev/v1
kind: InfrastructureProvider
metadata: { name: azure-prod }
spec:
  type: azure
  credentialsSecretRef: { name: azure-prod-credentials }
  defaultRegion: westeurope
  config:
    vnetCidr: "10.40.0.0/16"
    orphanGraceMinutes: 15
    resourceGroup: forge
```
