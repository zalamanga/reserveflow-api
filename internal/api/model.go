package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"time"
)

type Product struct {
	ID        int64     `json:"id"`
	SKU       string    `json:"sku"`
	Name      string    `json:"name"`
	OnHand    int       `json:"onHand"`
	Reserved  int       `json:"reserved"`
	Available int       `json:"available"`
	CreatedAt time.Time `json:"createdAt"`
}
type Reservation struct {
	ID        int64     `json:"id"`
	ProductID int64     `json:"productId"`
	Quantity  int       `json:"quantity"`
	State     string    `json:"state"`
	ExpiresAt time.Time `json:"expiresAt"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
type Event struct {
	ID            int64     `json:"id"`
	Action        string    `json:"action"`
	ProductID     int64     `json:"productId"`
	ReservationID *int64    `json:"reservationId"`
	Quantity      int       `json:"quantity"`
	HappenedAt    time.Time `json:"happenedAt"`
}
type productInput struct {
	SKU    string `json:"sku"`
	Name   string `json:"name"`
	OnHand *int   `json:"onHand"`
}
type reserveInput struct {
	ProductID  int64 `json:"productId"`
	Quantity   int   `json:"quantity"`
	TTLSeconds int   `json:"ttlSeconds"`
}
type page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"nextCursor,omitempty"`
}
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type errorBody struct {
	Error apiError `json:"error"`
}
type result struct {
	status int
	body   []byte
	replay bool
}

func encoded(status int, value any) result {
	b, _ := json.Marshal(value)
	return result{status: status, body: append(b, '\n')}
}
func problem(status int, code, message string) result {
	return encoded(status, errorBody{apiError{code, message}})
}
func effective(r Reservation, now time.Time) Reservation {
	if r.State == "held" && !r.ExpiresAt.After(now) {
		r.State = "expired"
	}
	return r
}

var skuPattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{0,39}$`)
var keyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,80}$`)

func parseID(s string) (int64, error) {
	n, e := strconv.ParseInt(s, 10, 64)
	if e != nil || n < 1 {
		return 0, errors.New("invalid id")
	}
	return n, nil
}

type cursor struct {
	Kind   string `json:"kind"`
	Tenant string `json:"tenant"`
	After  int64  `json:"after"`
}

func makeCursor(kind, tenant string, after int64) string {
	b, _ := json.Marshal(cursor{kind, tenant, after})
	return base64.RawURLEncoding.EncodeToString(b)
}
func readCursor(raw, kind, tenant string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	if len(raw) > 256 {
		return 0, errors.New("invalid cursor")
	}
	b, e := base64.RawURLEncoding.DecodeString(raw)
	if e != nil {
		return 0, e
	}
	var c cursor
	if e = json.Unmarshal(b, &c); e != nil || c.Kind != kind || c.Tenant != tenant || c.After < 1 {
		return 0, errors.New("invalid cursor")
	}
	return c.After, nil
}
