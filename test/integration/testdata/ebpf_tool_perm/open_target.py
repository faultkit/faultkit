"""
Opens a file via the openat syscall. Under faultkit's
tool-permission-denied scenario openat is rewritten to EACCES.

At probability 1.0 the interpreter's own openat calls (module imports, etc.)
are faulted too, so the process fails with a permission error somewhere in
its open path — the point being that openat is denied for the whole target
PID tree. Either way the run exits non-zero and the report records a fired
openat fault.
"""

import sys

with open(sys.argv[1]) as f:
    sys.stdout.write(f"read {len(f.read())} bytes\n")
