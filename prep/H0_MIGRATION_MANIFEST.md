# H0 Migration Manifest — design-only, non-executable

This is a schema implementation checklist derived from F1.1. It is intentionally **not SQL** so no project code is written before kickoff.

## Migration runner contract

Bootstrap metadata:
- `schema_migrations`
  - version — integer primary key
  - name — text, not null
  - checksum_sha256 — text, not null
  - applied_at_utc — text timestamp, not null

Rules:
- apply migrations in ascending version order;
- one transaction per migration;
- applied migration checksum mismatch = startup failure;
- never edit applied migration;
- forward-only during the hackathon;
- migrations run before seeding.

## 0001_t1_core

### users
PK: id
Unique: normalized email
Fields: display name, password hash/parameters, created/disabled timestamps

### sessions
PK: id
FK: user_id → users
Fields: token hash, created/expires/revoked/last-seen timestamps
Index: active session lookup by token hash

### events
PK: id
Fields: independent registration/submission/judging windows, archived timestamp, default expected reviews, simple validated prizes JSON for MVP
Checks: each open timestamp precedes matching close timestamp

### event_roles
Composite uniqueness: event_id + user_id + role + active-state policy
FKs: event_id, user_id
Fields: granted/revoked timestamps, granted_by

### tracks
PK: id
FK: event_id
Fields: name, expected-review override

### teams
PK: id
FK: event_id
Fields: name, created_by, created_at, locked_at

### team_memberships
PK: id
FKs: event_id, team_id, user_id
Fields: membership role, joined_at, left_at
Invariant: final one-team-per-event rule is decided at H0 after contract delta
Invariant: max four active members enforced transactionally

### team_invites
PK: id
FKs: event_id, team_id
Unique: token hash
Fields: optional email binding, creator, created/expires/accepted/revoked fields
Invariant: acceptance is idempotent and re-checks team capacity inside the write transaction

### projects
PK: id
FKs: event_id, team_id, track_id
Fields: eligibility status, duplicate status / optional duplicate_of_project_id, created metadata
Important: no one-project-per-team constraint

### submissions
PK: id
FKs: event_id, project_id, supersedes_submission_id
Unique: project_id + version_no
Fields: state, title, summary, repo/demo URLs, created/submitted timestamps
Invariant: submitted versions immutable
Invariant: deadline decision is atomic on server

### seed_imports
PK: id
Unique: source hash + event policy
Fields: source name/hash, event, status, start/end/error
Invariant: same fixture does not duplicate data
Invariant: different fixture never silently overwrites edited persistent data

Exit gate:
- migration on empty DB succeeds;
- repeated startup is a no-op;
- fixture gallery works;
- closed fixture submission is rejected;
- team/invite/draft flow survives restart.

## 0002_t2_judging

### judge_profiles
Composite key: event_id + user_id
Fields: capacity, active/invited/accepted/deactivated timestamps

### judge_track_eligibility
Composite key: event_id + judge_user_id + track_id
Fields: eligible, source, reason, updated_at

### assignment_runs
PK: id
FK: event_id
Fields: algorithm key/version, config JSON, requested total, status, timestamps, structured infeasibility code/details
Invariant: an infeasible run is representable even when zero assignments exist

### assignments
PK: id
FKs: event_id, assignment_run_id, judge_user_id, project_id, rubric_version_id
Fields: status, timestamps, supersedes_assignment_id, reason
Invariant: no duplicate active judge/project assignment
Invariant: completed history is never overwritten by reassignment

### rubric_versions
PK: id
FKs: event_id, optional track_id
Unique: logical rubric scope + version
Fields: state, immutable canonical criteria JSON, config hash, creator/timestamps
Checks performed by application/service:
- no negative weights
- not all zero
- valid min/max
- stable criterion keys
Invariant: published version immutable

### ballot_versions
PK: id
FKs: assignment_id, judge_user_id, project_id, rubric_version_id
Unique: assignment_id + version_no
Fields: save kind/state, immutable canonical scores JSON, comment, timestamps, supersedes_version_id
Invariant: required criterion missing ≠ zero
Invariant: correction creates a new version

Exit gate:
- deterministic assignment runs;
- infeasible assignment returns structured reason;
- judge sees own work only;
- participant/peer are denied;
- weighted ballot can be independently recalculated;
- reassignment preserves submitted history.

## 0003_results_replay

### result_runs
PK: id
FK: event_id, optional supersedes_result_run_id
Fields:
- method key/version/config
- cohort/tie/eligibility policy identifiers
- status/timestamps
- exact canonical input manifest JSON
- input digest
- publication fields
- failure reason
Invariant: published run immutable
Invariant: correction = new run

### result_entries
Composite key: result_run_id + project_id
Fields:
- raw/normalized/final score
- expected/completed/effective counts
- fallback count
- rank/tie group
- exact per-project explanation JSON
Invariant: replay inputs identify exact ballot versions and fallbacks

Exit gate:
- result publishes;
- Explain this rank renders;
- independent replay returns equality;
- correction produces a new run without rewriting prior history.

## Deferred until a demonstrated need

- dedicated prizes table
- logical rubrics parent
- normalized rubric_criteria
- ballots parent
- normalized ballot_items
- result_run_ballots relational mapping
- separate result_snapshots
- audit_events
- judge_conflicts
- duplicate_findings / duplicate_finding_projects

Adding one requires a new forward migration; it never edits 0001–0003 after those migrations have been applied.
