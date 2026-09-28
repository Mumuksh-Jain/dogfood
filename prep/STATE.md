# STATE

## Current phase
T2 JUDGING: OFFLINE-FIRST RUNTIME PROOF & COMPLETE VERIFICATION

## Last checkpoint passed
Checkpoint 15: Offline-First Requirement & Complete Network-Isolated Verification
- Mandatory Offline-First Audit:
  - Scanned all codebase files: 0 external CDNs, 0 Google Fonts, 0 external CSS/JS imports, 0 remote analytics, 0 third-party auth, 0 external databases, 0 remote scoring services.
  - All UI templates (`web/templates/*.html`) and stylesheets (`web/static/app.css`) are embedded directly into the Go executable via `embed.FS`.
  - The production Docker image uses `FROM scratch`, containing only `/dogfood` and `/official/fixtures.json`.
- Live Network-Isolated Docker Proof:
  - Created Docker internal network `dogfood_offline_test` with `--internal` flag (completely blocking all internet and WAN egress).
  - Proved outbound connectivity unreachable: `ping 8.8.8.8` returns `Network unreachable`; external DNS/HTTP requests fail with `[Errno 101] Network is unreachable`.
  - Executed full 14-step offline browser and workflow test suite from an attached container:
    - Public pages (`/`, `/projects`, `/projects/prj_01`, `/login`): 200 OK, zero external assets.
    - Organizer workflows (assignments, results compute, results publish): 200 OK.
    - Participant workflows (`/dashboard`, team view, 403 authorization guards): 200/403 OK.
    - Judge workflows (evaluation, draft, finalization, 409 ballot immutability lock): 200/409 OK.
    - Peer isolation (Judge B blocked from Judge A evaluation and peer score API): 403 Forbidden.
    - Results freshness & public leaderboard (`/results`, `/api/results`): 200 OK.
    - Explain This Rank receipt (`/results/{run_id}/projects/{project_id}`): 200 OK.
    - Auditable CSV export (`/api/export.csv`): 401 anon, 403 participant, 200 organizer with RFC 4180 and F21 formula sanitization.
    - REST API v1 parity (`/api/v1/projects`, `/api/v1/teams`): 200 OK.
  - Executed independent CLI replay inside isolated container: `100% Mathematical Equality Verified`.
  - Executed official test runner (`official/run.py`) on `--internal` network: 7/7 PASS (`claimed T1 T2, verified T1 T2`).

## Current blocker
none

## Last checker output
T1  gallery is public ................. PASS
T1  project from fixtures shown ....... PASS
T1  closed event refuses submissions .. PASS
T2  judge sees own scores ............. PASS
T2  judge cannot see peer scores ...... PASS
T2  participant blocked ............... PASS
T2  csv export works .................. PASS
claimed T1 T2, verified T1 T2

## Next action
Ready for final review, commit, and push. Human controls Git.

## Known deltas since H0
None.

## Build status
- Contract research: complete
- F1.1 architecture freeze: complete
- Migration manifest: complete
- Environment preparation: complete
- Directory structure: initialized
- Application scaffold: complete and committed
- Migration 0001 (T1 Core Schema): complete and verified
- Fixture seeding: complete and verified
- First vertical slice & checker: complete and verified
- T1 core complete: complete and verified (T1 + T2 pass)
- T1 closure patch: complete and verified
- Containerized runtime: VERIFIED
- Self-contained runtime assets: VERIFIED
- Clean/reviewer-environment offline release proof: VERIFIED
- T2 judging schema: complete and verified (Migration 0002)
- T2 judging engine: complete and verified (internal/assignment)
- T2 rubric & ballot lifecycle: complete and verified (internal/judging, templates, HTTP API)
- T2 results & normalization engine: complete and verified (Migration 0003, internal/results)
- Replay differentiator: complete and verified (Explain This Rank UI, API, & CLI)
- Auditable CSV Export & API-First consistency: complete and verified (internal/httpapp, openapi.yaml, Flow I)
- Final acceptance evidence: verified (7/7 PASS)

## Rule
Update this file after every meaningful checkpoint. The next AI session must read this file before proposing work.
