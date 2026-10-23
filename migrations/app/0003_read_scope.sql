-- The "mcp" scope becomes "read": one read-only key for the REST data endpoints and the MCP
-- server. Existing MCP keys keep working. SQLite cannot change a CHECK constraint, so the table
-- is rebuilt.
CREATE TABLE api_keys_new (id INTEGER PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL, prefix TEXT NOT NULL,
  scope TEXT NOT NULL CHECK (scope IN ('read','monitor','ingest')),
  key_hash BLOB UNIQUE NOT NULL, created_at INTEGER NOT NULL,
  last_used_at INTEGER, revoked_at INTEGER);
INSERT INTO api_keys_new SELECT id, user_id, name, prefix, CASE scope WHEN 'mcp' THEN 'read' ELSE scope END,
  key_hash, created_at, last_used_at, revoked_at FROM api_keys;
DROP TABLE api_keys;
ALTER TABLE api_keys_new RENAME TO api_keys;
CREATE INDEX api_keys_user ON api_keys(user_id);
