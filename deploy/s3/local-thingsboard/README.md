# S3 local ThingsBoard CE profile

This profile replaces the lightweight local device simulator with ThingsBoard CE 4.3.1.3 and three virtual HVAC devices.

Virtual devices:

- AHU-01
- FCU-02
- Chiller-03

ThingsBoard UI and REST access is bound to `http://127.0.0.1:18080`. The HVAC command page remains available at `http://127.0.0.1:5173/commands`; its internal Gateway port-forward uses `127.0.0.1:18081`. Kubernetes workloads remain `ClusterIP` only. Generated credentials, device access values, rendered manifests, and reports are written under ignored `out/s3-local-thingsboard/`.

Commands:

```bash
node scripts/s3-local-thingsboard.mjs up
node scripts/s3-local-thingsboard.mjs status
node scripts/s3-local-thingsboard.mjs smoke ahu-01
node scripts/s3-local-thingsboard.mjs smoke fcu-02
node scripts/s3-local-thingsboard.mjs smoke chiller-03
node scripts/s3-local-thingsboard.mjs down
```

Each device Smoke enforces a 15-second submit-to-`SUCCEEDED / VERIFIED` ceiling and records `terminalDurationMs` in `out/s3-local-thingsboard/web-smoke-<device>.json`. This is a local integration performance gate, not a production SLO.

This is a local integration profile only. It does not produce formal S3 certification evidence and does not enable production traffic.
