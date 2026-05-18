package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOrdersRPCAddAndList(t *testing.T) {
	store := &memoryOrderStore{now: time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)}
	handler := newRPCServer(store, "test-region").routes()

	addResp := postRPC(t, handler, map[string]any{
		"jsonrpc": "2.0",
		"id":      "add-1",
		"method":  "orders.add",
		"params": map[string]any{
			"customer": "acme",
			"sku":      "sku-123",
			"quantity": 2,
		},
	})
	if addResp.Error != nil {
		t.Fatalf("orders.add error = %+v", addResp.Error)
	}

	var addResult struct {
		Order Order `json:"order"`
	}
	if err := json.Unmarshal(addResp.Result, &addResult); err != nil {
		t.Fatalf("decode add result: %v", err)
	}
	if addResult.Order.ID != 1 || addResult.Order.Customer != "acme" || addResult.Order.Quantity != 2 {
		t.Fatalf("order = %+v, want persisted order", addResult.Order)
	}

	listResp := postRPC(t, handler, map[string]any{
		"jsonrpc": "2.0",
		"id":      "list-1",
		"method":  "orders.list",
		"params":  map[string]any{"limit": 10},
	})
	if listResp.Error != nil {
		t.Fatalf("orders.list error = %+v", listResp.Error)
	}

	var listResult struct {
		Orders []Order `json:"orders"`
	}
	if err := json.Unmarshal(listResp.Result, &listResult); err != nil {
		t.Fatalf("decode list result: %v", err)
	}
	if len(listResult.Orders) != 1 || listResult.Orders[0].SKU != "sku-123" {
		t.Fatalf("orders = %+v, want one stored order", listResult.Orders)
	}
}

func TestOrdersRPCRejectsInvalidOrder(t *testing.T) {
	store := &memoryOrderStore{now: time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)}
	handler := newRPCServer(store, "test-region").routes()

	resp := postRPC(t, handler, map[string]any{
		"jsonrpc": "2.0",
		"id":      "bad-1",
		"method":  "orders.add",
		"params": map[string]any{
			"customer": "acme",
			"sku":      "sku-123",
			"quantity": 0,
		},
	})
	if resp.Error == nil {
		t.Fatal("orders.add unexpectedly succeeded")
	}
	if resp.Error.Code != -32602 {
		t.Fatalf("error code = %d, want invalid params", resp.Error.Code)
	}
	if len(store.orders) != 0 {
		t.Fatalf("stored %d orders after invalid request", len(store.orders))
	}
}

type rpcTestResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

func postRPC(t *testing.T, handler http.Handler, body any) rpcTestResponse {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal rpc request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(payload))
	req.Header.Set("content-type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusInternalServerError {
		t.Fatalf("rpc returned 500: %s", rec.Body.String())
	}

	var decoded rpcTestResponse
	if err := json.NewDecoder(rec.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode rpc response: %v", err)
	}
	return decoded
}

type memoryOrderStore struct {
	now    time.Time
	orders []Order
}

func (s *memoryOrderStore) Ready(context.Context) error {
	return nil
}

func (s *memoryOrderStore) AddOrder(_ context.Context, params addOrderParams) (Order, error) {
	if err := validateAddOrder(params); err != nil {
		return Order{}, err
	}
	order := Order{
		ID:        int64(len(s.orders) + 1),
		Customer:  params.Customer,
		SKU:       params.SKU,
		Quantity:  params.Quantity,
		CreatedAt: s.now,
	}
	s.orders = append(s.orders, order)
	return order, nil
}

func (s *memoryOrderStore) ListOrders(_ context.Context, limit int) ([]Order, error) {
	limit, err := normalizeLimit(limit)
	if err != nil {
		return nil, err
	}
	if limit > len(s.orders) {
		limit = len(s.orders)
	}
	orders := make([]Order, limit)
	copy(orders, s.orders[:limit])
	return orders, nil
}

func (s *memoryOrderStore) CountOrders(context.Context) (int64, error) {
	return int64(len(s.orders)), nil
}
