# Incident & Postmortem Context

- **GitHub Issues (`gh`)**: search closed/open issues and linked PRs for incident, regression, outage, hotfix, or reliability keywords (`gh issue list --state all --search "..."`, `gh issue view <number> --comments`)
- **Operations Runbooks & Docs**: inspect `docs/operations/` and `docs/HANDOFF.md` for incident context, recovery drills, and known hardware/site caveats
- **Git History**: commits with messages like "fix for incident", "hotfix", "add defensive check", "revert" followed by "re-apply with..." are strong signals
