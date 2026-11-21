FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PYTHONPATH=/app \
    AI_SOLVER_HTTP_ADDR=:19099

WORKDIR /app

COPY services/ai-solver-service/ai_solver ./ai_solver
COPY services/ai-solver-service/pyproject.toml ./pyproject.toml

RUN adduser --disabled-password --gecos "" --uid 10001 solver \
    && chown -R solver:solver /app

USER solver

EXPOSE 19099

HEALTHCHECK --interval=10s --timeout=4s --retries=6 --start-period=10s \
  CMD python3 -c "import urllib.request; urllib.request.urlopen('http://127.0.0.1:19099/healthz', timeout=2).read()"

CMD ["python3", "-m", "ai_solver.main"]
