from cube import config


@config("context_to_groups")
def context_to_groups(ctx: dict) -> list[str]:
    """Map the short-lived Query Service JWT security context to Cube groups."""
    security_context = ctx.get("securityContext", {})
    return security_context.get("groups", [])
