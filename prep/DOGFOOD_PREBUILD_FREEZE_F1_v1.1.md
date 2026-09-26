# DOGFOOD 2026 — F1 Pre-Build Freeze

## Contract → State Machine → Schema → Offline Packaging

**Freeze date:** 24 September 2026  
**Purpose:** This is a pre-build design artifact, not project code. It freezes only the decisions that are expensive to retrofit after kickoff.  
**Rule:** If a later implementation detail does not violate an invariant in this document, it may change during the build without reopening F1.

---

# 0. Executive freeze

The pre-kickoff research phase is complete when this document is accepted.

The project will be built around four fixed decisions:

1. **Data model first:** event-scoped authorization, immutable rubric/ballot/result versions, assignment-run failure records, duplicate adjudication without silent merging, and exact result-input provenance.
2. **Offline-first packaging:** a self-contained Go application with embedded UI assets and SQLite, shipped as precompiled Linux binaries and wrapped by a `FROM scratch` container so `docker compose up` does not need to pull a runtime image or fetch packages.
3. **API-first from the beginning:** all product actions have `/api/v1` equivalents and are documented in `openapi.yaml`; the browser UI is a thin client of the same application/service layer rather than a separate business-logic path.
4. **Default differentiator locked:** **Replayable Result Receipt / Explain This Rank**. Every published result must be independently reproducible from immutable ballot versions, rubric version, method configuration, eligibility decisions, fallback reasons, and tie policy.

Anything outside those decisions is implementation detail unless it changes a contract requirement.

---

# 1. Contract freeze

## 1.1 What is authoritative

At H0, the authoritative contract is:

- the current Dogfood main site and `/spec/` page,
- the current downloadable `spec.md`,
- the current downloadable `run.py`,
- the current downloadable `fixtures.json`,
- the current example `.dogfood.toml`,
- any organizer clarification pinned or announced in the official Discord.

The current specification explicitly says the downloadable files are already available before kickoff. Therefore the pre-event copies are valid research inputs. At H0 they must be re-downloaded/re-read and compared against the frozen pre-build assumptions before code starts.

## 1.2 Current machine-checkable contract

The current public checker contract exposes seven checks:

**T1**

- public gallery returns HTTP 200 without authentication,
- gallery contains a known fixture project title,
- submission against the fixture's closed event returns a 4xx response.

**T2**

- judge A can read their own scores,
- judge B cannot read judge A's scores,
- a participant cannot read judge scores,
- organizer CSV export returns HTTP 200 and CSV content.

The checker intentionally does not dictate language, database, schema, route structure, or authentication mechanism.

## 1.3 Current non-checker requirements that still matter

Manual judging still evaluates:

- complete lifecycle rather than checker-only endpoints,
- backend authorization,
- judge assignment,
- weighted rubrics,
- normalization,
- self-hosting and offline startup,
- import/export,
- documentation,
- architecture/data-model quality,
- an actual end-to-end demo.

Therefore passing the seven checks is necessary evidence, not the product definition.

## 1.4 Hard offline interpretation

The specification says `docker compose up` must bring up a seeded portal **with the network off** and without a cloud account, hosted database, external API, or hosted authentication service.

The specification does not explicitly define whether reviewers are guaranteed to have language/runtime base images pre-pulled. We therefore adopt the stronger interpretation:

> A clean reviewer environment that already has Docker/Compose but no network must not need to pull a base image, download language packages, run npm/pip/go package downloads, or contact an external service in order to start the submitted portal.

That interpretation shapes the stack and packaging decision below.

## 1.5 Tier-claim policy — frozen sentence

> **At final freeze, `.dogfood.toml` will claim only the highest contiguous tier set that the authoritative kickoff acceptance suite reports as verified on the exact clean submitted commit; any higher-tier functionality is described separately as implemented or partial, never claimed when the suite does not verify it.**

This sentence does not get renegotiated at H68.

---

# 2. Stack and offline-packaging decision

## 2.1 Frozen stack

**Backend/runtime:** Go 1.27.1 toolchain, compiled to self-contained Linux executables. The final runtime does not require Go to be installed.  
**HTTP layer:** Go standard library (`net/http`) unless a tiny dependency provides a material build-time benefit.  
**Database:** SQLite through a pinned pure-Go, CGo-free driver.  
**Frontend:** embedded HTML/CSS/vanilla JavaScript; no Node/npm build pipeline and no CDN assets.  
**API:** versioned REST surface under `/api/v1`, with a committed `openapi.yaml`.  
**Persistence:** one SQLite database on a Docker named volume or bind-mounted `/data` path.  
**Time:** all persisted timestamps UTC; display conversion is presentation-only.  
**IDs:** text IDs. Fixture IDs are preserved exactly; newly created IDs use a collision-resistant application prefix/UUID/ULID strategy.

## 2.2 Why this stack is frozen

The stack is chosen primarily for **offline operability and deployability**, not novelty.

A compiled Go application can contain its templates and static assets in the executable. A pure-Go SQLite driver avoids a system SQLite package or C toolchain at runtime. The runtime container can therefore be `scratch` and contain only:

- the platform-specific application executable,
- the current official fixture file if the application reads it externally,
- optional local CA/timezone data only if a real feature needs it.

The application does not need outbound TLS or timezone databases for the core Dogfood workflow, so these should not be added by default.

## 2.3 Multi-architecture packaging

During the hackathon, build linux/amd64 only.

linux/arm64 was dropped on 24 September 2026 after a prep test on the build machine returned exec format error for --platform linux/arm64. The brief does not require multi-architecture. Adding arm64 later is one extra build command, not a schema change.

The repository contains the resulting binaries under `dist/`.

The Docker build selects the binary matching Docker's target architecture. The default path assumes a modern BuildKit-backed Docker/Compose installation; the README also records the exact supported host/target architectures.

No project code or compiled project binary is created before kickoff. This section freezes only the packaging design.

## 2.4 Cold-start rule

The final offline test is not:

> "Disconnect Wi-Fi after the container has already been built."

It is:

> "Remove the project image, keep only Docker/Compose and the cloned repository, disable network access, then run the documented one command and obtain a seeded working portal."

Any build step that attempts to contact a registry or package repository fails this gate.

## 2.5 What is forbidden by our architecture

The final implementation must not require at startup:

- `go mod download`,
- `pip install`,
- `npm install`,
- CDN JavaScript/CSS/fonts,
- cloud object storage,
- hosted email,
- OAuth/Auth0/Firebase/etc.,
- hosted database,
- outbound scoring/AI API,
- registry pull for the application runtime image.

Optional integrations may exist only if the local product remains complete without them.

---

# 3. API-first decision

API-first is frozen now because retrofitting it late is expensive.

## 3.1 API rule

Every action a human can perform in the browser must have a corresponding documented API operation.

Examples include:

- event create/update,
- team create/invite/join,
- project draft/save/submit,
- judge invitation/eligibility,
- assignment generation/reassignment,
- rubric versioning,
- ballot save/submit,
- result compute/publish,
- CSV/export,
- duplicate adjudication.

The UI may use server-rendered HTML for initial pages, but it must not contain a second hidden business-logic implementation. Business rules live in application/service functions invoked by both API and any server-rendered adapters.

## 3.2 Route shape

Canonical API prefix:

`/api/v1/...`

The acceptance-suite-facing routes in `.dogfood.toml` may point to HTML or API routes as appropriate. The application does not distort its internal design around checker-specific route names.

## 3.3 OpenAPI

`openapi.yaml` is created during the first build phase and updated with API changes. It is not written from memory in the last hour.

If full "every UI action" API coverage is achieved and verified, API First can be demonstrated. If not, the partial API remains useful architecture but the bonus is not claimed.

---

# 4. Lifecycle model

There is deliberately **no single giant event status enum**.

An event has independent gates.

## 4.1 Event gates

### Registration gate

Derived from:

- `registration_opens_at`
- `registration_closes_at`

States:

- NOT_OPEN
- OPEN
- CLOSED

### Submission gate

Derived from:

- `submissions_open_at`
- `submissions_close_at`

States:

- NOT_OPEN
- OPEN
- CLOSED

### Judging gate

Derived from:

- `judging_opens_at`
- `judging_closes_at`

States:

- NOT_OPEN
- OPEN
- CLOSED

### Community-voting gate

Only activated if T3 is built:

- NOT_OPEN
- OPEN
- CLOSED

### Publication

Publication is represented by immutable result snapshots, not by changing an event into a "published" mega-state.

### Archive

An event may have `archived_at`; archival blocks ordinary writes but does not destroy history.

---

# 5. Authorization model

Roles are scoped to an event except system admin.

A user may legitimately hold multiple roles, so role membership is a relation, not one `user.role` column.

## 5.1 Core roles

- visitor
- participant
- judge
- organizer
- admin

## 5.2 Frozen permission boundary

| Capability                   | Visitor | Participant |  Judge | Organizer | Admin |
| ---------------------------- | ------: | ----------: | -----: | --------: | ----: |
| Public gallery               |     yes |         yes |    yes |       yes |   yes |
| Own team/project             |      no |         yes |     no |       yes |   yes |
| Other team's private draft   |      no |          no |     no |       yes |   yes |
| Own assignments              |      no |          no |    yes |       yes |   yes |
| Own submitted ballots        |      no |          no |    yes |       yes |   yes |
| Peer judge ballots           |      no |          no | **no** |       yes |   yes |
| Aggregate unpublished result |      no |          no |     no |       yes |   yes |
| Assignment configuration     |      no |          no |     no |       yes |   yes |
| Rubric configuration         |      no |          no |     no |       yes |   yes |
| Audit log                    |      no |          no |     no |       yes |   yes |
| Publish/correct results      |      no |          no |     no |       yes |   yes |

Every protected query must include event scope. "I know a record ID" never bypasses event authorization.

---

# 6. Frozen schema

The schema below is conceptual and non-executable. Exact SQL types/index syntax can be written after kickoff, but the entities, relationships, and invariants are frozen.

## 6.1 Identity and event

### `users`

Purpose: global identity.

Key fields:

- `id` — PK, text
- `email_normalized` — unique
- `display_name`
- `password_hash`
- `password_params`
- `created_at`
- `disabled_at` nullable

Constraints:

- normalized email unique
- disabling a user does not delete their historical records

### `sessions`

Purpose: local authentication/session lifecycle.

Key fields:

- `id` — PK
- `user_id` — FK users
- `token_hash`
- `created_at`
- `expires_at`
- `revoked_at` nullable
- `last_seen_at`

Constraints:

- only token hashes persisted
- expired/revoked sessions rejected
- seeded checker credentials are clearly identified as fixture/demo credentials

### `events`

Purpose: event configuration and independent lifecycle windows.

Key fields:

- `id` — PK, preserves fixture event ID
- `name`
- `registration_opens_at`
- `registration_closes_at`
- `submissions_open_at`
- `submissions_close_at`
- `judging_opens_at`
- `judging_closes_at`
- `voting_opens_at` nullable
- `voting_closes_at` nullable
- `default_expected_reviews`
- `archived_at` nullable
- `created_at`
- `updated_at`

Constraints:

- all timestamps UTC
- open time must precede corresponding close time when both exist
- fixture `submissions_close` is imported exactly

### `event_roles`

Purpose: event-scoped authorization.

Key:

- `(event_id, user_id, role)` composite PK

Fields:

- `event_id` — FK
- `user_id` — FK
- `role` — participant/judge/organizer/admin-equivalent event role
- `granted_at`
- `granted_by`
- `revoked_at` nullable

Constraint:

- active role rows are immutable history; revocation is recorded rather than deleting evidence

### `tracks`

Purpose: event tracks and review-target overrides.

Key fields:

- `id` — PK
- `event_id` — FK
- `name`
- `expected_reviews_override` nullable
- `created_at`

Constraints:

- unique `(event_id, name)` unless organizer explicitly supports duplicate display names
- effective expected reviews = track override if present else event default

### `prizes`

Purpose: T1 event prize configuration.

Key fields:

- `id` — PK
- `event_id` — FK
- `track_id` nullable FK
- `title`
- `description`
- `amount_text` nullable
- `position` nullable

No currency arithmetic is required by the current contract.

---

## 6.2 Judge eligibility

### `judge_profiles`

Purpose: event-specific judging capacity/status.

Key:

- `(event_id, user_id)`

Fields:

- `capacity`
- `active`
- `invited_at`
- `accepted_at` nullable
- `deactivated_at` nullable

Constraint:

- capacity non-negative

### `judge_track_eligibility`

Purpose: explicit eligibility graph used by assignment.

Key:

- `(event_id, judge_user_id, track_id)`

Fields:

- `eligible`
- `source` — fixture / organizer / rule
- `reason` nullable
- `updated_at`

This is not inferred only from completed ballots.

### `judge_conflicts`

Purpose: project-specific conflict exclusions.

Key fields:

- `id` — PK
- `event_id`
- `judge_user_id`
- `project_id`
- `reason`
- `declared_by`
- `active`
- `created_at`

Constraint:

- active conflicts make the judge/project edge ineligible for future assignment

---

## 6.3 Teams

### `teams`

Purpose: team identity.

Key fields:

- `id` — PK
- `event_id` — FK
- `name`
- `created_by`
- `created_at`
- `locked_at` nullable

Do **not** assume team name uniqueness; the fixture itself contains repeated names.

### `team_memberships`

Purpose: membership history.

Key fields:

- `id` — PK
- `event_id`
- `team_id`
- `user_id`
- `membership_role` — owner/member
- `joined_at`
- `left_at` nullable

Hard invariants:

- at most one active team membership for a user within an event
- at most four active members in a Dogfood team
- joining and final-slot checks happen in one write transaction

### `team_invites`

Purpose: invite lifecycle and replay protection.

Key fields:

- `id` — PK
- `event_id`
- `team_id`
- `token_hash`
- `email_normalized` nullable
- `created_by`
- `created_at`
- `expires_at`
- `accepted_at` nullable
- `accepted_by` nullable
- `revoked_at` nullable

Derived state:

- PENDING
- ACCEPTED
- REVOKED
- EXPIRED

Constraints:

- invite acceptance is idempotent
- accepted/revoked/expired invite cannot create another membership
- accepting an invite re-checks team capacity and event membership inside the same transaction

---

## 6.4 Projects and submissions

### `projects`

Purpose: stable project identity independent of submitted content versions.

Key fields:

- `id` — PK, fixture ID preserved
- `event_id`
- `team_id`
- `track_id`
- `created_by`
- `eligibility_status` — ELIGIBLE / QUARANTINED / DISQUALIFIED / WITHDRAWN
- `created_at`

Important:

- **do not enforce one project per team.** The fixture has 41 project records and 40 teams, including a duplicate candidate sharing a team.
- changing a track after judging begins is a controlled organizer action because it can invalidate assignment eligibility.

### `submissions`

Purpose: immutable submitted/draft content versions.

Key fields:

- `id` — PK
- `event_id`
- `project_id`
- `version_no`
- `state` — DRAFT / SUBMITTED / SUPERSEDED / WITHDRAWN
- `title`
- `summary`
- `repo_url`
- `demo_url` nullable
- `created_by`
- `created_at`
- `submitted_at` nullable
- `supersedes_submission_id` nullable

Constraints:

- `(project_id, version_no)` unique
- a SUBMITTED version is never edited in place
- an allowed pre-deadline edit creates a new version and supersedes the previous submitted version
- at most one current submitted version per project
- submission acceptance and deadline check are atomic
- post-deadline draft mutation/submission that changes judged content is rejected

### `duplicate_findings`

Purpose: evidence and adjudication, not silent merge.

Key fields:

- `id`
- `event_id`
- `status` — OPEN / CONFIRMED / DISMISSED
- `detection_method`
- `evidence_json`
- `canonical_project_id` nullable
- `created_at`
- `decided_at` nullable
- `decided_by` nullable
- `decision_note` nullable

### `duplicate_finding_projects`

Key:

- `(finding_id, project_id)`

Fields:

- `relationship_role` nullable

Invariant:

- duplicate adjudication never deletes source projects or silently pools ballots.

---

## 6.5 Rubrics

### `rubrics`

Purpose: logical rubric identity.

Key fields:

- `id`
- `event_id`
- `track_id` nullable
- `name`
- `created_at`

### `rubric_versions`

Purpose: versioned scoring semantics.

Key fields:

- `id`
- `rubric_id`
- `version_no`
- `state` — DRAFT / PUBLISHED / RETIRED
- `created_by`
- `created_at`
- `published_at` nullable
- `configuration_hash`

Constraints:

- `(rubric_id, version_no)` unique
- published versions are immutable
- a new version is a new row, never an update to a published version

### `rubric_criteria`

Purpose: criterion definitions belonging to exactly one rubric version.

Key fields:

- `id`
- `rubric_version_id`
- `criterion_key`
- `label`
- `description`
- `weight`
- `min_score`
- `max_score`
- `required`
- `position`

Constraints:

- criterion key unique within version
- weight cannot be negative
- at least one criterion has positive weight
- max > min
- submitted ballot versions must contain every required criterion

---

## 6.6 Assignment

### `assignment_runs`

Purpose: make assignment generation itself auditable, including failure.

Key fields:

- `id`
- `event_id`
- `algorithm_key`
- `algorithm_version`
- `config_json`
- `requested_reviews_total`
- `status` — RUNNING / COMPLETED / INFEASIBLE / CANCELLED
- `created_by`
- `created_at`
- `finished_at` nullable
- `infeasibility_code` nullable
- `infeasibility_details_json` nullable

Why this exists:
A failed assignment run may produce no assignment row at all. Infeasibility therefore belongs to the run, not to a log line or nonexistent assignment.

### `assignments`

Purpose: judge/project work allocation.

Key fields:

- `id`
- `event_id`
- `assignment_run_id`
- `judge_user_id`
- `project_id`
- `rubric_version_id`
- `status` — ASSIGNED / STARTED / COMPLETED / CANCELLED / REASSIGNED
- `assigned_at`
- `started_at` nullable
- `completed_at` nullable
- `supersedes_assignment_id` nullable
- `reason` nullable

Constraints:

- only eligible judge/project edges may be newly assigned
- active conflict forbids new assignment
- capacity applies to active assignments
- same judge/project pair must not accidentally receive duplicate active assignments
- completed assignment history is never overwritten by reassignment

---

## 6.7 Ballots

### `ballots`

Purpose: stable judge/project ballot identity.

Key fields:

- `id`
- `event_id`
- `assignment_id`
- `judge_user_id`
- `project_id`
- `rubric_version_id`
- `status` — DRAFT / SUBMITTED / VOIDED
- `current_version_id` nullable
- `submitted_version_id` nullable
- `created_at`
- `submitted_at` nullable
- `voided_at` nullable
- `void_reason` nullable

Constraints:

- one ballot identity per assignment
- judge/project/rubric copied from assignment and must agree with it
- peer judges cannot query this record through protected endpoints

### `ballot_versions`

Purpose: append-only ballot revision history.

Key fields:

- `id`
- `ballot_id`
- `version_no`
- `comment`
- `save_kind` — DRAFT_SAVE / SUBMISSION / CORRECTION
- `created_by`
- `created_at`
- `supersedes_version_id` nullable

Constraints:

- `(ballot_id, version_no)` unique
- previously submitted versions are never changed
- a correction creates another version

### `ballot_items`

Purpose: raw criterion values.

Key:

- `(ballot_version_id, criterion_id)`

Fields:

- `score`

Constraints:

- criterion belongs to ballot's rubric version
- score within criterion range
- no invented zero for a missing required score

**Completed review count is derived** from valid latest submitted, non-voided ballots. It is not a mutable counter on `projects`.

---

## 6.8 Results and replay

### `result_runs`

Purpose: one deterministic computation attempt.

Key fields:

- `id`
- `event_id`
- `method_key`
- `method_version`
- `method_config_json`
- `rubric_policy`
- `cohort_policy`
- `tie_policy`
- `eligibility_policy_version`
- `status` — RUNNING / COMPUTED / FAILED
- `created_by`
- `created_at`
- `finished_at` nullable
- `input_digest`
- `failure_reason` nullable

Invariant:
Once COMPUTED and referenced by a published snapshot, the run is immutable.

### `result_run_ballots`

Purpose: exact provenance of every ballot version considered.

Key:

- `(result_run_id, ballot_version_id)`

Fields:

- `included`
- `exclusion_reason` nullable
- `fallback_code` nullable
- `raw_ballot_score`
- `normalized_ballot_score` nullable

This table is the heart of the replay differentiator.

### `result_entries`

Purpose: per-project result computed by one result run.

Key:

- `(result_run_id, project_id)`

Fields:

- `raw_score`
- `normalized_score`
- `final_score`
- `expected_review_count`
- `completed_review_count`
- `effective_review_count`
- `fallback_count`
- `rank`
- `tie_group` nullable
- `explanation_json`

Review-count semantics:

- **expected** comes from event/track configuration at computation time and is copied into the result record
- **completed** is derived from valid submitted ballots
- **effective** is the number actually included by this result method
- these are intentionally distinct

### `result_snapshots`

Purpose: immutable publication/correction history.

Key fields:

- `id`
- `event_id`
- `result_run_id`
- `snapshot_version`
- `published_by`
- `published_at`
- `visibility` — ORGANIZER_ONLY / PUBLIC
- `supersedes_snapshot_id` nullable
- `publication_note` nullable

Constraints:

- `(event_id, snapshot_version)` unique
- a published snapshot is never edited
- correcting published results creates a new result run and a new snapshot
- old snapshots remain inspectable by authorized organizers

---

## 6.9 Audit and seed provenance

### `audit_events`

Purpose: explain security-sensitive state transitions.

Key fields:

- `id`
- `event_id`
- `actor_user_id` nullable for system
- `action`
- `target_type`
- `target_id`
- `before_json` nullable
- `after_json` nullable
- `reason` nullable
- `request_id`
- `created_at`

Do not put secrets/session tokens/password material into audit payloads.

### `seed_imports`

Purpose: make fixture loading idempotent and traceable.

Key fields:

- `id`
- `source_name`
- `source_sha256`
- `event_id`
- `status` — STARTED / COMPLETED / FAILED
- `started_at`
- `completed_at` nullable
- `error` nullable

Startup rule:

- empty DB + unseen fixture => transactional import
- same fixture hash already imported => no-op
- persistent edited DB + different fixture hash => fail loudly or require explicit reset/import; do **not** silently overwrite user data

---

# 7. Frozen core invariants

These are more important than exact endpoint names.

1. Every event-scoped domain record is directly scoped by `event_id` or unambiguously reaches one through a parent whose ownership is validated.
2. Authorization is enforced before data leaves the backend.
3. A judge never receives a peer judge's private ballot through any read path.
4. Fixture source IDs are preserved.
5. Published rubric versions are immutable.
6. Submitted ballot versions are immutable.
7. Published result snapshots are immutable.
8. Every published result can identify the exact ballot versions it used.
9. Every ballot identifies the exact rubric version it was scored against.
10. Duplicate detection does not merge source records or ballots automatically.
11. Completed ballots survive judge reassignment/deactivation unless explicitly voided with a recorded reason.
12. Deadline acceptance is decided atomically on the server, not by the browser.
13. Team capacity and one-active-team-per-event are enforced atomically.
14. Expected, completed, and effective review counts have different meanings and are never collapsed.
15. Assignment infeasibility is persisted as structured data on an assignment run.
16. Seed restart is idempotent and never silently overwrites post-seed edits.
17. Result correction creates a new history entry; it never rewrites what was previously published.
18. UI state hiding is never a substitute for API authorization.
19. CSV/export output contains stable IDs sufficient to reconcile exported data with stored records.
20. No mandatory product flow requires network access to an external service.

---

# 8. Six destructive paper simulations

F1 is not frozen until the schema survives all six.

## Simulation A — two users claim the final team slot

Initial:

- team has 3 active members
- maximum = 4
- users A and B accept valid invites concurrently

Rule:

- both operations obtain a write transaction
- after obtaining the transaction, each re-checks event membership, invite validity, and active member count
- only one insert may commit
- the other receives a conflict/full response

No fifth member can exist even transiently.

## Simulation B — submission request races the deadline

Rule:

- the write transaction is obtained first
- server UTC time is sampled inside the operation after write access is acquired
- if sampled time is at or after `submissions_close_at`, the mutation is rejected
- if allowed, the submitted version and its timestamp are written in the same transaction

A request queued before the deadline but unable to obtain its write transaction until after the deadline is rejected. This prevents lock contention from creating an accidental grace period.

## Simulation C — organizer changes rubric after judging started

Initial:

- rubric version 3 is PUBLISHED
- ballots already reference version 3

Rule:

- version 3 cannot be edited
- organizer may draft version 4
- activating a materially changed version while valid submitted ballots exist requires an explicit re-review/migration decision; existing ballots are never silently reinterpreted
- result computation states which rubric-version policy it used

For the 72-hour build, the UI should prefer simply freezing the rubric after the first submitted ballot.

## Simulation D — judge is removed after submitting a ballot

Rule:

- deactivation blocks future assignments/authenticated judging
- submitted ballot remains immutable
- organizer may mark that ballot void only with a recorded reason
- replacement assignment is a new row
- result run records whether the previous ballot was included or excluded

No score disappears because an assignment row was overwritten.

## Simulation E — duplicate discovered after both projects were judged

Rule:

- create `duplicate_findings`
- preserve both projects and all ballots
- optionally quarantine affected project(s) from publish eligibility
- organizer explicitly confirms/dismisses and names canonical project if needed
- never pool nine source ballots into "nine independent reviews" when reviewers overlap
- result run records eligibility/exclusion policy

## Simulation F — published result is corrected

Initial:

- snapshot 1 is public

Rule:

- old ballots/rubric/snapshot are not rewritten
- correction creates new ballot version or eligibility decision where appropriate
- compute result run 2
- publish snapshot 2 with `supersedes_snapshot_id = snapshot 1`
- public UI displays current snapshot
- authorized audit/replay can still reproduce snapshot 1

---

# 9. Assignment interface freeze

The assignment implementation may change, but it must consume:

- project track,
- judge track eligibility,
- active conflict edges,
- judge capacity,
- expected reviews per track/event,
- existing completed/active assignments.

It must output:

- deterministic assignments for the same input/config,
- coverage counts,
- structured infeasibility when targets cannot be met,
- reassignment without erasing completed ballots.

The existing fixture analysis demonstrated that global nominal capacity can be sufficient while track-local capacity still makes the target infeasible. Therefore a greedy "keep assigning until capacity runs out" implementation is not an acceptable default.

A deterministic max-flow/bipartite matching baseline is the intended build direction. Advanced affinity optimization is dormant.

---

# 10. Scoring/normalization interface freeze

Full normalization research is closed. The build uses one defensible method with explicit limits.

## Raw ballot score

For a ballot version:

`weighted sum(score_i * weight_i) / sum(weights)`

Required criteria must be present. Missing required values are incomplete, not zero.

## Guarded normalization baseline

Cohort:

- judge × track, so severity calibration does not silently mix unrelated track batches.

Guard:

- if usable cohort size is too small (especially n=1 or n=2), do not pretend a stable judge distribution exists;
- if judge score variance is zero, z-score is undefined;
- those ballots use the documented fallback.

When z-normalization is valid:

- compute judge-local standardized score,
- map it back onto the corresponding track score scale,
- clamp only to the legal rubric range if necessary and record that policy.

Fallback:

- preserve/use the raw weighted score for that ballot,
- record fallback code and effective-review impact in result provenance.

The exact numeric implementation can be finalized during the build, but **fallbacks can never be silent**.

---

# 11. Replayable Result Receipt — locked differentiator

For any published project result the organizer/judge demo can open:

**Explain this rank**

It must show:

- published snapshot ID/version,
- project ID,
- rubric version,
- included ballot versions,
- excluded ballots and reasons,
- raw weighted ballot values,
- normalization/fallback applied to each,
- expected/completed/effective review counts,
- final score arithmetic,
- tie policy,
- final rank,
- result method/version/config hash,
- independent replay status.

The corresponding test/CLI/service operation recomputes the result from stored immutable inputs and compares it to the frozen published value.

If replay requires mutating historical data, the schema is wrong.

---

# 12. Seed/import strategy

At first boot:

1. create/migrate schema,
2. hash `fixtures.json`,
3. inspect `seed_imports`,
4. if database is empty, import fixture atomically,
5. create fixture-associated users/roles needed by the UI and checker,
6. create stable local checker credentials,
7. record completed seed import,
8. print demo/checker credential information to startup logs without printing password hashes or unrelated secrets.

Restart:

- migrations run,
- seed import detects same hash,
- no duplicated teams/projects/ballots are created,
- user edits persist.

---

# 13. Authentication decision

Local authentication only.

Normal UI:

- email/password login,
- password hashing using a current standard-library KDF available in the selected Go toolchain,
- opaque random session token,
- HttpOnly/SameSite cookie,
- CSRF protection for cookie-authenticated state-changing browser requests.

Acceptance suite:

- fixed fixture/demo **Bearer** credentials are configured in `.dogfood.toml`.
- explicit Authorization-header authentication is not browser ambient authority, so it does not depend on a CSRF token.

Fixture credentials are documented as development/evaluation credentials, not production defaults.

---

# 14. Documentation is continuous work

The following skeleton files are created immediately after project scaffold at H0, then changed in the same commits that change architecture.

## `README.md`

Sections:

- what the project is
- tier status
- one-command start
- offline/cold-start verification
- demo users/credentials
- feature matrix
- honest limitations
- test/acceptance command
- data reset/backup path
- license

## `ARCHITECTURE.md`

Sections:

- design goals
- component/request flow
- offline packaging
- API-first design
- auth/authorization boundary
- transaction boundaries
- result replay
- deliberate non-goals/tradeoffs

## `DATA-MODEL.md`

Sections:

- entity overview
- schema/table definitions
- invariants
- lifecycle states
- fixture import
- migrations
- export/reconciliation
- result history

## `JUDGING.md`

Sections:

- assignment inputs/constraints
- infeasibility reporting
- raw score formula
- normalization
- n=1/n=2/zero-variance fallbacks
- duplicate adjudication
- coverage semantics
- result replay
- known statistical limitations

## `openapi.yaml`

Updated continuously with `/api/v1`.

## `acceptance-report.txt`

Never hand-written. Always generated from the authoritative checker against the final candidate commit.

---

# 15. H0 20-minute delta gate

Before the first line of project code:

1. re-open main site and `/spec/`;
2. inspect official Discord pins/announcements since this freeze;
3. re-download `spec.md`, `run.py`, `fixtures.json`, and example `.dogfood.toml`;
4. calculate/record hashes;
5. compare `run.py` cases and fixture shape with the frozen assumptions;
6. record any delta;
7. modify the freeze only if an official delta invalidates an invariant;
8. start scaffolding.

**Timebox: 20 minutes.**  
Do not restart general research.

---

# 16. H0 build order after the delta gate

The first vertical slice is intentionally tiny:

- repository + license + docs skeleton
- Go module/source
- SQLite open/migration
- fixture import
- public fixture gallery
- closed-event submission rejection
- seeded bearer identities
- `.dogfood.toml`
- first official checker run through the strict wrapper

Then complete T1 before expanding T2.

---

# 17. Dormant until stability

Do not activate before the H48 stability gate:

- community voting implementation
- comments
- advanced anti-abuse
- pairwise judging
- certificate generation
- webhooks
- embeddable gallery
- cryptographic judge records
- advanced assignment affinity optimization
- further competitor research
- further judge-profile research

API-first is **not** dormant; it is structural and begins at H0.

---

# 18. F1 closure test

F1 is closed if the answer to all of these is yes:

- Can every fixture record be represented without destructive transformation?
- Can one team legitimately have more than one project record?
- Can a submitted rubric/ballot/result be reproduced after later edits?
- Can assignment fail without losing the reason?
- Can duplicates be adjudicated without pooling ballots?
- Can expected/completed/effective review counts disagree legitimately?
- Can a final team slot race produce at most one winner?
- Can a deadline race be decided atomically?
- Can a removed judge's old ballot remain auditable?
- Can a published result be corrected without rewriting history?
- Can every UI action have an API operation?
- Can the final portal start without runtime package downloads, registry pulls, CDN assets, hosted DB/auth, or external APIs?
- Can the final tier claim be decided mechanically from acceptance evidence?

**F1 status: FROZEN, subject only to H0 official contract delta.**

---

# 19. F1.1 closure amendment — migrations, offline fallback, scope ceiling, tests, demo

This amendment closes the final pre-kickoff risks without reopening the architecture.

## 19.1 Migration strategy — frozen

Use a **small application-owned, forward-only migration runner** rather than Goose or golang-migrate.

At H0, project code will implement:

- a versioned directory such as `internal/migrations/`;
- ordered migration files named `0001_t1_core.sql`, `0002_t2_judging.sql`, `0003_results_replay.sql`, and later additive migrations;
- migrations embedded into the application binary with Go `embed`;
- a `schema_migrations` table recording:
  - migration version,
  - migration name,
  - SHA-256/checksum of the migration body,
  - applied timestamp;
- one migration transaction at a time;
- a hard failure if an already-applied migration's checksum differs from the recorded checksum;
- **never edit an applied migration**; add a new migration instead;
- forward-only migrations during the hackathon. Database rollback means restore/reset a test volume, not maintain fragile down-migrations.

Startup order:

1. open SQLite;
2. enable foreign-key enforcement and the chosen concurrency pragmas;
3. create/check the migration metadata table;
4. apply every unapplied migration in order;
5. only after migrations succeed, run fixture seeding;
6. start HTTP serving.

Seeding and migrations are separate concerns. A schema migration must not silently re-import fixtures or overwrite post-seed user edits.

This migration runner is intentionally simple so the final application has no runtime migration-tool dependency.

## 19.2 SQLite dependency freeze

Use a **pinned, vendored CGo-free SQLite driver**. The intended implementation dependency is `modernc.org/sqlite`, with its transitive dependency versions pinned exactly in `go.mod`/`vendor/`.

Reasons:

- pure Go / no CGo runtime requirement;
- supports Linux amd64 and arm64;
- keeps the runtime image compatible with the `scratch` strategy;
- vendoring means the final offline build does not need the public Go module proxy.

The exact version is pinned at H0 after one compatibility build; it must not float during final packaging.

## 19.3 Offline packaging — primary and fallback

### Primary path

The required primary path remains:

`docker compose up`

Rules for the submitted primary Compose/Docker build:

- `build:` uses a **local relative context only**;
- no remote Git build context;
- no `cache_from` registry reference;
- no Dockerfile `ADD` from an HTTP/HTTPS URL;
- no build step that downloads modules/packages/assets;
- no CDN/static remote dependency;
- no runtime registry dependency;
- `FROM scratch` remains the runtime base;
- compiled Linux binaries and vendored dependencies are produced during the official event window.

Where `image:` is used together with `build:`, the Compose configuration must explicitly prevent an accidental registry-first path. Prefer omitting `image:` from the primary build service unless an image tag is required for packaging.

### Shell-free healthcheck

A `scratch` image contains no shell, `curl`, or `wget`.

Therefore the application must expose a built-in healthcheck command, conceptually:

`dogfood healthcheck`

The Compose healthcheck uses exec form and invokes the binary directly. It must not rely on `/bin/sh`.

The same binary may also expose an HTTP `/healthz` endpoint for operators, but the container healthcheck cannot assume a shell tool exists.

### Preloaded-image fallback

The fallback is **not** the scored primary one-command path. It is an operability escape hatch for an older/unusual Docker installation or a builder incapable of the primary architecture-selection path.

During the official build window, create and commit/package:

- `dist/dogfood-image-linux-amd64.tar.gz`
- `dist/dogfood-image-linux-arm64.tar.gz`

These are produced from the exact release candidate image and are tied to the release commit/evidence log.

Fallback procedure:

1. load the correct local image archive with Docker;
2. start `compose.preloaded.yaml`.

`compose.preloaded.yaml` contains:

- an explicit local image tag;
- **no `build:` section**;
- `pull_policy: never`;
- the same ports, volumes, environment and healthcheck semantics as the primary Compose file.

This avoids the failure mode where loading an image is followed by Compose rebuilding or trying to pull it.

The fallback is documented under "Offline fallback / unusual Docker" in README and is separately tested before submission.

## 19.4 Schema is a ceiling, not an H0 target

The full F1 schema describes the **maximum defensible shape**. It is not a mandate to create every table before T1 works.

The build uses staged migrations.

### Migration 0001 — T1 vertical slice

Implement first:

- `users`
- `sessions`
- `events`
- `event_roles`
- `tracks`
- `teams`
- `team_memberships`
- `team_invites`
- `projects`
- `submissions`
- `seed_imports`

`schema_migrations` is infrastructure, not counted as a product table.

For the first T1 implementation:

- simple prize configuration may live as validated event JSON rather than requiring a dedicated `prizes` table immediately;
- duplicate candidate state may use explicit project fields such as `duplicate_status` / `duplicate_of_project_id` without merging records.

Exit condition: fixture imports, public gallery works, closed submission is rejected, auth/roles work, invite/team flow works, drafts persist.

### Migration 0002 — T2 judging core

Add:

- `judge_profiles`
- `judge_track_eligibility`
- `assignment_runs`
- `assignments`
- `rubric_versions`
- `ballot_versions`

To control table count in the initial T2 build:

- rubric criteria may be stored as validated canonical JSON on the immutable rubric version;
- raw criterion scores may be stored as canonical JSON on the immutable ballot version.

This is an intentional hackathon simplification, not permission to discard versioning.

`assignment_runs` stays in the core. It is not cut, because an infeasible run can legitimately create zero assignment rows and still must preserve its reason.

Exit condition: deterministic assignment, judge queue, weighted scoring, role isolation, progress, and raw ballot preservation work end to end.

### Migration 0003 — results and replay

Add:

- `result_runs`
- `result_entries`

For the minimum implementation:

- `result_runs` stores method/version/config, input digest, publication fields and `supersedes_result_run_id`;
- the exact ballot-version manifest used by a run is stored as canonical JSON and hashed;
- `result_entries` stores expected/completed/effective counts, arithmetic/explanation data and final rank.

This keeps replay structurally possible without forcing `result_run_ballots` and a separate publication-snapshot table into the first 30 hours.

Exit condition:

- results compute;
- publish is immutable;
- a correction creates a new run;
- "Explain this rank" is visible;
- an independent replay operation reproduces the stored value.

### Deferred normalization/ceiling tables

Do not create these merely because they exist in the full conceptual model:

- `prizes`
- `rubrics` parent table
- normalized `rubric_criteria`
- stable `ballots` parent table
- normalized `ballot_items`
- `result_run_ballots`
- separate `result_snapshots`
- `audit_events`
- `judge_conflicts`
- `duplicate_findings`
- `duplicate_finding_projects`

Activate a deferred table only if:

1. a required behavior cannot be defended without it, or
2. T1+T2+replay are already stable and the migration has a clear payoff.

The storage simplifications above remain behind service/API boundaries so later normalization does not require changing public routes.

## 19.5 Team-membership invariant is assumed, not contractual

The rule:

> one active team membership per user per event

is now marked **ASSUMED PRODUCT POLICY**, not an official rule.

At H0:

- re-read the authoritative spec/FAQ/Discord for an explicit multi-team rule;
- if official guidance permits or requires multi-team membership, remove this uniqueness rule before migration 0001 is written;
- if the contract remains silent, keep one active team per event as the documented product policy because it simplifies eligibility and ownership.

Do not claim this as a Dogfood requirement unless the organizer states it.

## 19.6 Test strategy — tied to the 42-case research pool

The workbook's 42 failure cases remain the adversarial test inventory. They are **not** all mandatory before T2.

### Unit / pure-logic tests

Run without a server where possible:

- F12 invalid/all-zero rubric weights
- F14 constant judge
- F15 n=1 / n=2 normalization fallback
- F16 easy-vs-hard batch limitation test
- F17 missing/partial review semantics
- F21 CSV escaping / spreadsheet-formula payload handling
- F39 claim-vs-verification parsing
- deterministic score/tie/replay arithmetic

### Database / transaction integration tests

Use a temporary SQLite database:

- F02 empty DB migration+seed
- F03 restart after edits
- F07 deadline during save
- F09 simultaneous final team-slot joins
- F10 expired/revoked invite
- F13 rubric version after ballots
- F18 infeasible assignment
- F19 repeated assignment request
- F20 dropout/reassignment
- migration checksum/drift detection
- seed idempotency

### Running-server authorization/HTTP tests

Use the real application:

- F04 peer ballot access
- F05 cross-event organizer isolation
- F06 cross-team project access
- F08 alternate write path deadline enforcement
- F11 draft leakage through gallery/export
- F21 CSV HTTP export
- F37 draft/media enumeration
- the seven official acceptance-suite cases

### Packaging / delivery / manual simulations

Require the packaged release candidate:

- F01 network-disabled startup
- F35 code changed after suite run
- F36 demo from reset state
- F40 suite/fixture version changed
- F41 video/build mismatch
- F42 independent clean-state runner

### Dormant-case rule

F22–F34 activate only if the corresponding T3/T4/bonus feature activates. Do not spend core-build time implementing tests for a dormant feature.

The existing failure-case IDs remain stable so evidence can be attached to the workbook later rather than inventing a second taxonomy.

## 19.7 Replay differentiator acceptance test

Replay counts as complete only when **both** exist:

### Visible product path

A judge/organizer can click:

**Explain this rank**

and inspect the exact inputs, fallbacks, arithmetic, tie policy and result-run identity.

### Independent replay path

The repository exposes one simple command created after kickoff, such as:

`make replay`

or an equivalent documented script/binary command.

It:

1. opens the persisted DB or an exported replay package;
2. reads a published result run;
3. recomputes the selected project/result independently from the stored immutable inputs;
4. compares the recomputed value/hash with the frozen result;
5. exits non-zero on mismatch.

The UI demonstrates the feature; the independent command makes it defensible.

## 19.8 Demo plan — frozen reference

The five-minute demo is not invented at H60. The build must preserve the existing shot sequence:

- 00:00–00:30 — provenance/startup
- 00:30–01:10 — organizer event/rubric configuration
- 01:10–01:55 — participant team/draft/edit/submit
- 01:55–02:40 — assignment + judge scoring
- 02:40–03:20 — denied peer/cross-scope access
- 03:20–04:10 — results, normalization, **Explain this rank**
- 04:10–05:00 — acceptance evidence, export, honest tier claim, handover

At H60, rehearse against a release candidate.

The final recorded video must identify the final source commit. If behavior/source changes afterward, rerun affected evidence and re-record affected scenes.

## 19.9 H0 `DELTA_LOG.md`

The repository gets `DELTA_LOG.md` immediately after kickoff.

Each delta entry records:

- observed UTC timestamp;
- source (spec page / main site / Discord pin / downloaded file);
- old assumption;
- new authoritative statement/hash;
- affected invariant/table/interface;
- action taken;
- whether the rest of F1 remained frozen.

This turns an H0 contract change into traceable engineering evidence instead of an undocumented rewrite.

## 19.10 Executable SQL is deliberately not pre-written

The official rules allow schema sketching before kickoff but prohibit project code before the 72-hour window.

Therefore this freeze intentionally stops at a **migration manifest and exact constraints**. It does **not** generate drop-in migration SQL before kickoff.

At H0, migration `0001_t1_core.sql` is written from the frozen manifest as the first implementation step. That preserves compliance while still eliminating schema-design uncertainty.

---

# 20. Final pre-kickoff closure

Pre-kickoff work is now closed except for:

- monitoring official rule/spec changes;
- team/availability logistics;
- retaining these planning artifacts.

No additional portal implementation, executable migration, Dockerfile, Compose file, binary, API handler, or application test is authored before kickoff.

**F1.1 status: FROZEN, subject only to H0 authoritative delta.**
