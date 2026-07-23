# AWS provider — least-privilege IAM

Forge Infrastructure's `aws` adapter uses only EC2 IaaS primitives: instances, EBS volumes, VPC/subnets/security groups, and Elastic IPs. It does **not** require EKS, ECS, RDS, SQS, Lambda, or CloudWatch.

## Credentials secret

Store a JSON secret referenced by `InfrastructureProvider.spec.credentialsSecretRef`:

```json
{
  "accessKeyId": "AKIA…",
  "secretAccessKey": "…",
  "sessionToken": ""
}
```

Local demo fallback: `FORGE_INFRA_AWS_CREDENTIALS_JSON`.

## Recommended IAM policy

Attach to a dedicated IAM user/role per Forge organization:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "EC2Compute",
      "Effect": "Allow",
      "Action": [
        "ec2:RunInstances",
        "ec2:TerminateInstances",
        "ec2:RebootInstances",
        "ec2:DescribeInstances",
        "ec2:DescribeInstanceTypes",
        "ec2:DescribeRegions",
        "ec2:CreateTags",
        "ec2:DescribeTags"
      ],
      "Resource": "*"
    },
    {
      "Sid": "EC2Network",
      "Effect": "Allow",
      "Action": [
        "ec2:CreateVpc",
        "ec2:DeleteVpc",
        "ec2:DescribeVpcs",
        "ec2:CreateSubnet",
        "ec2:DeleteSubnet",
        "ec2:DescribeSubnets",
        "ec2:CreateSecurityGroup",
        "ec2:DeleteSecurityGroup",
        "ec2:DescribeSecurityGroups",
        "ec2:AuthorizeSecurityGroupIngress",
        "ec2:AuthorizeSecurityGroupEgress"
      ],
      "Resource": "*"
    },
    {
      "Sid": "EBS",
      "Effect": "Allow",
      "Action": [
        "ec2:CreateVolume",
        "ec2:DeleteVolume",
        "ec2:AttachVolume",
        "ec2:DetachVolume",
        "ec2:ModifyVolume",
        "ec2:DescribeVolumes"
      ],
      "Resource": "*"
    },
    {
      "Sid": "ElasticIP",
      "Effect": "Allow",
      "Action": [
        "ec2:AllocateAddress",
        "ec2:ReleaseAddress",
        "ec2:AssociateAddress",
        "ec2:DisassociateAddress",
        "ec2:DescribeAddresses"
      ],
      "Resource": "*"
    },
    {
      "Sid": "OptionalPricing",
      "Effect": "Allow",
      "Action": ["pricing:GetProducts"],
      "Resource": "*"
    }
  ]
}
```

## Tags / idempotency

Every Forge-managed resource is tagged:

| Tag | Purpose |
|---|---|
| `forge.managed=true` | Orphan reconciliation selector |
| `forge.op_id` | Idempotent retry adoption (+ `ClientToken` on `RunInstances`) |
| `forge.nodepool` | Pool-scoped teardown |

## Example InfrastructureProvider

```yaml
apiVersion: forge.dev/v1
kind: InfrastructureProvider
metadata: { name: aws-prod }
spec:
  type: aws
  credentialsSecretRef: { name: aws-prod-credentials }
  defaultRegion: eu-central-1
  config:
    vpcCidr: "10.30.0.0/16"
    orphanGraceMinutes: 15
```
