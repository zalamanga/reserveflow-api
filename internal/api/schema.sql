CREATE TABLE IF NOT EXISTS products (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant text NOT NULL CHECK (tenant IN ('alpha', 'beta')),
    sku text NOT NULL CHECK (sku ~ '^[A-Z0-9][A-Z0-9_-]{0,39}$'),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 120),
    on_hand integer NOT NULL CHECK (on_hand BETWEEN 0 AND 1000000),
    created_at timestamptz NOT NULL,
    UNIQUE (tenant, sku),
    UNIQUE (tenant, id)
);
CREATE TABLE IF NOT EXISTS reservations (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant text NOT NULL,
    product_id bigint NOT NULL,
    quantity integer NOT NULL CHECK (quantity BETWEEN 1 AND 1000000),
    state text NOT NULL CHECK (state IN ('held', 'confirmed', 'cancelled')),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    FOREIGN KEY (tenant, product_id) REFERENCES products(tenant, id)
);
CREATE INDEX IF NOT EXISTS reservations_active ON reservations(tenant, product_id, expires_at) WHERE state = 'held';
CREATE INDEX IF NOT EXISTS reservations_tenant_id ON reservations(tenant, id);
CREATE TABLE IF NOT EXISTS idempotency (
    tenant text NOT NULL CHECK (tenant IN ('alpha', 'beta')),
    key text NOT NULL CHECK (length(key) BETWEEN 8 AND 80),
    fingerprint text NOT NULL,
    status integer,
    body bytea,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (tenant, key),
    CHECK ((status IS NULL AND body IS NULL) OR (status BETWEEN 200 AND 499 AND body IS NOT NULL))
);
CREATE TABLE IF NOT EXISTS audit_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant text NOT NULL CHECK (tenant IN ('alpha', 'beta')),
    action text NOT NULL CHECK (action IN ('product.created', 'reservation.held', 'reservation.confirmed', 'reservation.cancelled')),
    product_id bigint NOT NULL,
    reservation_id bigint,
    quantity integer NOT NULL CHECK (quantity >= 0),
    happened_at timestamptz NOT NULL,
    FOREIGN KEY (tenant, product_id) REFERENCES products(tenant, id)
);
CREATE INDEX IF NOT EXISTS audit_tenant_id ON audit_events(tenant, id);
