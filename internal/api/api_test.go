package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const alphaRaw = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const betaRaw = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

type fixture struct {
	s     *Server
	http  *httptest.Server
	clock atomic.Int64
	pool  *pgxpool.Pool
}

func database(t *testing.T) *fixture {
	t.Helper()
	dsn := os.Getenv("RESERVEFLOW_TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("REQUIRE_DB_TESTS") == "1" {
			t.Fatal("RESERVEFLOW_TEST_DATABASE_URL is required")
		}
		t.Skip("set RESERVEFLOW_TEST_DATABASE_URL for actual PostgreSQL tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid test database URL")
	}
	if cfg.ConnConfig.Database != "reserveflow_test" {
		t.Fatal("tests require database name reserveflow_test")
	}
	ctx := context.Background()
	admin, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 8)
	rand.Read(b)
	schema := "test_" + hex.EncodeToString(b)
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	cfg2 := cfg.Copy()
	cfg2.MaxConns = 16
	cfg2.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, Schema); err != nil {
		t.Fatal(err)
	}
	f := &fixture{pool: pool}
	f.clock.Store(time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC).UnixMicro())
	f.s = New(pool, sha256.Sum256([]byte(alphaRaw)), sha256.Sum256([]byte(betaRaw)))
	f.s.now = func() time.Time { return time.UnixMicro(f.clock.Load()).UTC() }
	f.http = httptest.NewServer(f.s.Handler())
	t.Cleanup(func() {
		f.http.Close()
		pool.Close()
		admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	})
	return f
}

type response struct {
	status int
	body   []byte
	replay bool
}

func (f *fixture) request(method, path, key, token, body string) response {
	req, _ := http.NewRequest(method, f.http.URL+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return response{status: 0, body: []byte(err.Error())}
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return response{res.StatusCode, b, res.Header.Get("Idempotency-Replayed") == "true"}
}
func mustStatus(t *testing.T, r response, status int) {
	t.Helper()
	if r.status != status {
		t.Fatalf("expected %d got %d: %s", status, r.status, r.body)
	}
}
func productFor(t *testing.T, f *fixture, sku string, n int) Product {
	t.Helper()
	r := f.request("POST", "/v1/products", "", alphaRaw, fmt.Sprintf(`{"sku":%q,"name":"Test product","onHand":%d}`, sku, n))
	mustStatus(t, r, 201)
	var p Product
	if json.Unmarshal(r.body, &p) != nil {
		t.Fatal("invalid product JSON")
	}
	return p
}
func hold(t *testing.T, f *fixture, p Product, key string) Reservation {
	t.Helper()
	r := f.request("POST", "/v1/reservations", key, alphaRaw, fmt.Sprintf(`{"productId":%d,"quantity":1,"ttlSeconds":60}`, p.ID))
	mustStatus(t, r, 201)
	var v Reservation
	json.Unmarshal(r.body, &v)
	return v
}
func TestCursorAndExpiryBoundary(t *testing.T) {
	raw := makeCursor("products", "alpha", 42)
	n, err := readCursor(raw, "products", "alpha")
	if n != 42 || err != nil {
		t.Fatal("cursor roundtrip failed")
	}
	if _, err = readCursor(raw, "products", "beta"); err == nil {
		t.Fatal("foreign cursor accepted")
	}
	if _, err = readCursor("!!", "products", "alpha"); err == nil {
		t.Fatal("malformed cursor accepted")
	}
	now := time.Now()
	r := Reservation{State: "held", ExpiresAt: now}
	if effective(r, now).State != "expired" || effective(r, now.Add(-time.Microsecond)).State != "held" {
		t.Fatal("incorrect expiry boundary")
	}
}
func TestLastUnitConcurrentRequests(t *testing.T) {
	f := database(t)
	p := productFor(t, f, "LAST-UNIT", 1)
	var wg sync.WaitGroup
	start := make(chan struct{})
	codes := make(chan int, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			r := f.request("POST", "/v1/reservations", fmt.Sprintf("reserve-%03d", i), alphaRaw, fmt.Sprintf(`{"productId":%d,"quantity":1,"ttlSeconds":60}`, p.ID))
			codes <- r.status
		}(i)
	}
	close(start)
	wg.Wait()
	close(codes)
	winners := 0
	for code := range codes {
		if code == 201 {
			winners++
		} else if code != 409 {
			t.Fatalf("unexpected concurrent result %d", code)
		}
	}
	if winners != 1 {
		t.Fatalf("oversell: %d winners", winners)
	}
	r := f.request("GET", fmt.Sprintf("/v1/products/%d", p.ID), "", alphaRaw, "")
	mustStatus(t, r, 200)
	var out Product
	json.Unmarshal(r.body, &out)
	if out.Available != 0 || out.Reserved != 1 || out.OnHand != 1 {
		t.Fatalf("wrong stock %+v", out)
	}
}
func TestConcurrentIdempotencyAndCanonicalReplay(t *testing.T) {
	f := database(t)
	p := productFor(t, f, "IDEMPOTENT", 10)
	body := fmt.Sprintf(`{"productId":%d,"quantity":2,"ttlSeconds":60}`, p.ID)
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan response, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- f.request("POST", "/v1/reservations", "same-key-0001", alphaRaw, body)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	first := ""
	fresh := 0
	for r := range results {
		mustStatus(t, r, 201)
		if first == "" {
			first = string(r.body)
		}
		if first != string(r.body) {
			t.Fatal("replay body differs")
		}
		if !r.replay {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatalf("wanted one original got %d", fresh)
	}
	var count int
	f.pool.QueryRow(context.Background(), `SELECT count(*) FROM reservations`).Scan(&count)
	if count != 1 {
		t.Fatalf("%d effects", count)
	}
	r := f.request("POST", "/v1/reservations", "same-key-0001", alphaRaw, fmt.Sprintf(`{ "ttlSeconds":60,"quantity":2,"productId":%d }`, p.ID))
	mustStatus(t, r, 201)
	if !r.replay || string(r.body) != first {
		t.Fatal("canonical JSON replay differs")
	}
	r = f.request("POST", "/v1/reservations", "same-key-0001", alphaRaw, fmt.Sprintf(`{"productId":%d,"quantity":3,"ttlSeconds":60}`, p.ID))
	mustStatus(t, r, 409)
	var v Reservation
	json.Unmarshal([]byte(first), &v)
	r = f.request("POST", fmt.Sprintf("/v1/reservations/%d/confirm", v.ID), "same-key-0001", alphaRaw, `{}`)
	mustStatus(t, r, 409)
	f.clock.Add(int64(61 * time.Second / time.Microsecond))
	r = f.request("POST", "/v1/reservations", "same-key-0001", alphaRaw, body)
	mustStatus(t, r, 201)
	if string(r.body) != first {
		t.Fatal("replay must keep original held response after expiry")
	}
}
func TestAtomicRollbackAndRetry(t *testing.T) {
	f := database(t)
	p := productFor(t, f, "ROLLBACK", 2)
	var enabled atomic.Bool
	enabled.Store(true)
	f.s.failpoint = func(stage string) error {
		if enabled.Load() && stage == "before_commit" {
			return fmt.Errorf("injected failure")
		}
		return nil
	}
	body := fmt.Sprintf(`{"productId":%d,"quantity":1,"ttlSeconds":60}`, p.ID)
	r := f.request("POST", "/v1/reservations", "rollback-key", alphaRaw, body)
	mustStatus(t, r, 503)
	var holds, idem, events int
	f.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM reservations),(SELECT count(*) FROM idempotency),(SELECT count(*) FROM audit_events WHERE action='reservation.held')`).Scan(&holds, &idem, &events)
	if holds != 0 || idem != 0 || events != 0 {
		t.Fatalf("partial transaction %d/%d/%d", holds, idem, events)
	}
	enabled.Store(false)
	r = f.request("POST", "/v1/reservations", "rollback-key", alphaRaw, body)
	mustStatus(t, r, 201)
	var reservation Reservation
	json.Unmarshal(r.body, &reservation)
	enabled.Store(true)
	r = f.request("POST", fmt.Sprintf("/v1/reservations/%d/confirm", reservation.ID), "confirm-rollback", alphaRaw, `{}`)
	mustStatus(t, r, 503)
	var stock int
	var state string
	f.pool.QueryRow(context.Background(), `SELECT p.on_hand,r.state FROM products p JOIN reservations r ON r.product_id=p.id WHERE r.id=$1`, reservation.ID).Scan(&stock, &state)
	if stock != 2 || state != "held" {
		t.Fatalf("partial confirmation %d/%s", stock, state)
	}
	enabled.Store(false)
	r = f.request("POST", fmt.Sprintf("/v1/reservations/%d/confirm", reservation.ID), "confirm-rollback", alphaRaw, `{}`)
	mustStatus(t, r, 200)
}
func TestExpiredHoldDoesNotBlockOrConfirm(t *testing.T) {
	f := database(t)
	p := productFor(t, f, "EXPIRY", 1)
	old := hold(t, f, p, "old-hold-key")
	f.clock.Store(old.ExpiresAt.UnixMicro())
	boundary := f.request("GET", fmt.Sprintf("/v1/products/%d", p.ID), "", alphaRaw, "")
	mustStatus(t, boundary, 200)
	var available Product
	json.Unmarshal(boundary.body, &available)
	if available.Available != 1 || available.Reserved != 0 {
		t.Fatal("expired hold still blocks read availability")
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan response, 3)
	for _, action := range []string{"confirm", "cancel"} {
		wg.Add(1)
		go func(a string) {
			defer wg.Done()
			<-start
			results <- f.request("POST", fmt.Sprintf("/v1/reservations/%d/%s", old.ID, a), "expire-"+a, alphaRaw, `{}`)
		}(action)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		results <- f.request("POST", "/v1/reservations", "fresh-hold-key", alphaRaw, fmt.Sprintf(`{"productId":%d,"quantity":1,"ttlSeconds":60}`, p.ID))
	}()
	close(start)
	wg.Wait()
	close(results)
	created, gone := 0, 0
	for r := range results {
		if r.status == 201 {
			created++
		} else if r.status == 410 {
			gone++
		} else {
			t.Fatalf("unexpected expiry race %d %s", r.status, r.body)
		}
	}
	if created != 1 || gone != 2 {
		t.Fatal("expiry race broke invariant")
	}
	r := f.request("GET", fmt.Sprintf("/v1/reservations/%d", old.ID), "", alphaRaw, "")
	mustStatus(t, r, 200)
	var v Reservation
	json.Unmarshal(r.body, &v)
	if v.State != "expired" {
		t.Fatal("GET must derive expired state")
	}
}

func TestCancellationReleaseAndConfirmationReplay(t *testing.T) {
	f := database(t)
	p := productFor(t, f, "RELEASE", 2)
	v := hold(t, f, p, "release-hold")
	path := fmt.Sprintf("/v1/reservations/%d/cancel", v.ID)
	first := f.request("POST", path, "release-cancel", alphaRaw, `{}`)
	mustStatus(t, first, 200)
	replayed := f.request("POST", path, "release-cancel", alphaRaw, `{}`)
	mustStatus(t, replayed, 200)
	if !replayed.replay || string(first.body) != string(replayed.body) {
		t.Fatal("cancellation replay changed")
	}
	current := f.request("GET", fmt.Sprintf("/v1/products/%d", p.ID), "", alphaRaw, "")
	var stock Product
	json.Unmarshal(current.body, &stock)
	if stock.OnHand != 2 || stock.Reserved != 0 || stock.Available != 2 {
		t.Fatal("cancellation did not release hold")
	}
	next := f.request("POST", "/v1/reservations", "second-full-hold", alphaRaw, fmt.Sprintf(`{"productId":%d,"quantity":2,"ttlSeconds":60}`, p.ID))
	mustStatus(t, next, 201)
	var full Reservation
	json.Unmarshal(next.body, &full)
	path = fmt.Sprintf("/v1/reservations/%d/confirm", full.ID)
	first = f.request("POST", path, "full-confirm", alphaRaw, `{}`)
	mustStatus(t, first, 200)
	replayed = f.request("POST", path, "full-confirm", alphaRaw, `{}`)
	mustStatus(t, replayed, 200)
	if !replayed.replay || string(first.body) != string(replayed.body) {
		t.Fatal("confirmation replay changed")
	}
	current = f.request("GET", fmt.Sprintf("/v1/products/%d", p.ID), "", alphaRaw, "")
	json.Unmarshal(current.body, &stock)
	if stock.OnHand != 0 || stock.Reserved != 0 || stock.Available != 0 {
		t.Fatal("confirmation consumed stock more than once")
	}
	var count int
	if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_events WHERE action='reservation.confirmed'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("confirmation audit duplicated")
	}
}
func TestConfirmCancelRaceOneTransition(t *testing.T) {
	f := database(t)
	p := productFor(t, f, "RACE", 1)
	v := hold(t, f, p, "race-hold-key")
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan response, 2)
	for _, action := range []string{"confirm", "cancel"} {
		wg.Add(1)
		go func(a string) {
			defer wg.Done()
			<-start
			results <- f.request("POST", fmt.Sprintf("/v1/reservations/%d/%s", v.ID, a), "race-"+a, alphaRaw, `{}`)
		}(action)
	}
	close(start)
	wg.Wait()
	close(results)
	ok, conflict := 0, 0
	for r := range results {
		switch r.status {
		case 200:
			ok++
		case 409:
			conflict++
		default:
			t.Fatalf("unexpected race %d %s", r.status, r.body)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatal("multiple terminal transitions")
	}
	var stock int
	var state string
	var transitions int
	f.pool.QueryRow(context.Background(), `SELECT p.on_hand,r.state,(SELECT count(*) FROM audit_events WHERE action IN ('reservation.confirmed','reservation.cancelled')) FROM products p JOIN reservations r ON r.product_id=p.id WHERE r.id=$1`, v.ID).Scan(&stock, &state, &transitions)
	if transitions != 1 || (state == "confirmed" && stock != 0) || (state == "cancelled" && stock != 1) {
		t.Fatalf("inconsistent terminal state %s %d %d", state, stock, transitions)
	}
}
func TestTenantIsolationPaginationAndValidation(t *testing.T) {
	f := database(t)
	p := productFor(t, f, "ISOLATION", 5)
	v := hold(t, f, p, "tenant-hold-key")
	productFor(t, f, "SECOND", 2)
	productFor(t, f, "THIRD", 3)
	for _, path := range []string{fmt.Sprintf("/v1/products/%d", p.ID), fmt.Sprintf("/v1/reservations/%d", v.ID)} {
		mustStatus(t, f.request("GET", path, "", betaRaw, ""), 404)
	}
	mustStatus(t, f.request("POST", fmt.Sprintf("/v1/reservations/%d/confirm", v.ID), "foreign-confirm", betaRaw, `{}`), 404)
	r := f.request("GET", "/v1/products?limit=2", "", alphaRaw, "")
	mustStatus(t, r, 200)
	var first page[Product]
	json.Unmarshal(r.body, &first)
	if len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatal("missing next page")
	}
	r = f.request("GET", "/v1/products?limit=2&cursor="+first.NextCursor, "", alphaRaw, "")
	mustStatus(t, r, 200)
	var last page[Product]
	json.Unmarshal(r.body, &last)
	if len(last.Items) != 1 || last.NextCursor != "" || last.Items[0].ID <= first.Items[1].ID {
		t.Fatal("keyset page duplicated/skipped data")
	}
	mustStatus(t, f.request("GET", "/v1/products?cursor="+first.NextCursor, "", betaRaw, ""), 400)
	mustStatus(t, f.request("GET", "/v1/products", "", "", ""), 401)
	mustStatus(t, f.request("GET", "/v1/products?limit=101", "", alphaRaw, ""), 400)
	mustStatus(t, f.request("POST", "/v1/reservations", "short", alphaRaw, `{}`), 400)
	mustStatus(t, f.request("POST", "/v1/reservations", "unknown-field", alphaRaw, `{"extra":true}`), 400)
	mustStatus(t, f.request("POST", "/v1/products", "", alphaRaw, `{"sku":"LARGE","name":"`+strings.Repeat("x", 5000)+`","onHand":1}`), 413)
	mustStatus(t, f.request("POST", "/v1/products", "", alphaRaw, `{"sku":"NO-STOCK","name":"Missing stock"}`), 400)
	mediaReq, _ := http.NewRequest("POST", f.http.URL+"/v1/products", strings.NewReader(`{}`))
	mediaReq.Header.Set("Authorization", "Bearer "+alphaRaw)
	mediaReq.Header.Set("Content-Type", "text/plain")
	mediaResponse, mediaErr := http.DefaultClient.Do(mediaReq)
	if mediaErr != nil {
		t.Fatal(mediaErr)
	}
	mediaResponse.Body.Close()
	if mediaResponse.StatusCode != 415 {
		t.Fatal("unsupported media type was accepted")
	}
	mustStatus(t, f.request("POST", fmt.Sprintf("/v1/reservations/%d/cancel", v.ID), "null-input-key", alphaRaw, `null`), 400)
	r = f.request("GET", "/v1/reservations?limit=1", "", betaRaw, "")
	mustStatus(t, r, 200)
	var foreign page[Reservation]
	json.Unmarshal(r.body, &foreign)
	if len(foreign.Items) != 0 {
		t.Fatal("tenant data leaked")
	}
}
