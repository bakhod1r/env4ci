# Changelog

## Unreleased

### Added
- `env4ci gen`: `.env.env4ci` (env4ci tokens/addresses, auto-loaded, never synced) plus one `.env.<env>` per environment pre-filled from CI files; only appends missing keys. `init` runs it.
- HashiCorp Vault KV v2 target (`targets.vault`, `path: app/{env}`), check-and-set writes, one version per push.
- `env4ci init` wizard: one or many environments; github / gitlab / vault targets; `--targets`, `--envs`.
- `--all`: diff/push every environment in env4ci.yaml.
- Repo, provider and base URL from `git remote origin`; environment from branch via `branches:`.
- Credential checks before push: SSH login (host key verified), ghcr.io / Docker Hub / v2 registry login; `env4ci verify`.
- GitLab multi-line secrets stored as file variables.
- Colored terminal output.
- `push` / `diff` / `pull` for GitHub Actions (repo and environment) and GitLab CI/CD (environment scope).
- Classification rules (glob, first match wins, unknown = secret).
- `scan`: find variables used in GitHub workflows and `.gitlab-ci.yml` (with `include: local`),
  tagged by environment, stage and branch; `--write` generates `.env` examples; `--by branch`.
- `diff --exit-code` for CI drift checks.
- Retries with backoff on rate limits and 5xx; 30s request timeout.
- Preflight validation: GitHub 48 KB secret limit, GitLab masking rules; push fails before any write.
- GitHub token fallback to `gh auth token`; `GITLAB_URL` / `CI_SERVER_URL` for self-hosted GitLab.
