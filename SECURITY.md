# Security Policy

env4ci handles CI secrets, so security reports get priority.

## Reporting

Use [GitHub private vulnerability reporting](https://github.com/bakhod1r/env4ci/security/advisories/new).
Do not open a public issue. Expect a first reply within 72 hours.

## Guarantees

- Secret values are never printed: not in `diff`, `push`, `scan`, errors, or logs.
- GitHub secrets are encrypted locally (libsodium sealed box) before leaving the machine.
- Tokens are read only from the environment or `gh auth token`; the config file rejects unknown fields.
- `push` never deletes without `--prune`, and asks before writing unless `--yes`.
- `pull` writes files with mode `0600`, refuses to overwrite, and adds them to `.gitignore`.

A breach of any of these is a vulnerability.

## Supported versions

The latest minor release.
