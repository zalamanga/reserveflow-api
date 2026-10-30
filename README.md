# ReserveFlow API

ReserveFlow is a Go REST API for reserving limited inventory. Two requests can compete for the last unit; PostgreSQL row locks decide which hold succeeds. A retry using the same idempotency key receives the original response instead of creating another hold.

This repository contains a backend, PostgreSQL schema, OpenAPI contract and executable HTTP examples. Alpha and Beta are synthetic tenants for demonstrating isolation. There is no payment integration or browser application.

![ReserveFlow OpenAPI reference](docs/screenshots/openapi-reference.png)

This image renders the committed OpenAPI contract with Swagger UI. It is API documentation, not a business dashboard or a recorded response from a running server.

## Run locally

Requirements: Docker Desktop with Linux containers, Docker Compose v2, Node.js 22 or newer, and PowerShell on Windows. Go is compiled inside Docker; a native Go installation is optional.

```powershell
cd "D:\kerjaan\done push repo\reserveflow-api"
.\scripts\Start.ps1
```

The script generates `.env` and `.local/LOCAL-CREDENTIALS.txt` on first use. Open the latter locally to copy a tenant key. These files are ignored by Git. The API container receives key digests, not raw keys.

| Service | Address |
|---|---|
| REST API | `http://127.0.0.1:8099` |
| OpenAPI JSON | `http://127.0.0.1:8099/openapi.json` |
| PostgreSQL | `127.0.0.1:5452`, database/user `reserveflow` |
| Test PostgreSQL | `127.0.0.1:5453`, database `reserveflow_test`, temporary |

```powershell
.\scripts\Test.ps1             # actual PostgreSQL integration tests
node .\scripts\smoke.mjs       # real HTTP requests against the running API
.\scripts\Stop.ps1             # stops this Compose project, preserves inventory
```

`Start.ps1 -NoBuild` and `Test.ps1 -NoBuild` reuse images already built. Main data lives in `reserveflow_pgdata`. Test data uses an isolated tmpfs and is removed after the test run.

## Try the REST API

Import [the Postman collection](docs/reserveflow.postman_collection.json) and [blank local environment](docs/reserveflow.postman_environment.json). Set `alphaApiKey` and `betaApiKey` locally using `.local/LOCAL-CREDENTIALS.txt`. Run requests in their numbered order. Each product-create request generates a new run ID; later requests reuse it so the replay demonstration remains stable.

Alternatively use [requests.http](docs/requests.http) with a REST client, or paste a key into a temporary shell variable:

```powershell
$headers = @{ Authorization = 'Bearer REPLACE_WITH_LOCAL_ALPHA_KEY' }
$product = Invoke-RestMethod -Method Post -Uri 'http://127.0.0.1:8099/v1/products' `
  -Headers $headers -ContentType 'application/json' `
  -Body '{"sku":"PAPER-A4","name":"A4 paper box","onHand":5}'

$headers['Idempotency-Key'] = 'hold-demo-0001'
$body = @{ productId = $product.id; quantity = 2; ttlSeconds = 300 } | ConvertTo-Json
$hold = Invoke-RestMethod -Method Post -Uri 'http://127.0.0.1:8099/v1/reservations' `
  -Headers $headers -ContentType 'application/json' -Body $body

# Repeat this request with the same body and key to replay the original response.
$hold
```

A repeated product SKU returns 409. Product creation intentionally uses SKU uniqueness rather than response replay. Use a fresh SKU for another demo.

## Behavior worth reviewing

- `available = onHand - unexpired held quantity`. Confirming a hold decrements `onHand`; cancelling only releases the hold.
- Reservation creation, confirmation and cancellation require `Idempotency-Key`. The key belongs to a tenant and is shared across these routes. Reusing it for a different route or normalized payload returns 409.
- Each reservation write commits its result, idempotency record and audit event in one transaction. Validation/authentication failures and database failures do not create an idempotency record. Business errors such as insufficient stock do, so retries remain stable.
- Expiry occurs at `expiresAt <= server time`. Reads derive the `expired` state; expired held rows remain stored, but stop counting against availability. There is no background reaper or expiry audit event.
- Mutation lock order is idempotency row, product row, then reservation row when applicable. The effective clock is read after acquiring the product lock.
- Every SQL resource lookup includes the authenticated tenant. Foreign IDs receive 404. Pagination is forward by ID, limited to 100 items, with a tenant/resource-specific cursor.

An idempotent replay is historical. A hold response can still say `held` after that hold expires, because the API returns the original bytes. Read `GET /v1/reservations/{id}` to obtain current state.

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| GET | `/healthz` | Database readiness, no API key |
| GET | `/openapi.json` | API contract, no API key |
| POST / GET | `/v1/products` | Create initial stock / list products |
| GET | `/v1/products/{id}` | Current availability |
| POST / GET | `/v1/reservations` | Create a hold / list reservations |
| GET | `/v1/reservations/{id}` | Current effective state |
| POST | `/v1/reservations/{id}/confirm` | Commit stock consumption |
| POST | `/v1/reservations/{id}/cancel` | Release an unexpired hold |
| GET | `/v1/audit` | Committed events for the current tenant |

All `/v1/` routes require `Authorization: Bearer <key>`. Writes require `Content-Type: application/json`, accept at most 4096 bytes, and reject unknown fields. Confirm/cancel bodies are `{}`. List routes accept only `limit` and `cursor`; writes accept no query parameters.

## Read the implementation

- [API behavior and error examples](docs/API.md)
- [Panduan kode per file dan fungsi](docs/CODE-GUIDE.md)
- [Transaction boundaries and concurrency](docs/ARCHITECTURE.md)
- [Running, stopping, storage and troubleshooting](docs/OPERATIONS.md)
- [Penjelasan untuk interview](docs/INTERVIEW.md)
- [Verification record](docs/VERIFICATION.md)

The test suite includes last-unit contention, concurrent identical keys, rollback injection, exact expiry boundary, confirm/cancel contention, foreign tenant IDs and cursor pagination. See the verification record and GitHub Actions run for actual execution results; the presence of a test is not a passing result.

## Scope and limitations

This is a local portfolio service. Credentials are fixed per synthetic tenant and rotated by deployment configuration; there is no key-management API, user login, rate limiter, payment provider, stock replenishment, deletion or multi-warehouse accounting. Idempotency and audit records have no retention job, and expiry does not purge stored reservations. Initial schema setup is protected by an advisory lock, but this repository does not implement an upgrade migration framework. Public deployment needs TLS, controlled exposure, credential rotation, retention and operational monitoring designed for its workload.
