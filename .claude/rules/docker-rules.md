# Docker Rules (PART 26)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO

- Never put Dockerfiles or Compose files in the repository root.
- Never use production or development Compose files for AI-driven automated tests.
- Never store runtime mounts, volumes, credentials, or test state in the project tree.
- Never publish an image from local development commands.

## CRITICAL - ALWAYS DO

- Keep container definitions under `docker/` and follow the required base image/runtime contract.
- Use `docker/docker-compose.test.yml` only through the permitted test workflow.
- Mount test runtime data from project-scoped OS temp directories.
- Keep image build and deployment docs aligned with actual files and tags.

## Key rules

- PART 26 defines image build, compose, runtime, and container security requirements.
- CI/CD performs publishing; local `make docker` builds without pushing.

For complete details, see AI.md PART 26.
