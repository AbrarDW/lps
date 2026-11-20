-- Apps whose code comes from a git repository (source_kind 'git').
ALTER TABLE apps ADD COLUMN git_url TEXT NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN git_branch TEXT NOT NULL DEFAULT '';
-- Access token for private repositories; never returned by the API.
ALTER TABLE apps ADD COLUMN git_token TEXT NOT NULL DEFAULT '';
-- 1 = deploy new commits on the branch automatically.
ALTER TABLE apps ADD COLUMN auto_deploy INTEGER NOT NULL DEFAULT 0;
-- The commit the running version was built from.
ALTER TABLE apps ADD COLUMN last_commit TEXT NOT NULL DEFAULT '';

ALTER TABLE deployments ADD COLUMN commit_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE deployments ADD COLUMN commit_message TEXT NOT NULL DEFAULT '';
