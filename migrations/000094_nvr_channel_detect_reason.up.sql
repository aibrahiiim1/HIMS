-- NVR/DVR camera channels: capture WHY a channel is offline, not just that it is.
-- Hikvision exposes chanDetectResult per channel (connect | netUnreachable |
-- errorUserNameOrPasswd | …). detect_reason stores the operator-facing reason
-- ("network unreachable", "credential error", …) so the system is aware of each
-- camera's real issue, not just an online/offline flag.
ALTER TABLE nvr_channels ADD COLUMN IF NOT EXISTS detect_reason text NOT NULL DEFAULT '';
