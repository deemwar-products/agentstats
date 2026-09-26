-- agentstats D1 — the real leaderboard. Public by default (opt-out): every
-- user with stats is ranked unless they switch this off in Settings. Private
-- profiles (is_public=0) are never ranked either, whatever this says.
ALTER TABLE users ADD COLUMN leaderboard INTEGER NOT NULL DEFAULT 1;
