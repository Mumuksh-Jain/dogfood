# DELTA_LOG.md

This file records every observed change from the pre-kickoff baseline captured in
DOGFOOD_PREBUILD_FREEZE_F1_v1.1.md. An empty delta table below the baseline means
the contract is unchanged from the frozen assumptions.

## Delta entries

| Observed UTC         | Source                 | Old assumption/hash            | New authoritative statement/hash                                                                | Affected invariant/interface | Action taken                                                    | F1 otherwise unchanged? |
| -------------------- | ---------------------- | ------------------------------ | ----------------------------------------------------------------------------------------------- | ---------------------------- | --------------------------------------------------------------- | ----------------------- |
| 2026-09-25T18:12:00Z | prep environment check | ship linux/amd64 + linux/arm64 | arm64 emulation unavailable on build machine (`exec format error` for `--platform linux/arm64`) | packaging (F1.1 A 2.3)        | ship linux/amd64 only; update README supported-platform section | yes                     |

## H0 baseline

- main-site revision/time read: 2026-09-25T18:05:00Z
- `/spec/` revision/time read: 2026-09-25T18:06:00Z
- `spec.md` SHA-256: 644B92EB50A37215CB992589E451850AB95CB14803BBAD3905FD8B071BFBD696
- `run.py` SHA-256: AA98963841BC8E18E8E5D76F0499697C093DD3C0055F9D73A459F592F4DCF09D
- `fixtures.json` SHA-256: 252896BC45D49FCA69AD413BE40C6BFDE9D9B9F9DD8DB702B3FF74EAAA181121
- example `.dogfood.toml` SHA-256: 58C974DA4F0FAA6D1A4FBB158770B34C470F2893D3169405ED73DECA0F3A9E44
- `context.txt` SHA-256: not downloaded
- Discord pins/announcements checked through: 2026-09-25T18:10:00Z at https://discord.gg/xfYPDZYqeh
- delta requiring schema change? no
- migration 0001 start approved at: 2026-09-26T13:32:00Z
