"""CLI entrypoint for ai-solver-service."""

from __future__ import annotations

import logging
import os
import signal
import sys
import threading

from ai_solver.calibration import load_calibration_profile
from ai_solver.server import create_server
from ai_solver.solver import HVACPhysicsSolver


def main() -> int:
    logging.basicConfig(
        level=logging.INFO,
        format='{"time":"%(asctime)s","level":"%(levelname)s","logger":"%(name)s","msg":"%(message)s"}',
        stream=sys.stdout,
    )
    addr = os.environ.get("AI_SOLVER_HTTP_ADDR", ":19099")
    profile_path = (os.environ.get("AI_SOLVER_PROFILE_PATH") or "").strip()
    calibration = load_calibration_profile(profile_path) if profile_path else None
    server = create_server(addr, solver=HVACPhysicsSolver(calibration=calibration))
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
