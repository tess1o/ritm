-- accounts; each user's health data lives in data/users/<db_name>.db (a random UUID, never reused)
CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT NOT NULL UNIQUE COLLATE NOCASE,
  password_hash TEXT NOT NULL, db_name TEXT NOT NULL UNIQUE, created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL, suspended_at INTEGER);
CREATE TABLE sessions (token_hash BLOB PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at INTEGER NOT NULL, created_at INTEGER NOT NULL);
CREATE INDEX sessions_user ON sessions(user_id);
CREATE TABLE api_keys (id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL, prefix TEXT NOT NULL,
  scope TEXT NOT NULL CHECK (scope IN ('mcp','monitor')),
  key_hash BLOB UNIQUE NOT NULL, created_at INTEGER NOT NULL,
  last_used_at INTEGER, revoked_at INTEGER);
CREATE INDEX api_keys_user ON api_keys(user_id);
