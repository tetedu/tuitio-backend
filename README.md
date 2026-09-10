<p align="center">
  <img src="docs/banner.svg" alt="Tuitio" width="480">
</p>

# Tuitio · Backend

[![CI](https://github.com/tetedu/tuitio-backend/actions/workflows/ci.yml/badge.svg)](https://github.com/tetedu/tuitio-backend/actions/workflows/ci.yml)
[![License: Apache-2.0](https://img.shields.io/badge/License-Apache--2.0-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-1.25-00add8)](go.mod)

**Go indexer and REST API for the Tuitio protocol.** It watches the deployed
Soroban contracts on Stellar, decodes every contract event from XDR, and folds
them into a Postgres read model the frontend can query.

## What it does

- **Polls** the Soroban RPC `getEvents` for the two Tuitio contracts.
- **Decodes** contract events (typed `#[contractevent]` structs arriving as
  XDR: snake_case symbols, topic fields in the topic array, the rest in a
  data map).
- **Applies** them to Postgres idempotently — the cursor and the events it
  covers commit in one transaction, and every write is a max- or upsert-style
  operation, so a crash mid-batch replays to the same state.
- **Serves** a read-only REST API.

## API

| Endpoint | Description |
|---|---|
| `GET /healthz` | liveness + database check |
| `GET /api/institutions` | all registered institutions |
| `GET /api/institutions/{address}` | one institution |
| `GET /api/grants?sponsor=&institution=&status=` | grants, filterable |
| `GET /api/grants/{id}` | one grant with escrowed amount |
| `GET /api/grants/{id}/terms` | every term, defaulting to `pending` like the contract |
| `GET /api/activity?limit=` | raw event audit feed |
| `GET /api/stats` | protocol counters |

## Quick start

The fastest path runs the whole stack — embedded Postgres, testnet indexer,
API — with no database provisioning:

```bash
go run ./cmd/devstack    # API on :8080, Postgres on :54329
```

Against a real database:

```bash
cp .env.example .env     # fill DATABASE_URL and contract addresses
go run ./cmd/api
```

Tests (unit + integration with an embedded Postgres):

```bash
go test ./...
TUITION_LIVE=1 REGISTRY_CONTRACT=… ESCROW_CONTRACT=… START_LEDGER=… \
  go test ./internal/indexer/ -run TestLiveIngestion   # full pipeline vs testnet
```

## Configuration

See [.env.example](.env.example): `DATABASE_URL`, `SOROBAN_RPC_URL`,
`REGISTRY_CONTRACT`, `ESCROW_CONTRACT`, `START_LEDGER` (0 = start at chain
tip; set below the first tracked event to backfill), `POLL_SECONDS`, `PORT`.

## Deployment

A Render blueprint is included ([render.yaml](render.yaml)): web service from
the Dockerfile plus a co-located Postgres, wired with the internal connection
string.

## Related repositories

- [`tuitio-contract`](https://github.com/adelekevictor12/tuitio-contract) — the Soroban contracts (Rust)
- [`tuitio-frontend`](https://github.com/adelekevictor12/tuitio-frontend) — Next.js web app

## Maintainers

| Name | Role | Contact |
|---|---|---|
| [adelekevictor12](https://github.com/adelekevictor12) | Maintainer | adelekevat@gmail.com |

## Contributing

Issues labeled `Stellar Wave` are part of the [Drips Wave](https://www.drips.network/wave/stellar)
program and carry point values. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Apache-2.0. See [LICENSE](LICENSE).
