# Activity Log

## [2026-09-04] Infrastructure Compose and Environment Alignment

- **What was attempted:**
  - Standardized `infra/docker/compose.yml` to use explicit environment variable interpolation for RabbitMQ and MongoDB.
  - Re-added `required: false` to `analytics-service` env_file in compose.yml to fix CI Docker Compose validation.
  - Added MongoDB initial root credential definitions to `infra/.env.example`.
  - Updated `infra/README.md` with production deployment instructions, `.env` linking practices, and security guidelines.
- **The hypothesis being tested:**
  - Explicit environment variable interpolation (`${VAR:?required}`) ensures least-privilege secret isolation, avoids path coupling, and allows CI to validate compose config using `--env-file infra/.env.example` without requiring untracked `.env` files.
- **The observed result/outcome:**
  - Successfully verified with `docker compose --env-file infra/.env.example -f infra/docker/compose.yml -f infra/docker/compose.dev.yml config --quiet` (exit code 0) and production compose validation (exit code 0). `git diff --check` passed cleanly with normalized line endings.