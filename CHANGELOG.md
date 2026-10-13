# Changelog

## Unreleased

### Added
- `env4ci gen` re-reads the CI files and follows them both ways: an environment CI no longer deploys to (for example `dev` dropped from `branches: [dev, main]`) leaves `env4ci.yaml` (`environments:` and `branches:` lines), its env file is deleted and its `.gitignore` line removed. It asks first on a terminal, showing how many values the file holds; `-y` removes without asking; otherwise it is only reported.
- `env4ci completion bash|zsh|fish`: tab completion of commands, each command's flags and provider names. `install.sh` installs the scripts.
- CI files that name no `environment:` but run on listed branches (`on: push: branches: [dev, main]`) give one environment per branch in `init` and `gen` (`main` -> `production`, `dev` -> `staging`, else the branch name). With `environment: ${{ github.ref_name }}` the environment is the branch's own name, and those jobs' keys go into every environment file.
- README: how to get the GitHub, GitLab and Vault tokens. GitHub environment secrets/variables need the *Environments* read/write permission (the docs said read).
- `env4ci gen` adds an environment the CI files deploy to that `env4ci.yaml` lacks: its `environments:` (and `branches:`) line is inserted, the rest of the file is untouched, and its `.env` file is created. Run it after changing CI files.
- `env4ci <command> --help` and `env4ci help <command>`: one command's usage and flags. `env4ci man` prints the manual page; `install.sh` installs it, so `man env4ci` works.
- `env4ci update`: replace the binary with the latest GitHub release after checking it against `checksums.txt`; `--check` only reports. Homebrew / Scoop installs are pointed to their own upgrade command.
- `env4ci gen`: `.env.env4ci` (env4ci tokens/addresses, auto-loaded, never synced) plus one `.env.<env>` per environment pre-filled from CI files; only appends missing keys. `init` runs it.
- HashiCorp Vault KV v2 target (`targets.vault`, `path: app/{env}`), check-and-set writes, one version per push.
- `env4ci init` wizard: one or many environments; github / gitlab / vault targets; `--targets`, `--envs`.
- `env4ci init` no longer asks for environments: it takes the ones the CI files deploy to (`environment:`), else the checked-out branch decides (`main`/`master` -> `production`, `develop`/`dev` -> `staging`, else the branch name). `branches:` gets the branch the CI jobs run on, or the real checked-out branch. `--envs` still sets them by hand.
- Every environment file starts with `ENVIRONMENT=<name>`; `init` classifies it as a variable.
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
