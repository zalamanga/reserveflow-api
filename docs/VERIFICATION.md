# Verification record

GitHub Actions [run 41100211326](https://github.com/zalamanga/reserveflow-api/actions/runs/41100211326) passed for source commit `05e49b5ca2eb1e3fadb1cfc28ce6b0dccfe43c53`. The job ran from 2026-10-30 17:36:09 to 17:38:05 UTC. Direct job logs, rather than only the status summary, were inspected.

- Go API and test harness compiled in the Linux Docker builder.
- All eight named Go tests below passed against actual PostgreSQL. No database scenario was skipped.
- The real API and PostgreSQL containers became healthy, then `scripts/smoke.mjs` completed its 12 HTTP requests successfully.
- Both isolated Compose environments were stopped by the workflow.
- The workflow exported the API and test binaries as `reserveflow-linux-binaries`, artifact ID `12998087253`. The artifact ZIP is 17,497,034 bytes; recorded SHA-256 is `6221fed80dbc78aa75c2236f49deaf4945cecf14665eeef2f170a4ef527655df`.

These results describe the referenced CI source commit. Local runtime execution has not been claimed here. The Postman collection is provided for manual/runner use but was not separately executed by this CI run.

The authored Go suite contains one unit test and seven PostgreSQL/HTTP integration tests:

| Test | Evidence it checks |
|---|---|
| `TestCursorAndExpiryBoundary` | Cursor scope and expiry equality |
| `TestLastUnitConcurrentRequests` | 24 distinct requests compete for one unit; exactly one succeeds |
| `TestConcurrentIdempotencyAndCanonicalReplay` | 16 identical concurrent keys produce one hold; stable bytes; payload/route conflict; replay after expiry |
| `TestAtomicRollbackAndRetry` | Reservation/idempotency/audit rollback and confirmation stock rollback |
| `TestExpiredHoldDoesNotBlockOrConfirm` | Exact expiry concurrent confirm/cancel/new hold |
| `TestConfirmCancelRaceOneTransition` | One terminal transition, consistent stock and audit |
| `TestCancellationReleaseAndConfirmationReplay` | Cancellation releases capacity; repeated confirmation consumes once |
| `TestTenantIsolationPaginationAndValidation` | Foreign IDs, keyset pagination, invalid input and large body |

The job log records each test as `PASS` at 17:37:38..17:37:39 UTC, followed by `PASS`. It records `HTTP smoke passed: 12 requests` at 17:37:57 UTC. Requests include state-changing synthetic demo data; this is a behavioral check, not a throughput benchmark.

`REQUIRE_DB_TESTS=1` makes a missing test database fatal. Tests reject any configured database name except `reserveflow_test`. Each test owns a random schema. CI uses real PostgreSQL and a compiled Go test harness.

The HTTP smoke creates synthetic data and checks health, authentication, create, replay, conflicting key, tenant isolation, stock exhaustion, confirm and resulting stock. Its private report contains method, path, status and replay flag, never API keys.

Static checks at source handoff: `gofmt` parsed and formatted all Go source; `node --check` parsed both Node scripts; PowerShell's parser accepted all `.ps1` files; OpenAPI JSON parsed. No Go compiler or package installation was run at that stage. A compiled test binary or OpenAPI file alone is not evidence that the business scenarios passed.


## OpenAPI reference image

On 31 October 2026 local time, Swagger Parser 12.1.0 validated the committed OpenAPI 3.0.3 contract (11 method/route operations). Its SHA-256 was `a3add4f8d1798d4ae4d68b24a3b2b8ca2ac6f262103643ed59e03e2495505e86`.

The [reference image](screenshots/openapi-reference.png) is a real 1440 x 1100 browser capture of that contract rendered by Swagger UI 5.34.1, with no JavaScript page errors. It displays documented endpoints; it is not an application dashboard or evidence of live API responses. No credentials were entered into the renderer. Runtime and database evidence remains separate above.
