"""
A backend client that opens a TCP connection, sends an HTTP request,
and reads the response. The bug: no retry on transient errors. An
ECONNRESET on the recv path propagates as a hard failure.
"""

import socket


def fetch(host: str, port: int, path: str = "/") -> str:
    with socket.create_connection((host, port), timeout=5) as s:
        s.sendall(
            (
                f"GET {path} HTTP/1.1\r\n"
                f"Host: {host}\r\n"
                "Connection: close\r\n\r\n"
            ).encode()
        )
        chunks = []
        while True:
            chunk = s.recv(4096)
            if not chunk:
                break
            chunks.append(chunk)
    body = b"".join(chunks)
    return body.split(b"\r\n", 1)[0].decode()
