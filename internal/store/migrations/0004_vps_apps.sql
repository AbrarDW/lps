-- Apps can run inside an LPS VPS instead of their own container.
-- vps_id = 0: the app is built into its own container (the original mode).
ALTER TABLE apps ADD COLUMN vps_id INTEGER NOT NULL DEFAULT 0;
-- 'pc': code is copied from source_path on this PC; 'vps': code already lives at vps_path in the VPS.
ALTER TABLE apps ADD COLUMN source_kind TEXT NOT NULL DEFAULT 'pc';
ALTER TABLE apps ADD COLUMN vps_path TEXT NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN packages TEXT NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN install_cmd TEXT NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN start_cmd TEXT NOT NULL DEFAULT '';
-- 0 after the user stops a VPS app, so LPS does not restart it.
ALTER TABLE apps ADD COLUMN want_running INTEGER NOT NULL DEFAULT 1;
