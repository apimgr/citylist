# Default branch protection

Configure GitHub branch protection for `main` (or the repository's configured default branch) with these requirements:

- Require pull requests before merging and dismiss stale approvals after new commits.
- Require the `CI / Lint`, `CI / Test`, `CI / Build`, `CI / Vulnerability Scan`, `CI / Secret Scan`, and `CI / Workflow SHA Policy` checks.
- Require branches to be up to date before merging.
- Block force pushes and branch deletion.
- Require conversation resolution.
- Do not allow bypassing required checks for routine changes.

GitHub does not apply repository branch protection from checked-in files; these settings must be configured in repository settings.
