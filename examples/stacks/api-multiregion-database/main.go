package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
)

const (
	defaultPort       = "8080"
	defaultOrderLimit = 20
	maxOrderLimit     = 100
)

type orderStore interface {
	Ready(context.Context) error
	AddOrder(context.Context, addOrderParams) (Order, error)
	ListOrders(context.Context, int) ([]Order, error)
	CountOrders(context.Context) (int64, error)
}

type postgresOrderStore struct {
	db          *sql.DB
	schemaMu    sync.Mutex
	schemaReady bool
}

func newPostgresOrderStore(db *sql.DB) *postgresOrderStore {
	return &postgresOrderStore{db: db}
}

func (s *postgresOrderStore) Ready(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return err
	}
	return s.ensureSchema(ctx)
}

func (s *postgresOrderStore) AddOrder(ctx context.Context, params addOrderParams) (Order, error) {
	if err := validateAddOrder(params); err != nil {
		return Order{}, err
	}
	if err := s.ensureSchema(ctx); err != nil {
		return Order{}, err
	}
	row := s.db.QueryRowContext(ctx, `
INSERT INTO orders (customer, sku, quantity)
VALUES ($1, $2, $3)
RETURNING id, customer, sku, quantity, created_at
`, params.Customer, params.SKU, params.Quantity)
	return scanOrder(row)
}

func (s *postgresOrderStore) ListOrders(ctx context.Context, limit int) ([]Order, error) {
	limit, err := normalizeLimit(limit)
	if err != nil {
		return nil, err
	}
	if err := s.ensureSchema(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, customer, sku, quantity, created_at
FROM orders
ORDER BY id DESC
LIMIT $1
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []Order
	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return orders, nil
}

func (s *postgresOrderStore) CountOrders(ctx context.Context) (int64, error) {
	if err := s.ensureSchema(ctx); err != nil {
		return 0, err
	}
	var count int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *postgresOrderStore) ensureSchema(ctx context.Context) error {
	s.schemaMu.Lock()
	defer s.schemaMu.Unlock()
	if s.schemaReady {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS orders (
	id BIGSERIAL PRIMARY KEY,
	customer TEXT NOT NULL,
	sku TEXT NOT NULL,
	quantity INTEGER NOT NULL CHECK (quantity > 0),
	created_at TIMESTAMPTZ NOT NULL DEFAULT now()
)
`)
	if err != nil {
		return err
	}
	s.schemaReady = true
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanOrder(row scanner) (Order, error) {
	var order Order
	if err := row.Scan(&order.ID, &order.Customer, &order.SKU, &order.Quantity, &order.CreatedAt); err != nil {
		return Order{}, err
	}
	return order, nil
}

type Order struct {
	ID        int64     `json:"id"`
	Customer  string    `json:"customer"`
	SKU       string    `json:"sku"`
	Quantity  int       `json:"quantity"`
	CreatedAt time.Time `json:"created_at"`
}

type addOrderParams struct {
	Customer string `json:"customer"`
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
}

type listOrdersParams struct {
	Limit int `json:"limit,omitempty"`
}

type rpcServer struct {
	store  orderStore
	region string
}

func newRPCServer(store orderStore, region string) *rpcServer {
	return &rpcServer{store: store, region: region}
}

func (s *rpcServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/readyz", s.handleReady)
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/rpc", s.handleRPC)
	return mux
}

func (s *rpcServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"service": "orders-rpc",
		"region":  s.region,
		"methods": []string{"orders.add", "orders.list"},
	})
}

func (s *rpcServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeText(w, http.StatusOK, "ok\n")
}

func (s *rpcServer) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.store.Ready(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Sprintf("database not ready: %v", err))
		return
	}
	writeText(w, http.StatusOK, "ok\n")
}

func (s *rpcServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	count, err := s.store.CountOrders(ctx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, fmt.Sprintf("database not ready: %v", err))
		return
	}
	writeText(w, http.StatusOK, fmt.Sprintf("orders_rpc_orders_total %d\n", count))
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcFailure struct {
	status  int
	code    int
	message string
}

func (e rpcFailure) Error() string {
	return e.message
}

func (s *rpcServer) handleRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "rpc endpoint requires POST")
		return
	}

	req, failure := decodeRPCRequest(w, r)
	if failure != nil {
		writeRPCError(w, *failure, nil)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	result, err := s.dispatch(ctx, req)
	if err != nil {
		var failure rpcFailure
		if errors.As(err, &failure) {
			writeRPCError(w, failure, req.ID)
			return
		}
		writeRPCError(w, rpcFailure{status: http.StatusInternalServerError, code: -32603, message: err.Error()}, req.ID)
		return
	}

	writeJSON(w, http.StatusOK, rpcResponse{
		JSONRPC: "2.0",
		ID:      rpcID(req.ID),
		Result:  result,
	})
}

func decodeRPCRequest(w http.ResponseWriter, r *http.Request) (rpcRequest, *rpcFailure) {
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()

	var req rpcRequest
	if err := decoder.Decode(&req); err != nil {
		return rpcRequest{}, &rpcFailure{status: http.StatusBadRequest, code: -32700, message: "invalid JSON-RPC request"}
	}
	if req.JSONRPC != "2.0" {
		return rpcRequest{}, &rpcFailure{status: http.StatusBadRequest, code: -32600, message: "jsonrpc must be 2.0"}
	}
	if strings.TrimSpace(req.Method) == "" {
		return rpcRequest{}, &rpcFailure{status: http.StatusBadRequest, code: -32600, message: "method is required"}
	}
	return req, nil
}

func (s *rpcServer) dispatch(ctx context.Context, req rpcRequest) (any, error) {
	switch req.Method {
	case "orders.add":
		var params addOrderParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, rpcFailure{status: http.StatusBadRequest, code: -32602, message: err.Error()}
		}
		order, err := s.store.AddOrder(ctx, params)
		if err != nil {
			return nil, mapRPCError(err)
		}
		return map[string]Order{"order": order}, nil
	case "orders.list":
		var params listOrdersParams
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, rpcFailure{status: http.StatusBadRequest, code: -32602, message: err.Error()}
		}
		orders, err := s.store.ListOrders(ctx, params.Limit)
		if err != nil {
			return nil, mapRPCError(err)
		}
		return map[string]any{"orders": orders}, nil
	default:
		return nil, rpcFailure{status: http.StatusNotFound, code: -32601, message: "method not found"}
	}
}

func decodeParams(raw json.RawMessage, out any) error {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		raw = []byte("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("invalid params: %w", err)
	}
	return nil
}

var errInvalidParams = errors.New("invalid params")

func validateAddOrder(params addOrderParams) error {
	if strings.TrimSpace(params.Customer) == "" {
		return fmt.Errorf("%w: customer is required", errInvalidParams)
	}
	if strings.TrimSpace(params.SKU) == "" {
		return fmt.Errorf("%w: sku is required", errInvalidParams)
	}
	if params.Quantity <= 0 {
		return fmt.Errorf("%w: quantity must be greater than zero", errInvalidParams)
	}
	return nil
}

func normalizeLimit(limit int) (int, error) {
	if limit == 0 {
		return defaultOrderLimit, nil
	}
	if limit < 0 {
		return 0, fmt.Errorf("%w: limit must not be negative", errInvalidParams)
	}
	if limit > maxOrderLimit {
		return maxOrderLimit, nil
	}
	return limit, nil
}

func mapRPCError(err error) error {
	if errors.Is(err, errInvalidParams) {
		return rpcFailure{status: http.StatusBadRequest, code: -32602, message: err.Error()}
	}
	return err
}

func writeRPCError(w http.ResponseWriter, failure rpcFailure, id json.RawMessage) {
	writeJSON(w, failure.status, rpcResponse{
		JSONRPC: "2.0",
		ID:      rpcID(id),
		Error: &rpcError{
			Code:    failure.code,
			Message: failure.message,
		},
	})
}

func rpcID(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	payload, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(payload, '\n'))
}

func writeText(w http.ResponseWriter, status int, body string) {
	w.Header().Set("content-type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func writeError(w http.ResponseWriter, status int, body string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": body})
}

func openDatabase(databaseURL string) (*sql.DB, error) {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	return db, nil
}

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		log.Fatal("DATABASE_URL is required; bind the Skiff managed database as DATABASE_URL")
	}

	db, err := openDatabase(databaseURL)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	server := newRPCServer(newPostgresOrderStore(db), firstNonEmpty(os.Getenv("SKIFF_REGION"), os.Getenv("AWS_REGION")))
	port := firstNonEmpty(os.Getenv("PORT"), defaultPort)
	if _, err := strconv.Atoi(port); err != nil {
		log.Fatalf("PORT must be numeric: %v", err)
	}

	addr := ":" + port
	log.Printf("orders rpc server listening on %s", addr)
	if err := http.ListenAndServe(addr, server.routes()); err != nil {
		log.Fatal(err)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
