# Transactions and inventory

## Request path

```mermaid
sequenceDiagram
    participant C as API client
    participant H as Go HTTP handler
    participant P as PostgreSQL
    C->>H: POST reservation + Bearer + Idempotency-Key
    H->>H: Authenticate tenant, validate JSON, normalize input
    H->>P: BEGIN; insert/lock tenant idempotency key
    alt Completed request already exists
        P-->>H: Original status and response bytes
        H-->>C: Replay + Idempotency-Replayed: true
    else New request
        H->>P: Lock tenant product row
        H->>H: Read server clock after lock
        H->>P: Sum unexpired holds
        H->>P: Insert hold + audit + stored response
        H->>P: COMMIT
        H-->>C: 201 hold response
    end
```

## Tables

`products` stores remaining physical stock in `on_hand`. `reservations` stores the hold quantity, terminal state and absolute expiry. A confirmation removes quantity from `on_hand` and changes that reservation to `confirmed` in the same transaction. A cancelled reservation never consumes physical stock.

`idempotency` stores `(tenant,key)`, a SHA-256 fingerprint of route plus normalized JSON, response status and exact response bytes. `audit_events` stores only committed product and reservation transitions. Foreign keys include the tenant, preventing a reservation from pointing to another tenant's product even if application SQL is wrong.

## Why two requests cannot claim the last unit

Every reservation mutation locks its product with `SELECT ... FOR UPDATE`. Only one mutation for that product can inspect capacity at a time. After acquiring the lock, creation computes active held quantity in a new statement under PostgreSQL READ COMMITTED isolation. It therefore sees holds committed by the previous lock owner. All API code that mutates a hold or consumes stock follows this lock rule.

The database checks enforce nonnegative `on_hand`, positive quantity and allowed stored states. Cross-row held quantity is protected by the transaction protocol, not by a SQL CHECK. Direct ad-hoc database writes can bypass that protocol and are outside the API contract.

## Lock order

1. Insert or lock the tenant idempotency record.
2. Lock the tenant product row.
3. Lock the tenant reservation row for confirm/cancel.

Each request modifies one product and at most one reservation. The preliminary reservation query reads its immutable `product_id` without locking; tenant and product ID cannot be changed by any endpoint. The implementation then locks product first and reloads the reservation under lock. This avoids opposing product/reservation lock order.

## Expiry and time

The application supplies UTC timestamps truncated to PostgreSQL microsecond precision. For mutation decisions it reads time after waiting for the product lock. A hold is active only when `expires_at > now`. Equality is expired. The same predicate appears in availability reads and capacity checks.

`held` rows that have expired remain in storage. Reads return a derived `expired` state and capacity queries exclude them. Confirm/cancel after expiry returns 410 and records that error response under its idempotency key. There is no timer, background process, cleanup job or event emitted when time passes. The fake clock exists only on the unexported server field used from Go package tests.

## Idempotency and retries

The first transaction inserts a key with an unfinished response, then completes it before committing. A concurrent insert of that key waits for the first transaction. It subsequently locks the committed row and either replays it or reports a fingerprint conflict. An unfinished row is never intentionally committed by this application.

Struct serialization makes JSON whitespace and property order irrelevant. The route contains the normalized numeric reservation ID and action. Key scopes span routes, so a hold key cannot later be used as a confirmation key.

Status 400/401/413/415 is produced before an idempotency transaction. Resource 404, stock/state 409 and expired 410 produced inside a valid reservation mutation are persisted, even if no inventory changes. Database 503 rolls the transaction back where possible. If the server loses a COMMIT acknowledgement, the client cannot know whether the commit succeeded. Retrying the original key reconciles that uncertainty.

## Read consistency and pagination

Each product query uses one PostgreSQL statement with one captured application timestamp. Reservations and audit pages are ordered by increasing identity ID. `limit+1` determines whether another cursor is available. Cursors include resource, tenant and the last returned ID; they are validated but not cryptographically signed. They are positions, not authorization credentials. Changing the cursor cannot bypass the tenant predicate.

Pagination is not a snapshot spanning several HTTP requests. New rows may appear on later pages, and sequence gaps are normal after a transaction rollback.

## Resource boundary

The pool permits at most 16 connections. Requests have a 10-second context deadline; server header/body/write timeouts bound slow requests. JSON bodies are limited to 4096 bytes and list pages to 100 rows. API Docker memory is 192 MiB and main PostgreSQL is 256 MiB. These are local configuration limits, not measured throughput or production capacity claims.
