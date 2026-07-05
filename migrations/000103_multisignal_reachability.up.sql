-- Multi-signal, evidence-based reachability. A reachability check now probes a SET of
-- candidate TCP ports (the device's discovered open ports) and is UP if ANY of them
-- answers — so a single dead service can never flip a live device to "offline" (the
-- false-unreachable bug: a POS/printer/SIP host answering tcp/5060 while its old
-- monitored port died). The winning signal and per-candidate up/down evidence are
-- recorded so status is explainable ("Online · via tcp/5060") instead of a bare bit.
-- ICMP is intentionally NOT a required signal (many hosts here block ping); it may be
-- layered later as OPTIONAL supplemental evidence only.

ALTER TABLE monitoring_checks ADD COLUMN candidate_ports JSONB NOT NULL DEFAULT '[]'; -- TCP ports this check probes (reachability = any-up)
ALTER TABLE monitoring_checks ADD COLUMN last_signal TEXT NOT NULL DEFAULT '';        -- winning signal, e.g. "tcp/5060" ("" = down)
ALTER TABLE monitoring_checks ADD COLUMN last_evidence JSONB NOT NULL DEFAULT '{}';   -- {"up":["tcp/5060"],"down":["tcp/3389"],"icmp":"skipped"}

-- Fast list/badge display without re-reading the checks per row.
ALTER TABLE devices ADD COLUMN reachability_signal TEXT NOT NULL DEFAULT '';        -- e.g. "tcp/5060" (the proof it's online)
ALTER TABLE devices ADD COLUMN reachability_confidence TEXT NOT NULL DEFAULT '';    -- none | low | medium | high
