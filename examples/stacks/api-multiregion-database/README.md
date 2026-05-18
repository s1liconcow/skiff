# API plus multi-region Postgres

This example is an orders JSON-RPC service backed by the managed Postgres
database declared in `skiff.yaml`. Skiff injects the writer connection string
through the `DATABASE_URL` binding.

Build the image from the repository root:

```bash
docker build -f examples/stacks/api-multiregion-database/Dockerfile \
  -t registry.example.com/orders-rpc:dev .
```

Run it locally against Postgres:

```bash
docker run --rm -d --name orders-postgres \
  -e POSTGRES_PASSWORD=orders \
  -e POSTGRES_USER=orders \
  -e POSTGRES_DB=orders \
  -p 5432:5432 \
  postgres:16
```

```bash
DATABASE_URL='postgres://orders:orders@localhost:5432/orders?sslmode=disable' \
  go run ./examples/stacks/api-multiregion-database
```

Add an order:

```bash
curl -s http://localhost:8080/rpc \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":"add-1","method":"orders.add","params":{"customer":"acme","sku":"sku-123","quantity":2}}'
```

List recent orders:

```bash
curl -s http://localhost:8080/rpc \
  -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":"list-1","method":"orders.list","params":{"limit":20}}'
```

`/healthz` reports process health. `/readyz` verifies the Postgres connection
and creates the `orders` table if needed, so the Skiff health check only routes
traffic after the service can talk to the database writer. `/metrics` reports a
Prometheus-style `orders_rpc_orders_total` count from Postgres.
