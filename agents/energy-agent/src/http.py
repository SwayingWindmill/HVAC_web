"""Custom HTTP routes mounted by the LangGraph development server."""

from fastapi import FastAPI


app = FastAPI(title="EnergyAgent runtime routes", docs_url=None, redoc_url=None)


@app.get("/health", include_in_schema=False)
async def health() -> dict[str, str]:
    """Report that the Agent HTTP process is ready to receive requests."""

    return {
        "status": "ok",
        "service": "energyagent-agent",
    }
