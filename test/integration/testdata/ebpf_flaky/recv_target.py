"""
A minimal libc-based TCP client: connect, send, recv. The recv() lands on
the recvfrom syscall, which faultkit's flaky-network eBPF program rewrites
to ECONNRESET — so under `faultkit run` this raises ConnectionResetError
and the process exits non-zero. (Go would use read() and bypass the
kprobes, so the target is deliberately Python/libc.)

Usage: recv_target.py HOST:PORT  (HOST is a numeric IP so no DNS recv runs
before the one we want to fault).
"""

import socket
import sys

addr = sys.argv[1]
host, port = addr.rsplit(":", 1)

with socket.create_connection((host, int(port)), timeout=5) as s:
    s.sendall(b"GET / HTTP/1.0\r\n\r\n")
    data = s.recv(4096)
    sys.stdout.write(f"received {len(data)} bytes\n")
