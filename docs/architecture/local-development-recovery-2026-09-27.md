# Local development recovery — 2026-09-27

The primary checkout is `E:\Code\HVAC_web`. Run project commands through Ubuntu-24.04 WSL in `/mnt/e/Code/HVAC_web`. The old `/home/haozhang/code/HVAC_web` checkout is inactive.

## Local environment

The fresh environment uses Compose project **hvac-phase1-dev**. Existing `hvac-phase1-local` containers and volumes are retained separately. Always pass the project name explicitly; the launcher otherwise selects the old project.

```bash
cd /mnt/e/Code/HVAC_web
npm run deployment:phase1:wsl -- --project-name hvac-phase1-dev up -d
npm run deployment:phase1:wsl -- --project-name hvac-phase1-dev ps
npm run deployment:phase1:wsl -- --project-name hvac-phase1-dev stop
```

The browser entry is `https://localhost:8443`. The local administrator is `admin`; its generated password is in `deploy/platform/phase1/runtime/local-admin.credentials`. Configuration and credentials are ignored by Git:

- `deploy/platform/phase1/environments/development.runtime.env`
- `deploy/platform/phase1/runtime/` (database role credentials, identity keys, TLS certificates, local monitoring configuration)

The demo deployment tier runs the Web/API, identity, telemetry, metric worker, scheduler, maintenance, Cube, Centrifugo, PostgreSQL, ClickHouse, Redis and core monitoring. Device integration, simulator acceptance and intelligence profiles are not enabled. The database is newly initialized; historical business data has not been imported.

The development CA and certificates were generated locally for one year. Browsers do not trust this private CA automatically. Automated local browser verification uses a dedicated context accepting this certificate. No system trust store was modified.

## Configuration repairs

- The gateway reads the existing mounted route-ownership registry and uses the seeded local tenant through `OIDC_DEFAULT_TENANT_ID`.
- Centrifugo uses HTTPS on port 8000. Telemetry validates its CA; the gateway's Go HTTP client uses `SSL_CERT_FILE` to trust the same local CA. The browser realtime endpoint uses the local HTTPS origin.
- Local Prometheus configuration uses HTTPS and the CA file for Centrifugo. It excludes the disabled IoT service. The WSL overlay mounts this generated configuration.
- Runtime service keys use owner/group access for the image's non-root UID/GID 65532. The CA private key stays owner-only.
- The migration manifest includes the existing `016-s24-alarm-assign-capability.sql`; the existing deployment gate verifies its inclusion.
- The Web Docker build copies the workspace package metadata and the runtime-check script before `npm ci`. A clean install/build passed without dependency-version or lockfile changes after the first installation omitted an optional native package.

## Centrifugo source review

This is configuration of the existing transport, not a new architecture or platform selection. ADOPT the already selected Centrifugo **v6.8.1**, commit `c1246c5472ff9700868ee768e72de2911a3e4f2b`, native HTTP-server TLS configuration; retain the existing subscribe proxy and Redis engine.

Reviewed official files:

- `internal/configtypes/types.go`: `HTTPServer.TLS` and internal TLS semantics.
- `internal/configtypes/tls.go`: certificate loading, CA verification and `tls.Config` creation.
- `internal/configtypes/tls_test.go`: valid certificate/CA configuration and invalid-key rejection.
- `internal/app/mux.go`: server TLS configuration and `ListenAndServeTLS`.
- [Official TLS configuration documentation](https://centrifugal.dev/docs/server/configuration#tls-config-object).

ADAPT with generated local certificates containing the `centrifugo` DNS SAN and both server/client EKUs. REJECT disabling server verification or relaxing the repository's HTTPS requirement. No upstream source was copied into product code.

## Preservation

The initial uncommitted working tree was saved under `out/migration-checkpoint-20260927-174038/` (archive, patch, status and HEAD). This snapshot excludes ignored runtime secrets. Existing changes were not committed, reset or overwritten by the old checkout.

## Verification and remaining product work

Passed: Linux TypeScript checking, local Vite production build, clean Docker Web build, database migration/preflight and `deployment:phase1:check`. Enabled containers are running; all containers with healthchecks are healthy. Prometheus reports all six configured targets up, including the HTTPS Centrifugo endpoint.

Linux Chromium at 1440 × 1000 completed a real administrator login, rendered the site shell, and opened Device Center and Work Orders without API errors. The public realtime WebSocket proxy completed its handshake. This verifies transport connectivity, not device telemetry delivery: the fresh site has no devices. Evidence is saved in `out/local-authenticated-desktop.png`, `out/local-browser-verification.json` and `out/local-browser-verification.log`.

The browser checks also identified existing product integration gaps:

- `GET /api/v1/sites/{siteId}/dashboard-overview` returns `404 ROUTE_NOT_FOUND`; there is no applied route owner. Dashboard aggregates are therefore unavailable, independently of the empty database.
- `GET /api/v1/sites/{siteId}/issues` returns 404. The current page explicitly says issue consolidation has not been connected; original alarms remain separately available.
- FDD findings return 502 because this demo environment does not enable the intelligence profile or FDD service.

These are not successful business-function checks. Connecting the aggregate endpoints and deciding whether to enable the intelligence tier remain separate product/deployment work; this recovery did not fabricate data or weaken authorization to hide them.
