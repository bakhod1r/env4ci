# Changelog

## Unreleased

### Added
- `push` / `diff` / `pull` for GitHub Actions (repo and environment) and GitLab CI/CD (environment scope).
- Classification rules (glob, first match wins, unknown = secret).
- `scan`: find variables used in GitHub workflows and `.gitlab-ci.yml` (with `include: local`),
  tagged by environment, stage and branch; `--write` generates `.env` examples; `--by branch`.
- `diff --exit-code` for CI drift checks.
- Retries with backoff on rate limits and 5xx; 30s request timeout.
- Preflight validation: GitHub 48 KB secret limit, GitLab masking rules; push fails before any write.
- GitHub token fallback to `gh auth token`; `GITLAB_URL` / `CI_SERVER_URL` for self-hosted GitLab.
