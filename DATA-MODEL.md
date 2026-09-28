# Data Model & Schema Dictionary — Dogfood 2026

Dogfood 2026 uses an auditable, relational SQLite schema designed for strict referential integrity, event isolation, and append-only versioning. All foreign key constraints are enforced at runtime via `PRAGMA foreign_keys = ON;`.

---

## 1. Schema Migration History

| Migration | File | Tables Defined |
| :--- | :--- | :--- |
| **0001** | `0001_t1_core.sql` | `events`, `tracks`, `users`, `event_roles`, `sessions`, `teams`, `team_memberships`, `team_invites`, `projects`, `submissions` |
| **0002** | `0002_t2_judging.sql` | `judge_profiles`, `judge_track_eligibility`, `assignment_runs`, `rubric_versions`, `assignments`, `ballot_versions` |
| **0003** | `0003_results_replay.sql`| `result_runs`, `result_entries` |
| **0010** | `0010_t1_prizes.sql` | `prizes` |

---

## 2. Table Dictionary

### 1. `events`
Root event metadata and lifecycle timestamp boundaries.
* `id` (`TEXT PRIMARY KEY`): Unique event identifier (e.g. `evt_01`).
* `name` (`TEXT NOT NULL`): Human-readable event name.
* `registration_opens_at` (`TEXT`): ISO 8601 UTC registration opening.
* `registration_closes_at` (`TEXT`): ISO 8601 UTC registration closing.
* `submissions_open_at` (`TEXT`): ISO 8601 UTC submissions opening.
* `submissions_close_at` (`TEXT`): ISO 8601 UTC submissions deadline.
* `judging_opens_at` (`TEXT`): ISO 8601 UTC judging opening.
* `judging_closes_at` (`TEXT`): ISO 8601 UTC judging closing.
* `created_at` (`TEXT NOT NULL`): Creation timestamp.

### 2. `tracks`
Competition categories within an event.
* `id` (`TEXT PRIMARY KEY`): Unique track ID (e.g. `trk_01`).
* `event_id` (`TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE`): Associated event.
* `name` (`TEXT NOT NULL`): Track display title.
* `description` (`TEXT`): Track prompt or guidance.
* `created_at` (`TEXT NOT NULL`): Creation timestamp.

### 3. `prizes`
Awards and prizes configured for the event or specific tracks.
* `id` (`TEXT PRIMARY KEY`): Unique prize ID.
* `event_id` (`TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE`): Associated event.
* `track_id` (`TEXT REFERENCES tracks(id) ON DELETE SET NULL`): Optional track restriction (null for all tracks).
* `name` (`TEXT NOT NULL`): Prize title (e.g. `Grand Prize`).
* `description` (`TEXT`): Prize eligibility criteria.
* `amount_text` (`TEXT`): Formatted prize value (e.g. `$10,000 USD`).
* `created_at` (`TEXT NOT NULL`): Creation timestamp.

### 4. `users`
Global user identities.
* `id` (`TEXT PRIMARY KEY`): Unique user identifier (e.g. `usr_organizer`).
* `email_normalized` (`TEXT NOT NULL UNIQUE`): Lowercase trimmed email address.
* `display_name` (`TEXT NOT NULL`): Display name.
* `created_at` (`TEXT NOT NULL`): Account creation timestamp.
* `disabled_at` (`TEXT`): Optional account deactivation timestamp.

### 5. `event_roles`
User role assignments scoped to an event.
* `event_id` (`TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE`): Scoped event.
* `user_id` (`TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE`): Target user.
* `role` (`TEXT NOT NULL`): Role key (`admin`, `organizer`, `judge`, `participant`).
* `granted_at` (`TEXT NOT NULL`): Grant timestamp.
* `revoked_at` (`TEXT`): Revocation timestamp.
* `PRIMARY KEY (event_id, user_id, role)`

### 6. `sessions`
Authentication sessions for browser cookies and API tokens.
* `id` (`TEXT PRIMARY KEY`): Unique session ID.
* `user_id` (`TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE`): Authenticated user.
* `token_hash` (`TEXT NOT NULL UNIQUE`): SHA-256 hex hash of the bearer/cookie token.
* `created_at` (`TEXT NOT NULL`): Session start.
* `expires_at` (`TEXT NOT NULL`): Expiration timestamp.
* `last_seen_at` (`TEXT NOT NULL`): Last request activity.
* `revoked_at` (`TEXT`): Explicit logout timestamp.

### 7. `teams`
Hackathon project teams.
* `id` (`TEXT PRIMARY KEY`): Unique team ID.
* `event_id` (`TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE`): Event scope.
* `name` (`TEXT NOT NULL`): Team name.
* `created_at` (`TEXT NOT NULL`): Creation timestamp.

### 8. `team_memberships`
Historical and active team membership records.
* `id` (`TEXT PRIMARY KEY`): Membership identifier.
* `event_id` (`TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE`): Event scope.
* `team_id` (`TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE`): Associated team.
* `user_id` (`TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE`): Member user.
* `membership_role` (`TEXT NOT NULL DEFAULT 'member'`): `lead` or `member`.
* `joined_at` (`TEXT NOT NULL`): Join timestamp.
* `left_at` (`TEXT`): Leave timestamp (null if currently active).

### 9. `team_invites`
Cryptographic invite tokens for team onboarding.
* `id` (`TEXT PRIMARY KEY`): Unique invite ID.
* `event_id` (`TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE`): Event scope.
* `team_id` (`TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE`): Inviting team.
* `token_hash` (`TEXT NOT NULL UNIQUE`): SHA-256 hash of the invite token.
* `email_normalized` (`TEXT`): Optional email restriction.
* `created_by` (`TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE`): Inviter user.
* `created_at` (`TEXT NOT NULL`): Creation timestamp.
* `expires_at` (`TEXT NOT NULL`): Expiration timestamp.
* `accepted_at` (`TEXT`): Acceptance timestamp.
* `accepted_by` (`TEXT REFERENCES users(id) ON DELETE SET NULL`): Accepting user.
* `revoked_at` (`TEXT`): Revocation timestamp.

### 10. `projects`
Stable project identities created by teams.
* `id` (`TEXT PRIMARY KEY`): Unique project ID.
* `event_id` (`TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE`): Event scope.
* `team_id` (`TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE`): Owning team.
* `track_id` (`TEXT NOT NULL REFERENCES tracks(id) ON DELETE RESTRICT`): Track selection.
* `created_by` (`TEXT REFERENCES users(id) ON DELETE SET NULL`): Author user.
* `eligibility_status` (`TEXT NOT NULL DEFAULT 'ELIGIBLE'`): Disqualification status.
* `created_at` (`TEXT NOT NULL`): Creation timestamp.

### 11. `submissions`
Append-only versioned project submission content.
* `id` (`TEXT PRIMARY KEY`): Submission revision ID.
* `event_id` (`TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE`): Event scope.
* `project_id` (`TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE`): Associated project.
* `version_no` (`INTEGER NOT NULL`): Monotonically increasing revision number.
* `state` (`TEXT NOT NULL DEFAULT 'DRAFT'`): `DRAFT` or `SUBMITTED`.
* `title` (`TEXT NOT NULL`): Project title.
* `summary` (`TEXT NOT NULL DEFAULT ''`): Project description.
* `repo_url` (`TEXT NOT NULL DEFAULT ''`): Code repository URL.
* `demo_url` (`TEXT`): Live demo or video link.
* `created_by` (`TEXT REFERENCES users(id) ON DELETE SET NULL`): Submitting user.
* `created_at` (`TEXT NOT NULL`): Creation timestamp.
* `submitted_at` (`TEXT`): Timestamp when finalized.
* `CONSTRAINT uq_submissions_project_version UNIQUE (project_id, version_no)`

### 12. `judge_profiles`
Judge capacity and status per event.
* `event_id` (`TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE`): Event scope.
* `user_id` (`TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE`): Judge user.
* `capacity` (`INTEGER NOT NULL DEFAULT 0`): Maximum project reviews.
* `active` (`INTEGER NOT NULL DEFAULT 1`): Active review status flag (1=active, 0=inactive).
* `invited_at` (`TEXT NOT NULL`): Invitation timestamp.
* `accepted_at` (`TEXT`): Acceptance timestamp.
* `deactivated_at` (`TEXT`): Deactivation timestamp.
* `PRIMARY KEY (event_id, user_id)`

### 13. `judge_track_eligibility`
Explicit bipartite graph of judge track eligibility.
* `event_id` (`TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE`): Event scope.
* `judge_user_id` (`TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE`): Judge user.
* `track_id` (`TEXT NOT NULL REFERENCES tracks(id) ON DELETE CASCADE`): Track ID.
* `eligible` (`INTEGER NOT NULL DEFAULT 1`): Eligibility flag (1=eligible, 0=ineligible).
* `source` (`TEXT NOT NULL DEFAULT 'rule'`): Eligibility source (`rule`, `organizer`).
* `reason` (`TEXT`): Audit reason.
* `updated_at` (`TEXT NOT NULL`): Last update timestamp.
* `PRIMARY KEY (event_id, judge_user_id, track_id)`

### 14. `assignment_runs`
Execution logs of the deterministic matching engine.
* `id` (`TEXT PRIMARY KEY`): Run ID.
* `event_id` (`TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE`): Event scope.
* `algorithm_key` (`TEXT NOT NULL`): Algorithm identifier (`deterministic_matching`).
* `algorithm_version` (`TEXT NOT NULL`): Algorithm version (`1.0.0`).
* `config_json` (`TEXT NOT NULL DEFAULT '{}'`): Target reviews and seed parameters.
* `requested_reviews_total` (`INTEGER NOT NULL DEFAULT 0`): Review target total.
* `status` (`TEXT NOT NULL`): `SUCCESS`, `INFEASIBLE`, or `FAILED`.
* `created_by` (`TEXT REFERENCES users(id) ON DELETE SET NULL`): Triggering organizer.
* `created_at` (`TEXT NOT NULL`): Execution start.
* `finished_at` (`TEXT`): Execution completion.
* `infeasibility_code` (`TEXT`): Detailed code if matching was mathematically infeasible.

### 15. `rubric_versions`
Logical versioned scoring rubrics with cryptographic hash.
* `id` (`TEXT PRIMARY KEY`): Unique rubric version identifier (e.g. `rub_evt_01_default_v1`).
* `event_id` (`TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE`): Event scope.
* `rubric_id` (`TEXT NOT NULL DEFAULT 'default'`): Logical rubric name.
* `track_id` (`TEXT REFERENCES tracks(id) ON DELETE SET NULL`): Optional track scope.
* `version_no` (`INTEGER NOT NULL DEFAULT 1`): Revision number.
* `state` (`TEXT NOT NULL DEFAULT 'DRAFT'`): `DRAFT`, `PUBLISHED`, or `RETIRED`.
* `criteria_json` (`TEXT NOT NULL DEFAULT '[]'`): Canonical JSON array of criteria and weights.
* `configuration_hash` (`TEXT NOT NULL DEFAULT ''`): SHA-256 hash of criteria JSON.
* `created_by` (`TEXT REFERENCES users(id) ON DELETE SET NULL`): Author organizer.
* `created_at` (`TEXT NOT NULL`): Creation timestamp.
* `published_at` (`TEXT`): Publication timestamp.
* `CONSTRAINT uq_rubric_versions_scope UNIQUE (event_id, rubric_id, version_no)`

### 16. `assignments`
Individual judge-to-project evaluation assignments.
* `id` (`TEXT PRIMARY KEY`): Unique assignment ID.
* `event_id` (`TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE`): Event scope.
* `assignment_run_id` (`TEXT REFERENCES assignment_runs(id) ON DELETE SET NULL`): Originating run.
* `judge_user_id` (`TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE`): Assigned judge.
* `project_id` (`TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE`): Target project.
* `rubric_version_id` (`TEXT NOT NULL REFERENCES rubric_versions(id) ON DELETE RESTRICT`): Bound rubric version.
* `status` (`TEXT NOT NULL DEFAULT 'ASSIGNED'`): `ASSIGNED`, `STARTED`, `COMPLETED`, `CANCELLED`.
* `assigned_at` (`TEXT NOT NULL`): Assignment timestamp.
* `started_at` (`TEXT`): First draft ballot timestamp.
* `completed_at` (`TEXT`): Final ballot submission timestamp.

### 17. `ballot_versions`
Append-only evaluation scoring revisions.
* `id` (`TEXT PRIMARY KEY`): Ballot revision ID.
* `assignment_id` (`TEXT NOT NULL REFERENCES assignments(id) ON DELETE CASCADE`): Parent assignment.
* `judge_user_id` (`TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE`): Judge user.
* `project_id` (`TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE`): Evaluated project.
* `rubric_version_id` (`TEXT NOT NULL REFERENCES rubric_versions(id) ON DELETE RESTRICT`): Applied rubric.
* `version_no` (`INTEGER NOT NULL`): Monotonic revision count.
* `save_kind` (`TEXT NOT NULL`): `DRAFT` or `SUBMISSION`.
* `scores_json` (`TEXT NOT NULL DEFAULT '{}'`): JSON object containing criteria scores, total score, and weighted score.
* `comment` (`TEXT NOT NULL DEFAULT ''`): Judge feedback.
* `created_by` (`TEXT REFERENCES users(id) ON DELETE SET NULL`): Author judge.
* `created_at` (`TEXT NOT NULL`): Timestamp.
* `CONSTRAINT uq_ballot_versions_assignment_version UNIQUE (assignment_id, version_no)`

### 18. `result_runs`
Immutable official scoring runs.
* `id` (`TEXT PRIMARY KEY`): Unique run ID.
* `event_id` (`TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE`): Event scope.
* `algorithm_key` (`TEXT NOT NULL`): Aggregation key (`standard_zscore_v1`).
* `algorithm_version` (`TEXT NOT NULL`): Algorithm version (`1.0.0`).
* `status` (`TEXT NOT NULL`): `DRAFT`, `PUBLISHED`, `RETIRED`.
* `input_digest` (`TEXT NOT NULL`): SHA-256 digest of the canonical source ballot manifest.
* `created_by` (`TEXT REFERENCES users(id) ON DELETE SET NULL`): Triggering organizer.
* `created_at` (`TEXT NOT NULL`): Computation timestamp.
* `published_at` (`TEXT`): Official publication timestamp.

### 19. `result_entries`
Itemized project standings produced by a result run.
* `result_run_id` (`TEXT NOT NULL REFERENCES result_runs(id) ON DELETE CASCADE`): Parent run.
* `project_id` (`TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE`): Evaluated project.
* `raw_score` (`REAL NOT NULL`): Arithmetic mean of raw judge scores.
* `normalized_score` (`REAL NOT NULL`): Normalized aggregate score.
* `final_score` (`REAL NOT NULL`): Official score used for ranking.
* `rank` (`INTEGER NOT NULL`): Official ordinal rank.
* `tie_group` (`INTEGER NOT NULL`): Group identifier for projects with identical scores.
* `expected_reviews` (`INTEGER NOT NULL DEFAULT 0`): Target reviews for the project.
* `completed_reviews` (`INTEGER NOT NULL DEFAULT 0`): Submitted ballots count.
* `effective_reviews` (`INTEGER NOT NULL DEFAULT 0`): Reviews incorporated into score.
* `fallback_count` (`INTEGER NOT NULL DEFAULT 0`): Count of ballots where normalization fell back.
* `explanation_json` (`TEXT NOT NULL DEFAULT '{}'`): Transparent mathematical receipt.
* `PRIMARY KEY (result_run_id, project_id)`
