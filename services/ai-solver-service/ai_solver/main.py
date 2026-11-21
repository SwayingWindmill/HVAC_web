"""CLI entrypoint for ai-solver-service."""

from __future__ import annotations

import logging
import os
import signal
import sys
import threading

from ai_solver.server import create_server


def main() -> int:
    logging.basicConfig(
        level=logging.INFO,
        format='{"time":"%(asctime)s","level":"%(levelname)s","logger":"%(name)s","msg":"%(message)s"}',
        stream=sys.stdout,
    )
    addr = os.environ.get("AI_SOLVER_HTTP_ADDR", ":19099")
    server = create_server(addr)
    host, port = server.server_address[:2]
    print(f"AI_SOLVER_LISTENING=http://{host}:{port}", flush=True)

    stop_event = threading.Event()

    def _request_shutdown(_signum: int, _frame: object) -> None:
        stop_event.set()
        threading.Thread(target=server.shutdown, daemon=True).start()

    signal.signal(signal.SIGINT, _request_shutdown)
    signal.signal(signal.SIGTERM, _request_shutdown)

    try:
        server.serve_forever(poll_interval=0.2)
    finally:
        server.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
