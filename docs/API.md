# REST API behavior

The complete machine-readable contract is [openapi.json](../internal/api/openapi.json), served at `GET /openapi.json`. OpenAPI is a JSON document; this project does not bundle Swagger UI or a dashboard.

## Authentication

Use `Authorization: Bearer <64-character-local-key>`. Alpha and Beta have different keys. The API hashes the supplied key and compares it with configured SHA-256 digests using constant-time comparison. It does not log authorization headers. Tenant selection comes from the matched key; request JSON cannot select a tenant.

## Products

```http
POST /v1/products
Authorization: Bearer <alpha-key>
Content-Type: application/json

{"sku":"PAPER-A4","name":"A4 paper box","onHand":5}
```

SKU starts with an uppercase letter or digit and then uses uppercase letters, digits, `_` or `-`, at most 40 characters. Name is trimmed and accepts 1..120 Unicode characters. Initial `onHand` is an integer 0..1000000. All three fields are required. Duplicate SKU under the same tenant returns 409. The same SKU in another tenant is allowed.

Product responses contain `onHand`, `reserved` and `available`. A new product starts with `reserved:0`. Confirmed quantity has already been subtracted from `onHand`.

## Create a hold

```http
POST /v1/reservations
Authorization: Bearer <alpha-key>
Idempotency-Key: hold-demo-0001
Content-Type: application/json

{"productId":1,"quantity":2,"ttlSeconds":300}
```

`quantity` is an integer 1..1000000. TTL is an integer 1..3600 seconds. A valid key has 8..80 ASCII letters, digits, `_` or `-`. Creation returns 201 and an ID. Stock insufficiency returns 409.

Repeat exactly that semantic request with the same key to replay its original status and JSON bytes. The response header includes `Idempotency-Replayed: true`. Changing JSON property order or whitespace preserves the fingerprint. Changing quantity, TTL, product, action or route causes a 409 conflict.

Even a stored 409 remains a 409 after stock is later released. Use a new key for a new business attempt. A replay can contain old state and timestamps; fetch the reservation to inspect current state.

## Confirm or cancel

```http
POST /v1/reservations/1/confirm
Authorization: Bearer <alpha-key>
Idempotency-Key: confirm-demo-0001
Content-Type: application/json

{}
```

Replace `confirm` with `cancel` to release the hold. Each action requires its own key. Confirming/cancelling a terminal reservation with a new key returns 409; replaying the original successful key returns 200. Expired holds return 410. A foreign tenant's ID returns 404.

## List and audit

`GET /v1/products`, `/v1/reservations` and `/v1/audit` accept `limit` (default 20, maximum 100) and `cursor`. Responses contain an `items` array and omit `nextCursor` when there is no following page. Pass a returned cursor unchanged to the same resource with the same tenant. Duplicate or unknown query parameters are rejected.

Audit actions are `product.created`, `reservation.held`, `reservation.confirmed` and `reservation.cancelled`. Expiry is computed at read time and does not generate a stored audit action.

## Errors

```json
{"error":{"code":"insufficient_stock","message":"Requested quantity exceeds available stock."}}
```

| HTTP | Meaning |
|---|---|
| 400 | Invalid object/field, ID, query, cursor or key |
| 401 | Missing or wrong tenant Bearer key |
| 404 | Unknown route or a resource unavailable to this tenant |
| 409 | Duplicate SKU, insufficient stock, invalid terminal transition or reused key |
| 410 | Reservation expired |
| 413 | Body exceeds 4096 bytes |
| 415 | Content-Type is not application/json |
| 503 | Database failure, lock/request timeout or uncertain commit result |

503 includes `Retry-After: 1`. Retry a reservation write with the original key, route and body. Product creation instead uses SKU uniqueness; after an uncertain result, list/read products to reconcile before trying another SKU.

Bodies must contain exactly one JSON object. Unknown fields, extra trailing JSON, arrays and top-level `null` are rejected. Writes accept no query parameters. Clients must never include raw keys in query strings or exported screenshots.
