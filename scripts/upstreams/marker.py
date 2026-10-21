#!/usr/bin/env python3
"""Marker server for scripts/test-upstreams.sh.

Answers every request with JSON naming the client address it saw and a fixed
marker, so a check can tell a direct request (source is the host) from one that
came through a proxy container (source is the container). /blob returns a
20 MB deterministic body for the large-download check.

Usage: marker.py <port> [certfile keyfile]
"""
import hashlib
import json
import ssl
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

BLOB = (b"shellroute-upstream-test-blob-" * 1000)[:20 * 1024 * 1024 // 1 ]
BLOB = (BLOB * ((20 * 1024 * 1024) // len(BLOB) + 1))[:20 * 1024 * 1024]
BLOB_SHA = hashlib.sha256(BLOB).hexdigest()


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):
        pass

    def do_GET(self):
        if self.path.startswith("/blob"):
            self.send_response(200)
            self.send_header("Content-Type", "application/octet-stream")
            self.send_header("Content-Length", str(len(BLOB)))
            self.end_headers()
            self.wfile.write(BLOB)
            return
        body = json.dumps({
            "source": self.client_address[0],
            "marker": "shellroute-upstream-test",
            "path": self.path,
            "host": self.headers.get("Host", ""),
            "blob_sha256": BLOB_SHA,
        }).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def main():
    port = int(sys.argv[1])
    srv = ThreadingHTTPServer(("0.0.0.0", port), Handler)
    if len(sys.argv) > 3:
        ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        ctx.load_cert_chain(sys.argv[2], sys.argv[3])
        srv.socket = ctx.wrap_socket(srv.socket, server_side=True)
    srv.serve_forever()


if __name__ == "__main__":
    main()
