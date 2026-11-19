-- Dockerfile generated/edited in LPS for projects without their own; '' = use the project's Dockerfile.
ALTER TABLE apps ADD COLUMN dockerfile TEXT NOT NULL DEFAULT '';
