ALTER TABLE them.middleware_defs
  ADD COLUMN IF NOT EXISTS emoji     TEXT,
  ADD COLUMN IF NOT EXISTS color     TEXT,
  ADD COLUMN IF NOT EXISTS bg_color  TEXT;

UPDATE them.middleware_defs
SET emoji = '🛡️', color = '#f59e0b', bg_color = 'rgba(245,158,11,0.08)'
WHERE slug = 'file-guard';
