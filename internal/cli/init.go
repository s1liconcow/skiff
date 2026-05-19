package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type initStackOutput struct {
	OK        bool     `json:"ok"`
	TraceID   string   `json:"trace_id,omitempty"`
	Recipe    string   `json:"recipe"`
	Name      string   `json:"name"`
	Directory string   `json:"directory"`
	Files     []string `json:"files"`
}

func runInit(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printInitUsage(stdout, binary)
		return ExitSuccess
	}
	if args[0] != "stack" {
		return writeClientCommandError(binary, "init", root.Format, root.TraceID, fmt.Errorf("unsupported init target %q; expected stack", args[0]), stdout, stderr)
	}
	return runInitStack(binary, args[1:], root, stdout, stderr)
}

func runInitStack(binary string, args []string, root rootOptions, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printInitUsage(stdout, binary)
		return ExitSuccess
	}
	recipe := args[0]

	fs := flag.NewFlagSet(binary+" init stack "+recipe, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	format := fs.String("format", root.Format, "output format: human, json, or json-pretty")
	noColor := fs.Bool("no-color", root.NoColor, "disable ANSI color output")
	traceID := fs.String("trace-id", root.TraceID, "trace identifier to include in machine-readable output")
	yes := fs.Bool("yes", root.Yes, "overwrite existing generated files")
	dir := fs.String("dir", "", "directory to create")
	env := fs.String("env", firstNonEmptyCLI(root.Env, defaultSkiffEnvFromEnv(), "prod"), "Skiff environment name")
	artifact := fs.String("artifact", "", "OCI artifact reference for the API service")
	objectStoreURI := fs.String("object-store-uri", "", "object store URI for api-slatedb")
	overwrite := fs.Bool("overwrite", false, "overwrite existing generated files")

	flagArgs, positionals, err := splitInitStackArgs(args[1:])
	if err != nil {
		return writeClientCommandError(binary, "init", *format, *traceID, err, stdout, stderr)
	}
	if handled, err := parseCommandFlags(fs, flagArgs, stdout); handled {
		return ExitSuccess
	} else if err != nil {
		return writeClientCommandError(binary, "init", *format, *traceID, err, stdout, stderr)
	}
	if len(positionals) != 1 {
		return writeClientCommandError(binary, "init", *format, *traceID, errors.New("stack name is required"), stdout, stderr)
	}
	_ = noColor

	name := strings.TrimSpace(positionals[0])
	if name == "" {
		return writeClientCommandError(binary, "init", *format, *traceID, errors.New("stack name is required"), stdout, stderr)
	}
	targetDir := *dir
	if strings.TrimSpace(targetDir) == "" {
		targetDir = name
	}
	if *artifact == "" {
		*artifact = defaultStackArtifact(recipe, name)
	}
	if recipe == "api-slatedb" && *objectStoreURI == "" {
		*objectStoreURI = fmt.Sprintf("s3://%s-slatedb-%s/slatedb/%s", name, *env, name)
	}
	var files map[string]string
	switch recipe {
	case "api-database":
		files = map[string]string{
			filepath.Join(targetDir, "skiff.yaml"):                                apiDatabaseSpecTemplate(name, *env, *artifact),
			filepath.Join(targetDir, "main.go"):                                   apiDatabaseMainTemplate(name),
			filepath.Join(targetDir, ".github", "workflows", "skiff-release.yml"): apiDatabaseCITemplate(name),
		}
	case "api-slatedb":
		files = map[string]string{
			filepath.Join(targetDir, "skiff.yaml"):                                apiSlateDBSpecTemplate(name, *env, *artifact, *objectStoreURI),
			filepath.Join(targetDir, "app.py"):                                    apiSlateDBAppTemplate(name),
			filepath.Join(targetDir, "Dockerfile"):                                apiSlateDBDockerfileTemplate(name),
			filepath.Join(targetDir, ".github", "workflows", "skiff-release.yml"): apiDatabaseCITemplate(name),
		}
	case "api-sqlite":
		files = map[string]string{
			filepath.Join(targetDir, "skiff.yaml"):                                apiSQLiteSpecTemplate(name, *env, *artifact),
			filepath.Join(targetDir, "app.py"):                                    apiSQLiteAppTemplate(),
			filepath.Join(targetDir, "Dockerfile"):                                apiSQLiteDockerfileTemplate(),
			filepath.Join(targetDir, ".github", "workflows", "skiff-release.yml"): apiSQLiteCITemplate(name),
		}
	default:
		return writeClientCommandError(binary, "init", *format, *traceID, fmt.Errorf("unsupported stack recipe %q; expected api-database, api-slatedb, or api-sqlite", recipe), stdout, stderr)
	}
	written := make([]string, 0, len(files))
	for path, body := range files {
		if err := writeInitFile(path, body, *yes || *overwrite); err != nil {
			return writeClientCommandError(binary, "init", *format, *traceID, err, stdout, stderr)
		}
		written = append(written, path)
	}
	sortStrings(written)

	switch *format {
	case "human", "text":
		fmt.Fprintf(stdout, "created %s stack %s in %s\n", recipe, name, targetDir)
		for _, file := range written {
			fmt.Fprintf(stdout, "- %s\n", file)
		}
		return ExitSuccess
	case "json":
		if err := json.NewEncoder(stdout).Encode(initStackOutput{OK: true, TraceID: *traceID, Recipe: recipe, Name: name, Directory: targetDir, Files: written}); err != nil {
			fmt.Fprintf(stderr, "%s init: %v\n", binary, err)
			return ExitInternalError
		}
		return ExitSuccess
	default:
		return writeClientCommandError(binary, "init", *format, *traceID, errors.New(`unsupported format; expected "human", "json", or "json-pretty"`), stdout, stderr)
	}
}

func printInitUsage(w io.Writer, binary string) {
	fmt.Fprintf(w, "Usage: %s init stack <recipe> <name> [flags]\n\n", binary)
	fmt.Fprintln(w, "Recipes:")
	fmt.Fprintln(w, "  api-database  API service plus managed database template, example app, and CI workflow")
	fmt.Fprintln(w, "  api-slatedb    API service plus object-store-backed SlateDB template, example app, and CI workflow")
	fmt.Fprintln(w, "  api-sqlite    Single-member API server with local SQLite on a durable StatefulGroup volume")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --dir <path> --env <env> --artifact <oci-ref> --object-store-uri <uri> --overwrite --format human|json|json-pretty --no-color --yes --trace-id <id>")
}

func splitInitStackArgs(args []string) ([]string, []string, error) {
	valueFlags := map[string]bool{
		"artifact":         true,
		"dir":              true,
		"env":              true,
		"format":           true,
		"object-store-uri": true,
		"trace-id":         true,
	}
	return splitArgs(args, valueFlags)
}

func writeInitFile(path, body string, overwrite bool) error {
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists; pass --overwrite or --yes", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}

func apiDatabaseSpecTemplate(name, env, artifact string) string {
	return fmt.Sprintf(`apiVersion: skiff.dev/v1alpha1
kind: Stack
metadata:
  name: %s
  env: %s
stack:
  services:
    - name: api
      artifact:
        type: oci
        ref: %s
      runtime:
        port: 8080
        env:
          SKIFF_STACK: %s
        health:
          path: /healthz
      scale:
        min: 2
        max: 4
      network:
        ingress:
          type: internal-http
  databases:
    - name: db
      engine: postgres
      version: "16"
      size: small
      storage:
        sizeGB: 20
        type: gp3
        encrypted: true
      backups:
        enabled: true
        retentionDays: 7
      network:
        private: true
  bindings:
    - from: api
      to: db
      as: DATABASE_URL
`, name, env, artifact, name)
}

func apiDatabaseMainTemplate(name string) string {
	return fmt.Sprintf(`package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("DATABASE_URL") == "" {
			http.Error(w, "database binding missing", http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprintf(w, "%s api is bound to a managed database\n")
	})
	_ = http.ListenAndServe(":8080", nil)
}
`, name)
}

func apiSlateDBSpecTemplate(name, env, artifact, objectStoreURI string) string {
	return fmt.Sprintf(`apiVersion: skiff.dev/v1alpha1
kind: Stack
metadata:
  name: %s
  env: %s
stack:
  services:
    - name: api
      artifact:
        type: oci
        ref: %s
      runtime:
        command:
          - python
          - /app/app.py
        port: 8080
        env:
          SKIFF_STACK: %s
          SLATEDB_TABLE: %s
          SLATEDB_CACHE_DIR: /var/cache/slatedb
        health:
          path: /healthz
        metrics:
          path: /metrics
      scale:
        min: 2
        max: 4
      network:
        ingress:
          type: internal-http
  objectStores:
    - name: data
      uri: %s
      purpose: slatedb
      access: read-write
      versioned: true
      encrypted: true
  bindings:
    - from: api
      to: data
      as: SLATEDB_URI
`, name, env, artifact, name, name, objectStoreURI)
}

func apiSlateDBAppTemplate(name string) string {
	return fmt.Sprintf(`import asyncio
import json
import os
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from slatedb.uniffi import DbBuilder, ObjectStore

slatedb_uri = os.environ.get("SLATEDB_URI", "memory:///")
slatedb_table = os.environ.get("SLATEDB_TABLE", os.environ.get("SKIFF_STACK", "%s"))
db_lock = threading.Lock()


async def with_db(action):
    store = ObjectStore.resolve(slatedb_uri)
    db = await DbBuilder(slatedb_table, store).build()
    try:
        return await action(db)
    finally:
        await db.shutdown()


async def health_check():
    async def run(db):
        await db.put(b"healthz", b"ok")
        return await db.get(b"healthz")

    return await with_db(run)


async def record_request(path):
    async def run(db):
        counter_key = b"requests:count"
        current = await db.get(counter_key)
        count = int(current.decode("utf-8")) if current else 0
        count += 1
        request_key = f"requests:{time.time_ns()}".encode("utf-8")
        await db.put(request_key, path.encode("utf-8"))
        await db.put(counter_key, str(count).encode("utf-8"))
        stored = await db.get(request_key)
        return count, stored.decode("utf-8") if stored else ""

    return await with_db(run)


async def request_count():
    async def run(db):
        current = await db.get(b"requests:count")
        return int(current.decode("utf-8")) if current else 0

    return await with_db(run)


def run_locked(coro):
    with db_lock:
        return asyncio.run(coro)


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        try:
            if self.path == "/healthz":
                value = run_locked(health_check())
                if value != b"ok":
                    self.respond(503, "text/plain", b"slatedb health check failed\n")
                    return
                self.respond(200, "text/plain", b"ok\n")
                return
            if self.path == "/metrics":
                count = run_locked(request_count())
                body = f"api_slatedb_requests_total {count}\n".encode("utf-8")
                self.respond(200, "text/plain", body)
                return
            count, stored_path = run_locked(record_request(self.path))
            body = json.dumps(
                {
                    "ok": True,
                    "database": slatedb_table,
                    "object_store": slatedb_uri,
                    "requests": count,
                    "stored_path": stored_path,
                },
                indent=2,
            ).encode("utf-8")
            self.respond(200, "application/json", body + b"\n")
        except Exception as exc:
            self.respond(500, "text/plain", f"slatedb error: {exc}\n".encode("utf-8"))

    def log_message(self, format, *args):
        return

    def respond(self, status, content_type, body):
        self.send_response(status)
        self.send_header("content-type", content_type)
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


if __name__ == "__main__":
    port = int(os.environ.get("PORT", "8080"))
    ThreadingHTTPServer(("0.0.0.0", port), Handler).serve_forever()
`, name)
}

func apiSlateDBDockerfileTemplate(name string) string {
	return fmt.Sprintf(`FROM python:3.13-slim

RUN pip install --no-cache-dir slatedb

WORKDIR /app
COPY app.py /app/app.py

ENV PORT=8080
ENV SLATEDB_URI=memory:///
ENV SLATEDB_TABLE=%s
EXPOSE 8080

CMD ["python", "/app/app.py"]
`, name)
}

func apiDatabaseCITemplate(name string) string {
	return fmt.Sprintf(`name: skiff-release

on:
  push:
    branches: [ main ]

jobs:
  candidate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Build and publish immutable artifact
        run: echo "publish registry.example.com/%s-api@sha256:${GITHUB_SHA}"
      - name: Record Skiff release candidate
        run: |
          skiff release candidate create %s-api \
            --env staging \
            --candidate cand-${GITHUB_RUN_ID} \
            --artifact registry.example.com/%s-api@sha256:${GITHUB_SHA} \
            --git-repo ${GITHUB_REPOSITORY} \
            --git-sha ${GITHUB_SHA} \
            --ci-provider github-actions \
            --ci-run-id ${GITHUB_RUN_ID} \
            --check tests=passed \
            --format json
`, name, name, name)
}

func defaultStackArtifact(recipe, name string) string {
	switch recipe {
	case "api-sqlite":
		return fmt.Sprintf("registry.example.com/%s@sha256:REPLACE_WITH_DIGEST", apiSQLiteServiceName(name))
	default:
		return fmt.Sprintf("registry.example.com/%s-api@sha256:REPLACE_WITH_DIGEST", name)
	}
}

func apiSQLiteSpecTemplate(name, env, artifact string) string {
	serviceName := apiSQLiteServiceName(name)
	return fmt.Sprintf(`apiVersion: skiff.dev/v1alpha1
kind: StatefulGroup
metadata:
  name: %s
  env: %s
  labels:
    app: %s
    tier: api-sqlite
stateful:
  replicas: 1
  members:
    - ordinal: 0
  volume:
    size: 10Gi
    type: gp3
    mountPath: /var/lib/skiff/sqlite
    encrypted: true
  recipe:
    name: sqlite-api-single
    config:
      artifact:
        type: oci
        ref: %s
      runtime:
        command:
          - python
          - /app/app.py
        env:
          SKIFF_STACK: %s
          SQLITE_PATH: /var/lib/skiff/sqlite/app.db
        ports:
          http: 8080
        health:
          path: /healthz
          port: 8080
        metrics:
          enabled: true
          path: /metrics
          port: 8080
      sqlite:
        path: /var/lib/skiff/sqlite/app.db
        journaling: wal
      snapshots:
        enabled: true
        interval: 30m
        retention: 7d
      recovery:
        requireFencing: true
        replaceMemberSaga: stateful.replace_member
        maxUnavailableMembers: 1
  update:
    strategy: ordered
`, serviceName, env, name, artifact, name)
}

func apiSQLiteAppTemplate() string {
	return `import json
import os
import sqlite3
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

db_path = Path(os.environ.get("SQLITE_PATH", "/var/lib/skiff/sqlite/app.db"))
db_path.parent.mkdir(parents=True, exist_ok=True)


def connect():
    conn = sqlite3.connect(db_path)
    conn.execute("PRAGMA journal_mode=WAL")
    conn.execute(
        "CREATE TABLE IF NOT EXISTS requests (id INTEGER PRIMARY KEY AUTOINCREMENT, path TEXT NOT NULL, created_at INTEGER NOT NULL)"
    )
    return conn


def request_count(conn):
    return conn.execute("SELECT COUNT(*) FROM requests").fetchone()[0]


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        with connect() as conn:
            if self.path == "/healthz":
                conn.execute("SELECT 1")
                self.respond(200, "text/plain", b"ok\n")
                return
            if self.path == "/metrics":
                body = f"api_sqlite_requests_total {request_count(conn)}\n".encode()
                self.respond(200, "text/plain", body)
                return
            conn.execute(
                "INSERT INTO requests (path, created_at) VALUES (?, ?)",
                (self.path, int(time.time())),
            )
            conn.commit()
            body = json.dumps(
                {
                    "ok": True,
                    "database": str(db_path),
                    "requests": request_count(conn),
                },
                indent=2,
            ).encode()
            self.respond(200, "application/json", body + b"\n")

    def log_message(self, format, *args):
        return

    def respond(self, status, content_type, body):
        self.send_response(status)
        self.send_header("content-type", content_type)
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


if __name__ == "__main__":
    port = int(os.environ.get("PORT", "8080"))
    ThreadingHTTPServer(("0.0.0.0", port), Handler).serve_forever()
`
}

func apiSQLiteDockerfileTemplate() string {
	return `FROM python:3.13-slim

WORKDIR /app
COPY app.py /app/app.py

ENV PORT=8080
ENV SQLITE_PATH=/var/lib/skiff/sqlite/app.db
EXPOSE 8080

CMD ["python", "/app/app.py"]
`
}

func apiSQLiteCITemplate(name string) string {
	serviceName := apiSQLiteServiceName(name)
	return fmt.Sprintf(`name: skiff-release

on:
  push:
    branches: [ main ]

jobs:
  candidate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Build and publish immutable artifact
        run: echo "publish registry.example.com/%s@sha256:${GITHUB_SHA}"
      - name: Record Skiff release candidate
        run: |
          skiff release candidate create %s \
            --env staging \
            --candidate cand-${GITHUB_RUN_ID} \
            --artifact registry.example.com/%s@sha256:${GITHUB_SHA} \
            --git-repo ${GITHUB_REPOSITORY} \
            --git-sha ${GITHUB_SHA} \
            --ci-provider github-actions \
            --ci-run-id ${GITHUB_RUN_ID} \
            --check tests=passed \
            --format json
`, serviceName, serviceName, serviceName)
}

func apiSQLiteServiceName(name string) string {
	name = strings.TrimSpace(name)
	if strings.HasSuffix(name, "-api") {
		return name
	}
	return name + "-api"
}

func sortStrings(values []string) {
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
}
