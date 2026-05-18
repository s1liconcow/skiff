import asyncio
import json
import os
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from slatedb.uniffi import DbBuilder, ObjectStore

slatedb_uri = os.environ.get("SLATEDB_URI", "memory:///")
slatedb_table = os.environ.get("SLATEDB_TABLE", os.environ.get("SKIFF_STACK", "app"))
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
