"""HTTP JSON server for the external Python HVAC optimization solver."""

from __future__ import annotations

from http import HTTPStatus
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import logging
import re
from typing import Any

from ai_solver.model import SolverInfeasibleError, SolverInputError, parse_solver_request
from ai_solver.solver import HVACPhysicsSolver, SOLVER_VERSION

MAX_REQUEST_BODY_BYTES = 256 * 1024
TRACEPARENT_RE = re.compile(r"^00-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$")

logger = logging.getLogger("ai-solver-service")


def extract_trace_id(header_value: str | None) -> str:
    raw = (header_value or "").strip().lower()
    if TRACEPARENT_RE.match(raw):
        return raw.split("-")[1]
    return ""


class SolverHTTPServer(ThreadingHTTPServer):
    daemon_threads = True
    allow_reuse_address = True

    def __init__(self, server_address: tuple[str, int], solver: HVACPhysicsSolver | None = None) -> None:
        self.solver = solver or HVACPhysicsSolver()
        super().__init__(server_address, SolverRequestHandler)


class SolverRequestHandler(BaseHTTPRequestHandler):
    server: SolverHTTPServer

    def do_GET(self) -> None:  # noqa: N802
        path = self.path.split("?", 1)[0]
        if path == "/healthz":
            self._write_json(HTTPStatus.OK, {"status": "ok", "service": "ai-solver-service"})
            return
        if path == "/readyz":
            self._write_json(
                HTTPStatus.OK,
                {
                    "status": "ready",
                    "service": "ai-solver-service",
                    "solverVersion": SOLVER_VERSION,
                },
            )
            return
        self._write_problem(HTTPStatus.NOT_FOUND, "NOT_FOUND", "Unknown solver endpoint")

    def do_POST(self) -> None:  # noqa: N802
        path = self.path.split("?", 1)[0]
        if path != "/v1/solve":
            self._write_problem(HTTPStatus.NOT_FOUND, "NOT_FOUND", "Unknown solver endpoint")
            return

        traceparent = (self.headers.get("traceparent") or "").strip()
        trace_id = extract_trace_id(traceparent)

        raw_length = self.headers.get("Content-Length")
        if raw_length is None:
            self._write_problem(HTTPStatus.LENGTH_REQUIRED, "CONTENT_LENGTH_REQUIRED", "Content-Length header is required")
            return
        try:
            content_length = int(raw_length)
        except ValueError:
            self._write_problem(HTTPStatus.BAD_REQUEST, "INVALID_CONTENT_LENGTH", "Content-Length must be an integer")
            return
        if content_length <= 0:
            self._write_problem(HTTPStatus.BAD_REQUEST, "EMPTY_BODY", "Request body cannot be empty")
            return
        if content_length > MAX_REQUEST_BODY_BYTES:
            if content_length <= MAX_REQUEST_BODY_BYTES * 2:
                _ = self.rfile.read(content_length)
            self.close_connection = True
            self._write_problem(
                HTTPStatus.REQUEST_ENTITY_TOO_LARGE,
                "PAYLOAD_TOO_LARGE",
                f"Request body exceeds {MAX_REQUEST_BODY_BYTES} bytes",
            )
            return

        raw_bytes = self.rfile.read(content_length)
        if len(raw_bytes) != content_length:
            self._write_problem(HTTPStatus.BAD_REQUEST, "TRUNCATED_BODY", "Request body truncated before Content-Length")
            return

        try:
            payload = json.loads(raw_bytes.decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError):
            self._write_problem(HTTPStatus.BAD_REQUEST, "INVALID_JSON", "Request body must be valid UTF-8 JSON")
            return

        try:
            req = parse_solver_request(payload)
            resp = self.server.solver.solve(req)
        except SolverInputError as exc:
            self._write_problem(HTTPStatus.BAD_REQUEST, "SOLVER_INPUT_INVALID", str(exc))
            return
        except SolverInfeasibleError as exc:
            self._write_problem(HTTPStatus.UNPROCESSABLE_ENTITY, "SOLVER_INFEASIBLE", str(exc))
            return

        extra_headers: dict[str, str] = {"X-Solver-Version": SOLVER_VERSION}
        if trace_id:
            extra_headers["X-Trace-Id"] = trace_id
        logger.info(
            "solve_completed site_id=%s run_id=%s trace_id=%s supply_temp_c=%.2f risk=%s",
            req.site_id,
            req.optimization_run_id,
            trace_id or "-",
            resp.supply_temp_c,
            resp.risk_level,
        )
        self._write_json(HTTPStatus.OK, resp.to_dict(), extra_headers=extra_headers)

    def log_message(self, format: str, *args: Any) -> None:  # noqa: A002
        # Suppress BaseHTTPRequestHandler's default unformatted stderr logs.
        return

    def _write_problem(self, status: HTTPStatus, code: str, detail: str) -> None:
        self._write_json(
            status,
            {
                "code": code,
                "status": int(status),
                "detail": detail,
            },
        )

    def _write_json(
        self,
        status: HTTPStatus,
        payload: dict[str, Any],
        extra_headers: dict[str, str] | None = None,
    ) -> None:
        body = json.dumps(payload, separators=(",", ":"), allow_nan=False).encode("utf-8")
        self.send_response(int(status))
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        if extra_headers:
            for key, value in extra_headers.items():
                self.send_header(key, value)
        self.end_headers()
        self.wfile.write(body)


def parse_listen_addr(raw: str) -> tuple[str, int]:
    addr = (raw or "").strip() or ":19099"
    if ":" not in addr:
        raise ValueError(f"invalid AI_SOLVER_HTTP_ADDR {addr!r}: missing host:port")
    host, port_raw = addr.rsplit(":", 1)
    host = host.strip() or "0.0.0.0"
    port = int(port_raw)
    if port < 0 or port > 65535:
        raise ValueError(f"invalid port {port} in AI_SOLVER_HTTP_ADDR")
    return host, port


def create_server(listen_addr: str = ":19099", solver: HVACPhysicsSolver | None = None) -> SolverHTTPServer:
    host, port = parse_listen_addr(listen_addr)
    return SolverHTTPServer((host, port), solver=solver)
