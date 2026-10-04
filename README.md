# Env4CI

> Environment configuration for CI/CD.

Sync environment variables and secrets between local `.env` files, **GitHub Actions** and **GitLab CI/CD**.

```bash
go install github.com/bakhod1r/env4ci/cmd/env4ci@latest
```

## Usage

```bash
env4ci init                      # write env4ci.yaml
env4ci validate                  # show how each key is classified
env4ci diff github -e production # plan, never prints values
env4ci push github -e production # apply after [y/N]
env4ci push gitlab --prune       # also delete remote-only keys
env4ci pull gitlab -o .env.prod  # remote -> local (0600, auto .gitignore)
```

```text
github:bakhod1r/my-api@production

  + DATABASE_URL                     secret   new
  ? JWT_SECRET                       secret   unverifiable
  ~ LOG_LEVEL                        variable changed
    APP_PORT                         variable unchanged
  - OLD_API_URL                      variable remote only
```

`?` = GitHub secrets are write-only, so env4ci cannot compare them and re-writes them on push.

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
