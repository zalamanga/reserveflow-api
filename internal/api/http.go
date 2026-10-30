package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if s.pool.Ping(ctx) != nil {
			write(w, databaseError())
			return
		}
		write(w, encoded(200, map[string]string{"status": "ok"}))
	})
	mux.HandleFunc("GET /openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write(OpenAPI)
	})
	mux.HandleFunc("/v1/", s.auth(s.dispatch))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}
func write(w http.ResponseWriter, out result) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if out.replay {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	if out.status == 503 {
		w.Header().Set("Retry-After", "1")
	}
	w.WriteHeader(out.status)
	w.Write(out.body)
}

type tenantKey struct{}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get("Authorization")
		if len(raw) != 71 || !strings.HasPrefix(raw, "Bearer ") {
			write(w, problem(401, "unauthorized", "A valid tenant Bearer API key is required."))
			return
		}
		sum := sha256.Sum256([]byte(raw[7:]))
		a := subtle.ConstantTimeCompare(sum[:], s.alpha[:])
		b := subtle.ConstantTimeCompare(sum[:], s.beta[:])
		tenant := ""
		if a == 1 {
			tenant = "alpha"
		}
		if b == 1 {
			tenant = "beta"
		}
		if tenant == "" {
			write(w, problem(401, "unauthorized", "A valid tenant Bearer API key is required."))
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), tenantKey{}, tenant)))
	}
}
func decode(w http.ResponseWriter, r *http.Request, v any) *result {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		out := problem(415, "unsupported_media_type", "Content-Type must be application/json.")
		return &out
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	raw, err := io.ReadAll(r.Body)
	if err == nil {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 || raw[0] != '{' {
			out := problem(400, "invalid_json", "Supply a JSON object.")
			return &out
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if err = d.Decode(v); err == nil {
			var trailing any
			if err = d.Decode(&trailing); errors.Is(err, io.EOF) {
				return nil
			}
		}
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		out := problem(413, "body_too_large", "JSON body must not exceed 4096 bytes.")
		return &out
	}
	out := problem(400, "invalid_json", "Supply exactly one JSON object with known fields.")
	return &out
}
func pageArgs(r *http.Request, kind, tenant string) (int64, int, error) {
	q := r.URL.Query()
	for k, v := range q {
		if (k != "limit" && k != "cursor") || len(v) != 1 {
			return 0, 0, errors.New("invalid query")
		}
	}
	limit := 20
	if q.Get("limit") != "" {
		n, e := strconv.Atoi(q.Get("limit"))
		if e != nil || n < 1 || n > 100 {
			return 0, 0, errors.New("invalid limit")
		}
		limit = n
	}
	after, e := readCursor(q.Get("cursor"), kind, tenant)
	return after, limit, e
}
func (s *Server) dispatch(w http.ResponseWriter, r *http.Request) {
	tenant := r.Context().Value(tenantKey{}).(string)
	path := strings.TrimPrefix(r.URL.Path, "/v1/")
	parts := strings.Split(path, "/")
	var out result
	if r.Method == "POST" && len(r.URL.Query()) != 0 {
		write(w, problem(400, "invalid_query", "Write endpoints have no query parameters."))
		return
	}
	if len(parts) == 1 && (path == "products" || path == "reservations" || path == "audit") && r.Method == "GET" {
		after, limit, err := pageArgs(r, path, tenant)
		if err != nil {
			write(w, problem(400, "invalid_pagination", "Use limit 1..100 and a cursor returned for this tenant and resource."))
			return
		}
		switch path {
		case "products":
			out = s.products(r.Context(), tenant, after, limit)
		case "reservations":
			out = s.reservations(r.Context(), tenant, after, limit)
		case "audit":
			out = s.events(r.Context(), tenant, after, limit)
		}
		write(w, out)
		return
	}
	if len(parts) == 2 && (parts[0] == "products" || parts[0] == "reservations") && r.Method == "GET" {
		id, err := parseID(parts[1])
		if err != nil {
			write(w, problem(400, "invalid_id", "ID must be a positive integer."))
			return
		}
		if len(r.URL.Query()) != 0 {
			write(w, problem(400, "invalid_query", "This endpoint has no query parameters."))
			return
		}
		if parts[0] == "products" {
			out = s.product(r.Context(), tenant, id)
		} else {
			out = s.reservation(r.Context(), tenant, id)
		}
		write(w, out)
		return
	}
	if r.Method == "POST" && path == "products" {
		var in productInput
		if err := decode(w, r, &in); err != nil {
			write(w, *err)
			return
		}
		in.Name = strings.TrimSpace(in.Name)
		if !skuPattern.MatchString(in.SKU) || !utf8.ValidString(in.Name) || utf8.RuneCountInString(in.Name) < 1 || utf8.RuneCountInString(in.Name) > 120 || in.OnHand == nil || *in.OnHand < 0 || *in.OnHand > 1000000 {
			write(w, problem(400, "invalid_product", "Use SKU [A-Z0-9_-] 1..40, name 1..120 characters, and onHand 0..1000000."))
			return
		}
		write(w, s.createProduct(r.Context(), tenant, in))
		return
	}
	if r.Method == "POST" && (path == "reservations" || (len(parts) == 3 && parts[0] == "reservations" && (parts[2] == "confirm" || parts[2] == "cancel"))) {
		key := r.Header.Get("Idempotency-Key")
		if !keyPattern.MatchString(key) {
			write(w, problem(400, "invalid_idempotency_key", "Idempotency-Key must contain 8..80 ASCII letters, digits, underscores or hyphens."))
			return
		}
		if path == "reservations" {
			var in reserveInput
			if err := decode(w, r, &in); err != nil {
				write(w, *err)
				return
			}
			if in.ProductID < 1 || in.Quantity < 1 || in.Quantity > 1000000 || in.TTLSeconds < 1 || in.TTLSeconds > 3600 {
				write(w, problem(400, "invalid_reservation", "Use a positive productId, quantity 1..1000000, and ttlSeconds 1..3600."))
				return
			}
			write(w, s.reserve(r.Context(), tenant, key, in))
			return
		}
		id, err := parseID(parts[1])
		if err != nil {
			write(w, problem(400, "invalid_id", "ID must be a positive integer."))
			return
		}
		var in struct{}
		if err := decode(w, r, &in); err != nil {
			write(w, *err)
			return
		}
		write(w, s.transition(r.Context(), tenant, key, id, parts[2]))
		return
	}
	write(w, problem(404, "route_not_found", "API endpoint was not found."))
}
