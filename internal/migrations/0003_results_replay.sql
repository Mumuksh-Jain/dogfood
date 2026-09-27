-- 0003_results_replay.sql: Tier 2 Results, Normalization, and Explain-This-Rank Auditability Schema
-- Tables: result_runs, result_entries

-- 1. result_runs: Auditable results computation and publication runs
CREATE TABLE result_runs (
    id TEXT PRIMARY KEY,
    event_id TEXT NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    supersedes_result_run_id TEXT REFERENCES result_runs(id) ON DELETE SET NULL,
    algorithm_key TEXT NOT NULL,
    algorithm_version TEXT NOT NULL,
    config_json TEXT NOT NULL DEFAULT '{}',
    cohort_policy TEXT NOT NULL DEFAULT 'all_completed',
    tie_policy TEXT NOT NULL DEFAULT 'deterministic_fallback',
    status TEXT NOT NULL DEFAULT 'DRAFT',
    input_manifest_json TEXT NOT NULL DEFAULT '{}',
    input_digest TEXT NOT NULL DEFAULT '',
    created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL,
    published_at TEXT,
    failure_reason TEXT
);
CREATE INDEX idx_result_runs_event_status ON result_runs(event_id, status);

-- 2. result_entries: Per-project defensible scoring records and transparent derivations
CREATE TABLE result_entries (
    result_run_id TEXT NOT NULL REFERENCES result_runs(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    raw_score REAL NOT NULL,
    normalized_score REAL NOT NULL,
    final_score REAL NOT NULL,
    rank INTEGER NOT NULL,
    tie_group INTEGER NOT NULL,
    expected_reviews INTEGER NOT NULL DEFAULT 0,
    completed_reviews INTEGER NOT NULL DEFAULT 0,
    effective_reviews INTEGER NOT NULL DEFAULT 0,
    fallback_count INTEGER NOT NULL DEFAULT 0,
    explanation_json TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY (result_run_id, project_id)
);
CREATE INDEX idx_result_entries_run_rank ON result_entries(result_run_id, rank ASC);
CREATE INDEX idx_result_entries_project ON result_entries(project_id);
