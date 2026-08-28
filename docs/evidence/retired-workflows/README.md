# Retired CI workflows

This directory keeps historical workflow definitions that are no longer part of active GitHub Actions CI. A retired workflow is evidence only: it must not be referenced by current package scripts, task matrices, workflow-topology checks, or branch-protection requirements.

Current development verification is selected through `.github/workflows/pr-gates.yml` and `scripts/domain-task-matrix.mjs`; broad regression belongs in `.github/workflows/nightly-full-regression.yml`. Operational release/cutover workflows may remain active only when they represent an actual operator action rather than a duplicate development gate.
