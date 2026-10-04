# Env4CI

> Environment configuration for CI/CD.

Sync environment variables and secrets between local `.env` files, **GitHub Actions**, **GitLab CI/CD** and **HashiCorp Vault**.

[![ci](https://github.com/bakhod1r/env4ci/actions/workflows/ci.yml/badge.svg)](https://github.com/bakhod1r/env4ci/actions/workflows/ci.yml)

```bash
go install github.com/bakhod1r/env4ci/cmd/env4ci@latest
```

Or download a binary (Linux, macOS, Windows; amd64/arm64) from [Releases](https://github.com/bakhod1r/env4ci/releases).
Each release has `checksums.txt` and a GitHub build provenance attestation:

```bash
gh attestation verify env4ci_*_linux_amd64.tar.gz --repo bakhod1r/env4ci
```

## Quick start

```bash
env4ci init                                  # asks: targets, environments, repo, Vault path
env4ci init --targets github,vault --envs production,staging   # same, no prompts
# fill .env.production / .env.staging
env4ci diff                                  # current branch -> environment (main -> production)
env4ci push                                  # verifies SSH/registry credentials, asks, writes
env4ci push --all                            # every environment
```

Inside a clone, the repository and provider come from `git remote origin`; the environment from the
current branch through `branches:` (in CI: `GITHUB_REF_NAME`, `CI_COMMIT_BRANCH` ...). A branch with no
mapping is an error, so nothing is ever pushed to the wrong place by accident.

## Usage

```bash
env4ci init                      # write env4ci.yaml
env4ci validate                  # show how each key is classified
env4ci diff github -e production # plan, never prints values
env4ci push github -e production # apply after [y/N]
env4ci push gitlab --prune       # also delete remote-only keys
env4ci pull gitlab -o .env.prod  # remote -> local (0600, auto .gitignore)
env4ci scan -f .env --write      # find vars CI files use, write .env examples
```

## HashiCorp Vault

```yaml
targets:
  vault:
    address: https://vault.example.com:8200   # or VAULT_ADDR
    mount: secret                             # KV v2 mount
    path: myapp/{env}                         # {env} -> production, staging; repository level -> shared
```

Token: `VAULT_TOKEN`, else `~/.vault-token` (`vault login`). `VAULT_NAMESPACE` for Enterprise/HCP.
Every key of an environment lives in one secret; a push is one check-and-set write (one new version),
and fails if someone else changed the secret meanwhile. Vault has no secret/variable split, so
classification is ignored there. `pull` reads every value back.

## Credential checks

Before `push` writes, credentials it is about to store are tried for real:

- **SSH keys** (`SSH_PRIVATE_KEY` + `SSH_HOST` + `SSH_USER` [+ `SSH_PORT`, `SSH_KNOWN_HOSTS`, `*_PASSPHRASE`],
  or `DEPLOY_SSH_KEY` + `DEPLOY_HOST` ...): SSH handshake and public-key login, then disconnect; no command runs.
  The host key must be in `SSH_KNOWN_HOSTS` or `~/.ssh/known_hosts` — a key is never offered to an unverified server.
  A key without host/user is only parsed.
- **Container registries**: `GHCR_TOKEN` (+ `GHCR_USER`) → ghcr.io, `DOCKERHUB_TOKEN` → Docker Hub,
  `REGISTRY_PASSWORD` + `REGISTRY_USER` + `REGISTRY` → any v2 registry.

Any failure stops the push before anything is written. `env4ci verify` runs the checks alone;
`--no-verify` skips them. Other names go under `checks:` in env4ci.yaml.

On GitLab, multi-line secrets (SSH keys, certificates) are stored as **file** variables because GitLab cannot mask them.

## Scan CI files

`env4ci scan` reads `.github/workflows/*.yml` (`${{ secrets.X }}`, `${{ vars.X }}`) and `.gitlab-ci.yml`
(`$X`, `${X}`) plus every `include: local` file it pulls in (globs supported; remote/template includes are not fetched).
It skips `GITHUB_TOKEN`, `CI_*`, `GITLAB_*`, `$$` escapes and variables defined in the pipeline itself.
Keys are grouped by the job's `environment:` and tagged with where they are used:
the GitLab `stage:` (default `test`) or the GitHub job id; `all` means workflow-level `env:` or GitLab `default:`.

```env
# secret · stages: deploy · branches: main, develop · .github/workflows/deploy.yml, .gitlab/ci/deploy.yml
DATABASE_URL=
```

### Group by branch

`env4ci scan --by branch --write` writes one example per branch: `.env.example` (all branches),
`.env.main.example`, `.env.develop.example`, `.env.tags.example`, ...

Branches come from GitHub `on.push/pull_request.branches` and job `if: github.ref == 'refs/heads/x'`,
and from GitLab `only:` and `rules: - if: $CI_COMMIT_BRANCH == "x"` (`=~ /regex/` and `$CI_DEFAULT_BRANCH` → `(default)`).
Tag-only jobs go to `(tags)`. Anything without a branch filter runs on all branches.

- `--write` creates `.env.example` (shared) and `.env.<env>.example` per environment; existing files are skipped.
- `-f .env` lists keys CI needs that your file lacks.

If your `.gitignore` has `.env.*`, add `!.env.*.example`.

```text
github:bakhod1r/my-api@production

  + DATABASE_URL                     secret   new
  ? JWT_SECRET                       secret   unverifiable
  ~ LOG_LEVEL                        variable changed
    APP_PORT                         variable unchanged
  - OLD_API_URL                      variable remote only
```

`?` = GitHub secrets are write-only, so env4ci cannot compare them and re-writes them on push.

## CI drift check

```yaml
- run: env4ci diff github -e production --exit-code   # exit 2 if remote differs from .env.production
  env:
    GITHUB_TOKEN: ${{ secrets.ENV4CI_TOKEN }}
```

Exit codes: `0` ok, `1` error, `2` drift (`--exit-code`).

## Authentication

| Provider | Token | Scope |
|----------|-------|-------|
| GitHub | `GITHUB_TOKEN` / `GH_TOKEN`, else `gh auth token` | fine-grained: *Secrets* + *Variables* read/write (+ *Environments* read) |
| GitLab | `GITLAB_TOKEN` | `api`, Maintainer role |

Self-hosted: `targets.github.base_url` (GHES `https://host/api/v3`), `targets.gitlab.base_url` or `GITLAB_URL`.

## Reliability

- 30 s request timeout; retries with backoff on network errors, 429, 502–504 and GitHub secondary rate limits (`Retry-After` honoured).
- Values the provider would refuse (GitHub > 48 KB, GitLab masked < 8 chars or multi-line) fail **before** any write.
- `Ctrl-C` / `SIGTERM` cancel in-flight requests.

## Classification

Each key becomes a **secret** (GitHub secret / GitLab masked variable) or a **variable**.
Rules in `env4ci.yaml` are globs, first match wins. Unmatched keys default to **secret** — leaking is worse than hiding.

```yaml
source: .env.production
default: secret
rules:
  - { pattern: "*SECRET*", type: secret }
  - { pattern: "APP_*",    type: variable }
targets:
  github: { repo: owner/name, environment: production }
  gitlab: { project: group/project, environment: production, protected: true }
```

## Security

- Tokens only from `GITHUB_TOKEN` / `GH_TOKEN` / `GITLAB_TOKEN`; config file rejects unknown fields.
- Output never contains values.
- GitHub secrets encrypted client-side (libsodium sealed box) with the repo/environment public key.
- `push` deletes nothing unless `--prune`; always asks unless `--yes`.
- `pull` refuses to overwrite files.

## Architecture

```text
cmd/env4ci                     CLI (interfaces)
internal/domain                Variable, Classifier, Plan — pure rules
internal/application           use cases + Provider port
internal/infrastructure/
  dotenv  config  provider/{github,gitlab}
```

New CI platforms implement `application.Provider` (`List`, `Set`, `Delete`).

## Roadmap

- [ ] OS keychain auth (`env4ci auth login`)
- [ ] GitLab group variables, GitHub org secrets
- [ ] Bitbucket, CircleCI providers
- [ ] goreleaser binaries

## License

MIT
