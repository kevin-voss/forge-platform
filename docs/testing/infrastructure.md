# Infrastructure testing

```bash
make test-infrastructure
```

This suite:

1. starts Compose services if needed
2. waits for readiness endpoints
3. runs `scripts/smoke-test.sh`
4. verifies the Step 00 PostgreSQL bootstrap row
