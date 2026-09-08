# Contributing to tuitio-backend

Thanks for considering a contribution. Issues labeled **Stellar Wave** are
part of the [Drips Wave](https://www.drips.network/wave/stellar) program and
carry point values (Trivial 100 / Medium 150 / High 200) — solving them earns
a share of the wave's reward pool.

## Setup

Stack: Go 1.25 / Postgres (pgx v5).

```bash
go vet ./... && gofmt -l . && go test ./...
```

All three checks must pass before a PR is reviewable.

## Workflow

1. Comment on the issue you want to take, so work isn't duplicated.
2. Branch from `main`: `feat/short-description` or `fix/short-description`.
3. Keep PRs scoped to one issue. Conventional commit style
   (`feat(scope): …`, `fix(scope): …`, `test: …`, `docs: …`).
4. PRs require a passing CI run and one approval before merging.

## Code expectations

- Match the existing style of the file you are touching.
- New contract functions need tests covering the failure paths, not just the
  happy path. New API endpoints need handler tests. New UI needs to typecheck
  and lint clean.
- No secrets in code or config — everything network-specific goes through
  environment variables.

## Reporting security issues

Do **not** open a public issue for security problems. See [SECURITY.md](SECURITY.md).
