-- AP operational status is a vendor-reported label whose vocabulary varies by
-- collection method: SNMP/SSH normalize to online/offline/unknown, but the
-- vendor management APIs report richer operational states the operator wants to
-- see verbatim — Extreme XCC: "In Service" / "Critical" / "Out of Service"; Ruckus
-- ZoneDirector: "Connected" / "Disconnected" / "Provisioning" / etc. The original
-- CHECK (online|offline|unknown) silently rejected those, so REST/XML AP rows
-- never persisted. Drop it; the UI renders status as a badge (any label is fine).
ALTER TABLE access_points DROP CONSTRAINT IF EXISTS access_points_status_check;
