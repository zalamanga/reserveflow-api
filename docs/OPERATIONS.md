# Run and troubleshoot

## Local startup

Use Node.js 22+, Docker Desktop Linux containers and Compose v2. Windows users run `scripts/Start.ps1` from PowerShell. It initializes private credentials, builds the API image and waits for PostgreSQL/API readiness. `Start.ps1 -NoBuild` skips compilation when the local image already exists.

For another shell:

```sh
node scripts/init.mjs
docker compose -f compose.yml -p reserveflow build api
docker compose -f compose.yml -p reserveflow up -d --wait
node scripts/smoke.mjs
docker compose -f compose.yml -p reserveflow down
```

Compose automatically reads `.env`. Never paste `docker compose config` output publicly because interpolation can reveal credentials. Use `config --quiet` for validation.

## Credentials

Read `.local/LOCAL-CREDENTIALS.txt` locally. Raw Alpha/Beta API keys stay in ignored local files and client memory. The API service environment receives only `ALPHA_KEY_SHA256` and `BETA_KEY_SHA256`; PostgreSQL credentials remain necessary to connect. Authorization values are never included in the smoke report.

`init.mjs` validates an existing file and refuses inconsistent digests. Editing `.env` alone does not change PostgreSQL's password inside an existing volume. To rotate the database password, change the database role and deployment configuration together. Do not delete the volume to repair authentication if it contains data you need.

## Storage and ports

Main volume: `reserveflow_pgdata`. Stopping with `Stop.ps1` or `docker compose ... down` preserves it. Tests use no persistent volume. API is bound only to `127.0.0.1:8099`; main DB uses 5452; test DB uses 5453. No service from another project is managed by these scripts.

All repository source, `.local` reports and any manually selected local build caches can remain on D:. Docker image/volume placement follows Docker Desktop's configured disk location. This repository does not change global Docker settings or redirect global TEMP.

For native Go development, use Go 1.27.3 and explicitly place caches/temp under this repository before building:

```powershell
$root = (Get-Location).Path
$env:GOCACHE = Join-Path $root '.local/go-cache'
$env:GOMODCACHE = Join-Path $root '.local/go-mod'
$env:GOTMPDIR = Join-Path $root '.local/tmp'
$env:TEMP = $env:GOTMPDIR
$env:TMP = $env:GOTMPDIR
New-Item -ItemType Directory -Force $env:GOCACHE,$env:GOMODCACHE,$env:GOTMPDIR | Out-Null
go test -p 1 ./...
```

Without a test database URL, the native test command skips database scenarios. Set `REQUIRE_DB_TESTS=1` plus `RESERVEFLOW_TEST_DATABASE_URL` pointing to an isolated database named exactly `reserveflow_test` to require them. Prefer `Test.ps1`, which creates and tears down that environment for you.

## Verification

`Test.ps1` compiles a test harness and runs it against real PostgreSQL. `smoke.mjs` requires the main API running and creates synthetic stock plus a confirmed reservation. Every smoke invocation uses a fresh SKU. It is not a read-only probe and does not delete its evidence rows.

GitHub Actions builds the test harness, runs required PostgreSQL scenarios, exports Linux API/test binaries, starts the real API and runs the HTTP smoke sequence. Artifacts contain binaries only, no `.env`, key files or reports. Root test schemas are isolated and dropped after each test.

## Common failures

| Symptom | Check |
|---|---|
| Docker daemon unavailable | Start Docker Desktop and wait for Linux engine readiness |
| Port already occupied | Check the owning process; choose free ports consistently before startup |
| 401 | Use the correct 64-character local key; Alpha and Beta are different |
| 404 on another tenant's ID | Expected isolation behavior |
| 409 repeated reservation | Inspect error code; replay preserves old stock/state errors |
| 410 | Hold has expired; create a new hold with a new key |
| 415 | Add `Content-Type: application/json`, including for `{}` actions |
| 503 | Inspect database health, then retry reservation write with original key |
| Database password mismatch | Existing volume may hold a different role password; coordinate rotation |

## Deployment boundary

This repo uses a synthetic two-tenant key map, local HTTP and PostgreSQL connections without TLS inside the Compose network. It does not provide public signup, token revocation UI, global rate limiting, retention jobs or production monitoring. Add those according to deployment requirements before exposing it. No external payment, messaging or customer service is contacted.
