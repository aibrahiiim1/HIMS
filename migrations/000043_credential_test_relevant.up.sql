-- Mark whether a credential-test result was for a RELEVANT protocol for the
-- device's candidate type (e.g. WinRM on a Windows host) vs an irrelevant probe.
-- The discovery scan now only tests relevant protocols, but this flag lets the
-- UI group "expected access method" results apart from legacy/other attempts and
-- avoids scaring operators with irrelevant SNMP/ONVIF/SSH failures on a Windows
-- workstation. Existing rows default to TRUE (they predate the protocol plan).
ALTER TABLE credential_test_results
    ADD COLUMN IF NOT EXISTS relevant BOOLEAN NOT NULL DEFAULT TRUE;
