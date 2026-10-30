# Verification record

At source handoff, execution is pending. No integration, image build or HTTP smoke pass is claimed by this document. Update this record with actual run URLs/results after executing the workflow.

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

`REQUIRE_DB_TESTS=1` makes a missing test database fatal. Tests reject any configured database name except `reserveflow_test`. Each test owns a random schema. CI uses real PostgreSQL and a compiled Go test harness.

The HTTP smoke creates synthetic data and checks health, authentication, create, replay, conflicting key, tenant isolation, stock exhaustion, confirm and resulting stock. Its private report contains method, path, status and replay flag, never API keys.

Static checks at source handoff: `gofmt` parsed and formatted all Go source; `node --check` parsed both Node scripts; PowerShell's parser accepted all `.ps1` files; OpenAPI JSON parsed. No Go compiler or package installation was run at that stage. A compiled test binary or OpenAPI file alone is not evidence that the business scenarios passed.
