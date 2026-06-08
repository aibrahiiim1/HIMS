-- Re-add the restrictive status CHECK (NOT VALID so existing vendor-label rows
-- don't block the down-migration; only new writes are constrained).
ALTER TABLE access_points
  ADD CONSTRAINT access_points_status_check
  CHECK (status = ANY (ARRAY['online'::text, 'offline'::text, 'unknown'::text])) NOT VALID;
