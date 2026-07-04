CREATE TABLE tasks (
  id              TEXT PRIMARY KEY,          -- uuid
  name            TEXT NOT NULL,             -- human label (defaults to short id)
  provider        TEXT NOT NULL,             -- 'claude' | 'copilot' | 'fake'
  mode            TEXT NOT NULL,             -- 'headless' | 'interactive'
  cwd             TEXT NOT NULL,             -- working directory for the provider
  session_mode    TEXT NOT NULL,             -- 'new' | 'existing'
  session_ref     TEXT,                      -- provider session id (nullable until known)
  prompt          TEXT,                      -- initial instruction (nullable if passthrough only)
  args_json       TEXT NOT NULL DEFAULT '[]',-- extra passthrough argv
  config_json     TEXT NOT NULL,             -- resolved policy snapshot at create time
  status          TEXT NOT NULL,             -- PENDING|RUNNING|WAITING|PAUSED|SUCCEEDED|FAILED|STOPPED
  current_attempt INTEGER NOT NULL DEFAULT 0,
  total_attempts  INTEGER NOT NULL DEFAULT 0,
  next_wake_at    TEXT,                      -- when WAITING
  last_outcome    TEXT,                      -- last Attempt Outcome
  last_reset_at   TEXT,                      -- last detected Reset Time
  cumulative_wait_ms INTEGER NOT NULL DEFAULT 0,
  deadline_at     TEXT,                      -- absolute cap (nullable = no deadline)
  exit_reason     TEXT,                      -- set on terminal state
  created_at      TEXT NOT NULL,
  updated_at      TEXT NOT NULL
);

CREATE TABLE attempts (
  id           TEXT PRIMARY KEY,             -- uuid
  task_id      TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  attempt_no   INTEGER NOT NULL,
  pid          INTEGER,
  started_at   TEXT NOT NULL,
  ended_at     TEXT,
  exit_code    INTEGER,
  outcome      TEXT,                         -- Outcome Category
  reset_at     TEXT,                         -- Reset Time if detected
  matched_rule TEXT,                         -- name of the detection rule that fired
  stdout_path  TEXT NOT NULL,                -- file under the Task's attempts dir
  stderr_path  TEXT NOT NULL,
  UNIQUE(task_id, attempt_no)
);

CREATE TABLE events (
  id       INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id  TEXT REFERENCES tasks(id) ON DELETE CASCADE,
  ts       TEXT NOT NULL,
  type     TEXT NOT NULL,                    -- state_change|attempt_start|attempt_exit|scheduled|user_action
  detail_json TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE daemon_meta (
  key   TEXT PRIMARY KEY,                    -- schema_version|pid|started_at|version
  value TEXT NOT NULL
);
