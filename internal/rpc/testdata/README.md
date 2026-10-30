# RPC fixtures

Recorded Soroban RPC responses, replayed offline through `httptest` by
`client_test.go` so the suite never touches the network.

## Files

| File | Method | What it pins down |
|---|---|---|
| `getEvents.json` | `getEvents` | event decoding path: two real contract events (one registry `institution_verified`, one escrow `grant_created`), `latestLedger` and `cursor` pass-through |
| `getLatestLedger.json` | `getLatestLedger` | tip reported as `latestLedger` |
| `getLatestLedger_sequence.json` | `getLatestLedger` | tip reported as `sequence` only — the fallback path |
| `getEvents_range_error.json` | `getEvents` | retention-window error, converted to `RangeError` |
| `getEvents_plain_error.json` | `getEvents` | a non-range RPC error, propagated unchanged |

## Provenance

- The `topic` / `value` payloads are the real base64 XDR captured from the
  deployed Tuitio contracts on Stellar testnet at ledger ~4.57M
  (2026-09-08) — the same captures recorded in
  [`internal/indexer/decode_test.go`](../indexer/decode_test.go). If the
  contract event schema ever changes, those decoder tests fail first and
  these fixtures follow.
- Envelope fields (`id`, `ledger`, `ledgerClosedAt`, `contractId`,
  `txHash`, `latestLedger`, `cursor`, error shape) follow the
  `getEvents` / `getLatestLedger` result schemas documented at
  <https://developers.stellar.org/docs/data/apis/rpc/api-reference/methods/getEvents>.
  Ledger numbers, close times and hashes are consistent stand-ins chosen to
  match the captures above.

## Re-recording against a live node

```bash
curl -s -X POST https://soroban-testnet.stellar.org \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"getEvents","params":{"startLedger":4571234,"filters":[{"type":"contract","contractIds":["CCTZVSPMIYEA4X2Z6UP5IPIO2HBIZWHLV37OUDBV32YUCP64JNIPBCPO","CDPGTHO2O7LTURTJF7L7ZZZTZCXPNABAPGVRDMBYIJ737R6O3DOPWJ34"]}],"limit":100}}' \
  > testdata/getEvents.json
```

Contract addresses come from `.env.example` (`REGISTRY_CONTRACT`,
`ESCROW_CONTRACT`). After re-recording, keep the assertions in
`client_test.go` in sync — they check the fields the indexer depends on,
not byte-for-byte equality, so unrelated envelope churn (new fields, new
close-time formats) does not break the suite.
