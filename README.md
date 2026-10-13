# Env4CI

> Environment configuration for CI/CD.

Sync environment variables and secrets between local `.env` files, **GitHub Actions**, **GitLab CI/CD** and **HashiCorp Vault**.

[![ci](https://github.com/bakhod1r/env4ci/actions/workflows/ci.yml/badge.svg)](https://github.com/bakhod1r/env4ci/actions/workflows/ci.yml)

## Install

No Go needed — every release ships static binaries.

```bash
# macOS / Linux (detects OS and arch, verifies checksums.txt)
curl -sSfL https://raw.githubusercontent.com/bakhod1r/env4ci/main/install.sh | sh
#   ENV4CI_VERSION=v0.1.0 pins a release; BINDIR=~/bin picks the directory

brew install bakhod1r/tap/env4ci                           # Homebrew
scoop bucket add bakhod1r https://github.com/bakhod1r/scoop-bucket
scoop install env4ci                                       # Windows / Scoop

go install github.com/bakhod1r/env4ci/cmd/env4ci@latest    # with Go
```

Update an installed binary:

```bash
env4ci update           # download the latest release, verify checksums.txt, replace this binary
env4ci update --check   # only say whether a newer release exists
```

A Homebrew or Scoop install is left alone: `update` prints the `brew upgrade` / `scoop update` command instead.

Help: `env4ci help`, `env4ci <command> --help`, and `man env4ci` (installed by `install.sh`; elsewhere `env4ci man > env4ci.1`).

Tab completion of commands and each command's flags (`install.sh` installs the scripts; if your shell does not pick them up, load one yourself):

```bash
echo 'source <(env4ci completion zsh)' >> ~/.zshrc     # zsh, after compinit
echo 'source <(env4ci completion bash)' >> ~/.bashrc   # bash
env4ci completion fish > ~/.config/fish/completions/env4ci.fish
```

GitHub Actions:

```yaml
- uses: bakhod1r/env4ci@v0        # with: { version: v0.1.0 } to pin
- run: env4ci check github --all  # exit 2 if CI uses secrets the repo lacks
  env:
    GITHUB_TOKEN: ${{ secrets.ENV4CI_TOKEN }}
```

Or download a binary (Linux, macOS, Windows; amd64/arm64) from [Releases](https://github.com/bakhod1r/env4ci/releases).
Each release has `checksums.txt` and a GitHub build provenance attestation:

```bash
gh attestation verify env4ci_*_linux_amd64.tar.gz --repo bakhod1r/env4ci
```

## Quick start

```bash
env4ci init                                  # asks: targets, repo, Vault path; environments come from the CI files, else the branch
env4ci init --targets github,vault --envs production,staging   # same, no prompts
# init writes env4ci.yaml (commit it), then runs "env4ci gen" in your CI folder:
#   .github/env4ci.env       env4ci's own settings: GITHUB_TOKEN / GITLAB_TOKEN / VAULT_ADDR / VAULT_TOKEN
#   .github/production.env   CI/CD variables, keys pre-filled from your workflow / .gitlab-ci.yml files
#   .github/staging.env      ...
# (.gitlab/ for GitLab projects; .env.env4ci / .env.<env> at the root for Vault-only.)
# Each file is added to .gitignore (mode 0600) before it is written; .github/ itself stays tracked.
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

## Generated files

`env4ci gen` reads env4ci.yaml and creates, or completes, two kinds of file:

| File | Holds | Used by |
|------|-------|---------|
| `.github/env4ci.env` (`auth_file:`) | tokens and addresses for the configured targets | env4ci itself; loaded at start, real env vars win, never synced |
| `.github/<env>.env` (`environments:`) | variables your pipelines read, found by `scan` | `diff` / `push` |

Before writing, every generated file is added to the root `.gitignore` by exact path
(`/.github/production.env`), never by folder, so workflows next to them stay tracked. Existing values are never touched; keys a file lacks are appended under `# --- added by env4ci gen ---`.
Run it again after changing CI files. The auth file accepts only env4ci settings, and `push` refuses to use it as a source.

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

Before `push` writes, registry credentials it is about to store are tried for real:
`GHCR_TOKEN` (+ `GHCR_USER`) → ghcr.io, `DOCKERHUB_TOKEN` → Docker Hub,
`REGISTRY_PASSWORD` + `REGISTRY_USER` + `REGISTRY` → any v2 registry.
Any failure stops the push before anything is written; `--no-verify` skips the checks.

SSH keys are not checked by `push` or `rotate`. `env4ci verify` tries them on request
(`SSH_PRIVATE_KEY` + `SSH_HOST` + `SSH_USER` [+ `SSH_PORT`, `*_PASSPHRASE`], or `DEPLOY_SSH_KEY` + `DEPLOY_HOST` ...):
SSH handshake and public-key login, then disconnect; no command runs and the server's host key is not checked.
A key without host/user is only parsed. Other names go under `checks:` in env4ci.yaml.

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

## Missing secrets check

`env4ci check` scans the CI files and lists keys they use that the provider does not hold — no local `.env` needed. An environment target also counts repository-level (shared) keys.

```console
$ env4ci check github --all
✗ missing in github:
  SONAR_TOKEN                  secret   environment: production  (.github/workflows/ci.yml)
```

Exit `2` when something is missing (`make env-check`).

## Rotate a secret

```bash
env4ci rotate API_TOKEN -e production              # type the new value (hidden), review, confirm
pbpaste | env4ci rotate API_TOKEN -e production -y # piped value needs -y
env4ci rotate JWT_SECRET --generate                # random 32-byte secret (base64url)
env4ci rotate DEPLOY_SSH_KEY --generate            # new ed25519 key; prints the public key
```

Order: new value → `validate:` rules → login check (SSH / registry, skip with `--no-verify`) → plan for that key only → confirm → old file copied to `<file>.bak` (0600, gitignored) → env file updated in place (comments and order kept) → only that key pushed → audit log. For a generated SSH key env4ci prints the `authorized_keys` line and waits until you have installed it, then logs in with the new key before anything is written. Values are never printed.

## Value rules

`validate:` in env4ci.yaml checks values before `diff` / `push` (and in `env4ci validate`). A failure names the key and the rule, never the value, and nothing is written.

```yaml
validate:
  DATABASE_URL: { required: true, type: url, pattern: "^postgres(ql)?://" }
  SENTRY_DSN:   { required_in: [production] }
  APP_PORT:     { type: port }          # url | port | int | bool | email
  JWT_SECRET:   { min_len: 32, max_len: 128 }
  LOG_LEVEL:    { one_of: [debug, info, warn, error] }
```

## Audit log

With `audit_log:` in env4ci.yaml (`env4ci init` sets `env4ci-audit.jsonl`), every push appends one JSON line — commit the file to keep a history:

```json
{"time":"2026-10-05T07:00:00Z","actor":"octocat","provider":"github","target":"acme/api","environment":"production","changes":[{"key":"DATABASE_URL","kind":"secret","action":"update"}],"result":"ok"}
```

Actor is `GITHUB_ACTOR` / `GITLAB_USER_LOGIN` in CI, else `git config user.email`. Values are never written. A failed push is logged with the error as `result`.

## Leak check

`env4ci leaks` reads the secret values from your local env files and searches every git-tracked file for them. It prints only `file:line` and the key name; values never appear.

```console
$ env4ci leaks
✗ deploy/values.yaml:12  value of DATABASE_URL

1 secret value(s) in tracked files. Remove them; if already pushed, rotate the secret.
$ env4ci hook        # pre-commit hook: runs "env4ci leaks --staged" before each commit
```

Values shorter than 8 characters and non-secret keys are not searched. An existing pre-commit hook is never overwritten; env4ci tells you the line to add.

## Authentication

| Provider | Token | Needs |
|----------|-------|-------|
| GitHub | `GITHUB_TOKEN` / `GH_TOKEN`, else `gh auth token` | fine-grained: *Secrets*, *Variables* and *Environments* read/write · classic: `repo` |
| GitLab | `GITLAB_TOKEN` | scope `api`, role Maintainer |
| Vault | `VAULT_TOKEN`, else `~/.vault-token` | `create`, `read`, `update` on the KV v2 path |

Put the token in the auth file `env4ci gen` creates (`.github/env4ci.env`, `.gitlab/env4ci.env` or `.env.env4ci`; gitignored, mode 0600), or export it. A real environment variable wins over the file. Tokens never go in env4ci.yaml.

Self-hosted: `targets.github.base_url` (GHES `https://host/api/v3`), `targets.gitlab.base_url` or `GITLAB_URL`.

### GitHub token

Easiest: `gh auth login` once. env4ci then uses `gh auth token` and `GITHUB_TOKEN` can stay empty.

For a token of its own (CI, or no `gh`):

1. GitHub → your avatar → **Settings** → **Developer settings** → **Personal access tokens** → **Fine-grained tokens** → **Generate new token**.
2. **Resource owner**: the user or organization that owns the repository. **Repository access**: *Only select repositories* → the repository.
3. **Repository permissions**, each *Read and write*:
   - **Secrets** and **Variables** — repository level (`--shared`).
   - **Environments** — secrets and variables of an environment (`-e production`, or a branch mapped through `branches:`).
4. **Generate token**, copy it (`github_pat_...`; shown once) into `GITHUB_TOKEN`.

An organization may have to approve the token before it works. A classic token (**Tokens (classic)**) needs the `repo` scope. The environment itself must already exist: repository → **Settings** → **Environments** → **New environment**.

In GitHub Actions the built-in `GITHUB_TOKEN` cannot write secrets; store a token as a repository secret and pass it:

```yaml
env:
  GITHUB_TOKEN: ${{ secrets.ENV4CI_TOKEN }}
```

### GitLab token

1. Project → **Settings** → **Access tokens** → **Add new token** (a project access token; where the plan has none, use a personal one: avatar → **Edit profile** → **Access tokens**).
2. **Role**: *Maintainer*. **Scopes**: `api`. Set an expiry date.
3. **Create**, copy it (`glpat-...`; shown once) into `GITLAB_TOKEN`.

A personal token works only if its user is Maintainer or Owner of the project. Self-hosted GitLab: also set `GITLAB_URL` (or `targets.gitlab.base_url`).

### Vault token

env4ci writes one KV v2 secret per environment at `<mount>/data/<path>`. For `mount: secret` and `path: api/{env}`:

```bash
vault policy write env4ci - <<'EOF'
path "secret/data/api/*" {
  capabilities = ["create", "read", "update"]
}
EOF
vault token create -policy=env4ci -ttl=720h   # copy "token" into VAULT_TOKEN
```

Or log in as yourself with `vault login`: it writes `~/.vault-token`, which env4ci reads when `VAULT_TOKEN` is empty. `VAULT_ADDR` is the server address (else `targets.vault.address`); `VAULT_NAMESPACE` is for Enterprise / HCP namespaces.

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
