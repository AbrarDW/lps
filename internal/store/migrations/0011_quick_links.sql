-- 1 = the app has a Cloudflare quick link (public https://<random>.trycloudflare.com).
ALTER TABLE apps ADD COLUMN quick_link INTEGER NOT NULL DEFAULT 0;
