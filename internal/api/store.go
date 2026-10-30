package api

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var Schema string

type Server struct {
	pool        *pgxpool.Pool
	alpha, beta [32]byte
	now         func() time.Time
	failpoint   func(string) error
}

func New(pool *pgxpool.Pool, alpha, beta [32]byte) *Server {
	return &Server{pool: pool, alpha: alpha, beta: beta, now: func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }}
}
func (s *Server) fail(stage string) error {
	if s.failpoint != nil {
		return s.failpoint(stage)
	}
	return nil
}
func databaseError() result {
	return problem(503, "database_unavailable", "Database operation failed. Retry with the same Idempotency-Key.")
}

func (s *Server) createProduct(ctx context.Context, tenant string, in productInput) result {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return databaseError()
	}
	defer tx.Rollback(ctx)
	p := Product{SKU: in.SKU, Name: in.Name, OnHand: *in.OnHand, Available: *in.OnHand, CreatedAt: s.now()}
	err = tx.QueryRow(ctx, `INSERT INTO products(tenant,sku,name,on_hand,created_at) VALUES($1,$2,$3,$4,$5) RETURNING id`, tenant, p.SKU, p.Name, p.OnHand, p.CreatedAt).Scan(&p.ID)
	if err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			return problem(409, "sku_exists", "SKU already exists for this tenant.")
		}
		return databaseError()
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant,action,product_id,quantity,happened_at) VALUES($1,'product.created',$2,$3,$4)`, tenant, p.ID, p.OnHand, p.CreatedAt); err != nil {
		return databaseError()
	}
	if err = tx.Commit(ctx); err != nil {
		return databaseError()
	}
	return encoded(201, p)
}

const productSelect = `SELECT p.id,p.sku,p.name,p.on_hand,COALESCE((SELECT sum(r.quantity) FROM reservations r WHERE r.tenant=p.tenant AND r.product_id=p.id AND r.state='held' AND r.expires_at>$2),0)::integer,p.created_at FROM products p WHERE p.tenant=$1`

func scanProduct(row pgx.Row) (Product, error) {
	var p Product
	err := row.Scan(&p.ID, &p.SKU, &p.Name, &p.OnHand, &p.Reserved, &p.CreatedAt)
	p.Available = p.OnHand - p.Reserved
	return p, err
}
func (s *Server) product(ctx context.Context, tenant string, id int64) result {
	p, err := scanProduct(s.pool.QueryRow(ctx, productSelect+` AND p.id=$3`, tenant, s.now(), id))
	if errors.Is(err, pgx.ErrNoRows) {
		return problem(404, "not_found", "Product was not found.")
	}
	if err != nil {
		return databaseError()
	}
	return encoded(200, p)
}
func (s *Server) products(ctx context.Context, tenant string, after int64, limit int) result {
	rows, err := s.pool.Query(ctx, productSelect+` AND p.id>$3 ORDER BY p.id LIMIT $4`, tenant, s.now(), after, limit+1)
	if err != nil {
		return databaseError()
	}
	defer rows.Close()
	out := page[Product]{Items: []Product{}}
	for rows.Next() {
		p, e := scanProduct(rows)
		if e != nil {
			return databaseError()
		}
		out.Items = append(out.Items, p)
	}
	if rows.Err() != nil {
		return databaseError()
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = makeCursor("products", tenant, out.Items[limit-1].ID)
	}
	return encoded(200, out)
}

const reservationSelect = `SELECT id,product_id,quantity,state,expires_at,created_at,updated_at FROM reservations WHERE tenant=$1`

func scanReservation(row pgx.Row) (Reservation, error) {
	var r Reservation
	err := row.Scan(&r.ID, &r.ProductID, &r.Quantity, &r.State, &r.ExpiresAt, &r.CreatedAt, &r.UpdatedAt)
	return r, err
}
func (s *Server) reservation(ctx context.Context, tenant string, id int64) result {
	r, err := scanReservation(s.pool.QueryRow(ctx, reservationSelect+` AND id=$2`, tenant, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return problem(404, "not_found", "Reservation was not found.")
	}
	if err != nil {
		return databaseError()
	}
	return encoded(200, effective(r, s.now()))
}
func (s *Server) reservations(ctx context.Context, tenant string, after int64, limit int) result {
	rows, err := s.pool.Query(ctx, reservationSelect+` AND id>$2 ORDER BY id LIMIT $3`, tenant, after, limit+1)
	if err != nil {
		return databaseError()
	}
	defer rows.Close()
	now := s.now()
	out := page[Reservation]{Items: []Reservation{}}
	for rows.Next() {
		r, e := scanReservation(rows)
		if e != nil {
			return databaseError()
		}
		out.Items = append(out.Items, effective(r, now))
	}
	if rows.Err() != nil {
		return databaseError()
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = makeCursor("reservations", tenant, out.Items[limit-1].ID)
	}
	return encoded(200, out)
}
func (s *Server) events(ctx context.Context, tenant string, after int64, limit int) result {
	rows, err := s.pool.Query(ctx, `SELECT id,action,product_id,reservation_id,quantity,happened_at FROM audit_events WHERE tenant=$1 AND id>$2 ORDER BY id LIMIT $3`, tenant, after, limit+1)
	if err != nil {
		return databaseError()
	}
	defer rows.Close()
	out := page[Event]{Items: []Event{}}
	for rows.Next() {
		var e Event
		if rows.Scan(&e.ID, &e.Action, &e.ProductID, &e.ReservationID, &e.Quantity, &e.HappenedAt) != nil {
			return databaseError()
		}
		out.Items = append(out.Items, e)
	}
	if rows.Err() != nil {
		return databaseError()
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = makeCursor("audit", tenant, out.Items[limit-1].ID)
	}
	return encoded(200, out)
}

func (s *Server) mutate(ctx context.Context, tenant, key, route string, in any, operation func(pgx.Tx) (result, error)) result {
	canonical, _ := json.Marshal(in)
	sum := sha256.Sum256(append([]byte(route+"\n"), canonical...))
	fingerprint := hex.EncodeToString(sum[:])
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return databaseError()
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `INSERT INTO idempotency(tenant,key,fingerprint,created_at) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, tenant, key, fingerprint, s.now())
	if err != nil {
		return databaseError()
	}
	var old string
	var status *int
	var body []byte
	if err = tx.QueryRow(ctx, `SELECT fingerprint,status,body FROM idempotency WHERE tenant=$1 AND key=$2 FOR UPDATE`, tenant, key).Scan(&old, &status, &body); err != nil {
		return databaseError()
	}
	if old != fingerprint {
		return problem(409, "idempotency_conflict", "Idempotency-Key was already used for a different request.")
	}
	if status != nil {
		return result{status: *status, body: body, replay: true}
	}
	out, err := operation(tx)
	if err != nil {
		return databaseError()
	}
	if err = s.fail("before_idempotency"); err != nil {
		return databaseError()
	}
	if _, err = tx.Exec(ctx, `UPDATE idempotency SET status=$3,body=$4 WHERE tenant=$1 AND key=$2`, tenant, key, out.status, out.body); err != nil {
		return databaseError()
	}
	if err = s.fail("before_commit"); err != nil {
		return databaseError()
	}
	if err = tx.Commit(ctx); err != nil {
		return databaseError()
	}
	return out
}

func (s *Server) reserve(ctx context.Context, tenant, key string, in reserveInput) result {
	return s.mutate(ctx, tenant, key, "POST /v1/reservations", in, func(tx pgx.Tx) (result, error) {
		var onHand int
		err := tx.QueryRow(ctx, `SELECT on_hand FROM products WHERE tenant=$1 AND id=$2 FOR UPDATE`, tenant, in.ProductID).Scan(&onHand)
		if errors.Is(err, pgx.ErrNoRows) {
			return problem(404, "not_found", "Product was not found."), nil
		}
		if err != nil {
			return result{}, err
		}
		now := s.now()
		var reserved int
		if err = tx.QueryRow(ctx, `SELECT COALESCE(sum(quantity),0)::integer FROM reservations WHERE tenant=$1 AND product_id=$2 AND state='held' AND expires_at>$3`, tenant, in.ProductID, now).Scan(&reserved); err != nil {
			return result{}, err
		}
		if in.Quantity > onHand-reserved {
			return problem(409, "insufficient_stock", "Requested quantity exceeds available stock."), nil
		}
		r := Reservation{ProductID: in.ProductID, Quantity: in.Quantity, State: "held", CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Duration(in.TTLSeconds) * time.Second)}
		err = tx.QueryRow(ctx, `INSERT INTO reservations(tenant,product_id,quantity,state,expires_at,created_at,updated_at) VALUES($1,$2,$3,'held',$4,$5,$5) RETURNING id`, tenant, r.ProductID, r.Quantity, r.ExpiresAt, now).Scan(&r.ID)
		if err != nil {
			return result{}, err
		}
		if err = s.fail("after_reservation"); err != nil {
			return result{}, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant,action,product_id,reservation_id,quantity,happened_at) VALUES($1,'reservation.held',$2,$3,$4,$5)`, tenant, r.ProductID, r.ID, r.Quantity, now)
		return encoded(201, r), err
	})
}
func (s *Server) transition(ctx context.Context, tenant, key string, id int64, action string) result {
	return s.mutate(ctx, tenant, key, fmt.Sprintf("POST /v1/reservations/%d/%s", id, action), struct{}{}, func(tx pgx.Tx) (result, error) {
		var productID int64
		err := tx.QueryRow(ctx, `SELECT product_id FROM reservations WHERE tenant=$1 AND id=$2`, tenant, id).Scan(&productID)
		if errors.Is(err, pgx.ErrNoRows) {
			return problem(404, "not_found", "Reservation was not found."), nil
		}
		if err != nil {
			return result{}, err
		}
		var onHand int
		if err = tx.QueryRow(ctx, `SELECT on_hand FROM products WHERE tenant=$1 AND id=$2 FOR UPDATE`, tenant, productID).Scan(&onHand); err != nil {
			return result{}, err
		}
		r, err := scanReservation(tx.QueryRow(ctx, reservationSelect+` AND id=$2 FOR UPDATE`, tenant, id))
		if err != nil {
			return result{}, err
		}
		now := s.now()
		r = effective(r, now)
		if r.State == "expired" {
			return problem(410, "reservation_expired", "Reservation has expired. Create a new reservation with a new key."), nil
		}
		if r.State != "held" {
			return problem(409, "invalid_state", "Only a held reservation can be confirmed or cancelled."), nil
		}
		if action == "confirm" {
			if r.Quantity > onHand {
				return result{}, errors.New("stock invariant violated")
			}
			if _, err = tx.Exec(ctx, `UPDATE products SET on_hand=on_hand-$3 WHERE tenant=$1 AND id=$2`, tenant, productID, r.Quantity); err != nil {
				return result{}, err
			}
			r.State = "confirmed"
		} else {
			r.State = "cancelled"
		}
		r.UpdatedAt = now
		if _, err = tx.Exec(ctx, `UPDATE reservations SET state=$3,updated_at=$4 WHERE tenant=$1 AND id=$2`, tenant, id, r.State, now); err != nil {
			return result{}, err
		}
		if err = s.fail("after_transition"); err != nil {
			return result{}, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO audit_events(tenant,action,product_id,reservation_id,quantity,happened_at) VALUES($1,$2,$3,$4,$5,$6)`, tenant, "reservation."+r.State, productID, id, r.Quantity, now)
		return encoded(200, r), err
	})
}
