-- 0001_t1_core.sql: T1 core database schema
-- Strictly frozen entities: users, sessions, events, event_roles, tracks, teams, team_memberships, team_invites, projects, submissions, seed_imports

-- 1. users: Global identity
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    email_normalized TEXT NOT NULL,
    display_name TEXT NOT NULL,
    password_hash TEXT NOT NULL DEFAULT '',
    password_params TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    disabled_at TEXT
);
CREATE UNIQUE INDEX idx_users_email_normalized ON users(email_normalized);

-- 2. sessions: Session lifecycle
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    revoked_at TEXT,
    last_seen_at TEXT NOT NULL
);
CREATE INDEX idx_sessions_token_hash ON sessions(token_hash);
CREATE INDEX idx_sessions_user_id ON sessions(user_id);

-- 3. events: Event configuration and independent lifecycle windows
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    registration_opens_at TEXT,
    registration_closes_at TEXT,
    submissions_open_at TEXT,
    submissions_close_at TEXT,
    judging_opens_at TEXT,
    judging_closes_at TEXT,
    voting_opens_at TEXT,
    voting_closes_at TEXT,
    default_expected_reviews INTEGER NOT NULL DEFAULT 3,
    archived_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CONSTRAINT chk_events_registration_window CHECK (registration_opens_at IS NULL OR registration_closes_at IS NULL OR registration_opens_at <= registration_closes_at),
    CONSTRAINT chk_events_submissions_window CHECK (submissions_open_at IS NULL OR submissions_close_at IS NULL OR submissions_open_at <= submissions_close_at),
    CONSTRAINT chk_events_judging_window CHECK (judging_opens_at IS NULL OR judging_closes_at IS NULL OR judging_opens_at <= judging_closes_at)
);

-- 4. event_roles: Event-scoped authorization
CREATE TABLE event_roles (
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    granted_at TEXT NOT NULL,
    granted_by TEXT,
    revoked_at TEXT,
    PRIMARY KEY (event_id, user_id, role)
);
CREATE INDEX idx_event_roles_user_role ON event_roles(user_id, role);

-- 5. tracks: Event tracks
CREATE TABLE tracks (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    expected_reviews_override INTEGER,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_tracks_event ON tracks(event_id);

-- 6. teams: Team identity
CREATE TABLE teams (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    created_by TEXT,
    created_at TEXT NOT NULL,
    locked_at TEXT
);
CREATE INDEX idx_teams_event ON teams(event_id);

-- 7. team_memberships: Team membership history
CREATE TABLE team_memberships (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    team_id TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    membership_role TEXT NOT NULL DEFAULT 'member',
    joined_at TEXT NOT NULL,
    left_at TEXT
);
CREATE INDEX idx_team_memberships_event_user ON team_memberships(event_id, user_id);
CREATE INDEX idx_team_memberships_team ON team_memberships(team_id);

-- 8. team_invites: Team invitation lifecycle
CREATE TABLE team_invites (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    team_id TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    email_normalized TEXT,
    created_by TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    accepted_at TEXT,
    accepted_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    revoked_at TEXT
);
CREATE INDEX idx_team_invites_team ON team_invites(team_id);
CREATE INDEX idx_team_invites_token_hash ON team_invites(token_hash);

-- 9. projects: Stable project identity
CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    team_id TEXT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    track_id TEXT NOT NULL REFERENCES tracks(id) ON DELETE RESTRICT,
    created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    eligibility_status TEXT NOT NULL DEFAULT 'ELIGIBLE',
    duplicate_status TEXT NOT NULL DEFAULT 'NONE',
    duplicate_of_project_id TEXT REFERENCES projects(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_projects_event_track ON projects(event_id, track_id);
CREATE INDEX idx_projects_team ON projects(team_id);

-- 10. submissions: Immutable submitted/draft content versions
CREATE TABLE submissions (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    version_no INTEGER NOT NULL,
    state TEXT NOT NULL DEFAULT 'DRAFT',
    title TEXT NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    repo_url TEXT NOT NULL DEFAULT '',
    demo_url TEXT,
    created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL,
    submitted_at TEXT,
    supersedes_submission_id TEXT REFERENCES submissions(id) ON DELETE SET NULL,
    CONSTRAINT uq_submissions_project_version UNIQUE (project_id, version_no)
);
CREATE INDEX idx_submissions_project_state ON submissions(project_id, state);
CREATE INDEX idx_submissions_event ON submissions(event_id);

-- 11. seed_imports: Seeding idempotency and provenance log
CREATE TABLE seed_imports (
    id TEXT PRIMARY KEY,
    source_name TEXT NOT NULL,
    source_hash_sha256 TEXT NOT NULL,
    event_id TEXT REFERENCES events(id) ON DELETE CASCADE,
    status TEXT NOT NULL,
    started_at TEXT NOT NULL,
    completed_at TEXT,
    error_message TEXT,
    CONSTRAINT uq_seed_imports_source UNIQUE (source_name, source_hash_sha256)
);
