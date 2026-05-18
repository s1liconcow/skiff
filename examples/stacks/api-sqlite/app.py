import json
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
