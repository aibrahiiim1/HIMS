-- Per-channel recording state + analog resolution for recorder channels.
-- recording: NULL = not reported, true/false = whether the channel is being recorded
-- (from ISAPI /ContentMgmt/record/tracks per-channel Enable).
-- resolution: analog input signal descriptor (resDesc, e.g. "1080P25"); "" = no signal.
ALTER TABLE nvr_channels ADD COLUMN IF NOT EXISTS recording boolean;
ALTER TABLE nvr_channels ADD COLUMN IF NOT EXISTS resolution text NOT NULL DEFAULT '';
