# Security Policy

## Status: unaudited

The Tuitio contracts are **not audited**. They hold real funds on mainnet at
your own risk; everything deployed today is on Stellar **testnet**. Treat the
escrow as experimental.

## Reporting a vulnerability

Email **adelekevat@gmail.com** with details and a reproduction if possible.
Please do not open public issues for vulnerabilities. You will get an
acknowledgement within 72 hours and a status update within 7 days.

## Scope

- The Soroban contracts in this repository (storage handling, authorization,
  arithmetic, cross-contract calls).
- Build and deployment scripts.

Out of scope: issues in dependencies themselves (report upstream), and
compromises of keys held by deployers or users.

## Disclosure

Once a fix ships we will credit the reporter unless they prefer to remain
anonymous.
