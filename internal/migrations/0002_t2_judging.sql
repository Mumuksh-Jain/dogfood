-- 0002_t2_judging.sql: T2 judging schema
-- Tables: judge_profiles, judge_track_eligibility, assignment_runs, assignments, rubric_versions, ballot_versions

-- 1. judge_profiles: Event-specific judging capacity and lifecycle status
CREATE TABLE judge_profiles (
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    capacity INTEGER NOT NULL DEFAULT 0,
    active INTEGER NOT NULL DEFAULT 1,
    invited_at TEXT NOT NULL,
    accepted_at TEXT,
    deactivated_at TEXT,
    PRIMARY KEY (event_id, user_id),
    CONSTRAINT chk_judge_profiles_capacity CHECK (capacity >= 0)
);
CREATE INDEX idx_judge_profiles_user ON judge_profiles(user_id);

-- 2. judge_track_eligibility: Explicit judge-track eligibility graph for assignment
CREATE TABLE judge_track_eligibility (
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    judge_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    track_id TEXT NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
    eligible INTEGER NOT NULL DEFAULT 1,
    source TEXT NOT NULL DEFAULT 'rule',
    reason TEXT,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (event_id, judge_user_id, track_id)
);
CREATE INDEX idx_judge_track_eligibility_judge ON judge_track_eligibility(judge_user_id);
CREATE INDEX idx_judge_track_eligibility_track ON judge_track_eligibility(track_id);

-- 3. assignment_runs: Auditable assignment generation runs (including structured infeasibility)
CREATE TABLE assignment_runs (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    algorithm_key TEXT NOT NULL,
    algorithm_version TEXT NOT NULL,
    config_json TEXT NOT NULL DEFAULT '{}',
    requested_reviews_total INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL,
    finished_at TEXT,
    infeasibility_code TEXT,
    infeasibility_details_json TEXT
);
CREATE INDEX idx_assignment_runs_event_status ON assignment_runs(event_id, status);

-- 4. rubric_versions: Logical rubric versions with immutable canonical criteria
CREATE TABLE rubric_versions (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    rubric_id TEXT NOT NULL DEFAULT 'default',
    track_id TEXT REFERENCES tracks(id) ON DELETE SET NULL,
    version_no INTEGER NOT NULL DEFAULT 1,
    state TEXT NOT NULL DEFAULT 'DRAFT',
    criteria_json TEXT NOT NULL DEFAULT '[]',
    configuration_hash TEXT NOT NULL DEFAULT '',
    created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL,
    published_at TEXT,
    CONSTRAINT uq_rubric_versions_scope UNIQUE (event_id, rubric_id, version_no)
);
CREATE INDEX idx_rubric_versions_event_track ON rubric_versions(event_id, track_id);
CREATE INDEX idx_rubric_versions_event_rubric ON rubric_versions(event_id, rubric_id);

-- 5. assignments: Judge-to-project allocation and reassignment lifecycle
CREATE TABLE assignments (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    assignment_run_id TEXT REFERENCES assignment_runs(id) ON DELETE SET NULL,
    judge_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    rubric_version_id TEXT NOT NULL REFERENCES rubric_versions(id) ON DELETE RESTRICT,
    status TEXT NOT NULL DEFAULT 'ASSIGNED',
    assigned_at TEXT NOT NULL,
    started_at TEXT,
    completed_at TEXT,
    supersedes_assignment_id TEXT REFERENCES assignments(id) ON DELETE SET NULL,
    reason TEXT
);
CREATE INDEX idx_assignments_event_judge ON assignments(event_id, judge_user_id);
CREATE INDEX idx_assignments_project ON assignments(project_id);
CREATE INDEX idx_assignments_run ON assignments(assignment_run_id);
CREATE UNIQUE INDEX idx_assignments_active_judge_project ON assignments(event_id, judge_user_id, project_id) WHERE status NOT IN ('CANCELLED', 'REASSIGNED');

-- 6. ballot_versions: Append-only revision history of scores and evaluations
CREATE TABLE ballot_versions (
    id TEXT PRIMARY KEY,
    assignment_id TEXT NOT NULL REFERENCES assignments(id) ON DELETE CASCADE,
    judge_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    rubric_version_id TEXT NOT NULL REFERENCES rubric_versions(id) ON DELETE RESTRICT,
    version_no INTEGER NOT NULL,
    save_kind TEXT NOT NULL,
    scores_json TEXT NOT NULL DEFAULT '{}',
    comment TEXT NOT NULL DEFAULT '',
    created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL,
    supersedes_version_id TEXT REFERENCES ballot_versions(id) ON DELETE SET NULL,
    CONSTRAINT uq_ballot_versions_assignment_version UNIQUE (assignment_id, version_no)
);
CREATE INDEX idx_ballot_versions_assignment ON ballot_versions(assignment_id, version_no DESC);
CREATE INDEX idx_ballot_versions_judge_project ON ballot_versions(judge_user_id, project_id);
CREATE INDEX idx_ballot_versions_project ON ballot_versions(project_id);
