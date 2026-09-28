-- 0004_t3_t4.sql: Tier 3 (Community Voting & Comments) and Tier 4 (Webhooks & Verifiable Records)
-- Standalone, forward-safe schema extensions preserving all T1/T2 invariant integrity

-- 1. Community Voting
CREATE TABLE IF NOT EXISTS project_votes (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    UNIQUE(event_id, project_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_project_votes_proj ON project_votes(project_id);
CREATE INDEX IF NOT EXISTS idx_project_votes_user ON project_votes(user_id);
CREATE INDEX IF NOT EXISTS idx_project_votes_event ON project_votes(event_id);

-- 2. Project Comments
CREATE TABLE IF NOT EXISTS project_comments (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    author_name TEXT NOT NULL,
    content TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_project_comments_proj ON project_comments(project_id, created_at);
CREATE INDEX IF NOT EXISTS idx_project_comments_user ON project_comments(user_id);

-- 3. Anti-Cheat & Rate Limit Audit Trail
CREATE TABLE IF NOT EXISTS vote_audit_events (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    action TEXT NOT NULL,
    ip_address TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_vote_audit_proj ON vote_audit_events(project_id);
CREATE INDEX IF NOT EXISTS idx_vote_audit_user ON vote_audit_events(user_id);

-- 4. Webhook Subscriptions (T4)
CREATE TABLE IF NOT EXISTS webhook_subscriptions (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    target_url TEXT NOT NULL,
    event_type TEXT NOT NULL,
    secret TEXT NOT NULL DEFAULT '',
    is_active INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_webhook_subs_event ON webhook_subscriptions(event_id);

-- 5. Community Settings (T3 hidden results / voting window toggle)
CREATE TABLE IF NOT EXISTS event_community_settings (
    event_id TEXT PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    voting_open INTEGER NOT NULL DEFAULT 1,
    results_hidden INTEGER NOT NULL DEFAULT 1,
    updated_at TEXT NOT NULL
);
