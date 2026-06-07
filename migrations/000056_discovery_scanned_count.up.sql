-- Per-host scan progress: how many of host_count targets have been processed so
-- far. Lets the UI show a 0→100% progress bar for a running scan (scanned_count /
-- host_count). Set to host_count at completion so a finished job reads 100%.
ALTER TABLE discovery_jobs ADD COLUMN IF NOT EXISTS scanned_count INTEGER NOT NULL DEFAULT 0;
