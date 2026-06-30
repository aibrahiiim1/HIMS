# Alcatel-Lucent OmniPCX Enterprise — voice connector

HIMS onboards the OmniPCX Enterprise call server (telnet-only management; no SSH) as a managed
PBX. This records what is collected, what is NOT (and why), and the ONLY supported paths to the
telephony directory/phones.

## Collected today (real, live-validated)

- **Managed PBX** — bound on a real authenticated protocol success (telnet `mtcl` login or
  SNMP), never on reachability alone.
- **Subtype `alcatel_omnipcx`** — set by both manual onboarding and discovery.
- **Identity** — vendor `Alcatel-Lucent`, model `OmniPCX Enterprise`, software version
  (e.g. `R7.1-f5.401-29-a-eg-c6s2`), release/delivery/patch/country/CPU-role — read from the
  login banner (`Application software identity`). Plus SNMP interfaces/system.

Discovery auto-detects the OXE from its unauthenticated telnet/23 banner and, with a scoped
`cli` credential, authenticates + onboards it as `managed pbx/alcatel_omnipcx`. Wrong cred =
`credential_failed`; banner-only = `credential_required`.

## NOT collected — directory / phone sets / extensions (`external_dependency_required`)

**Proven live (read-only), do NOT re-probe:** the `mtcl` telnet account is **information-only**
— after proper telnet negotiation the server echoes input (session is live), but there is **no
shell prompt and no command executes** (`mgr`, `ls`, `cat`, `echo`, `whoami`, `id` all return
nothing, with every line ending). **SNMP** exposes identity only (2 status OIDs under
`.1.3.6.1.4.1.637`; no telephony tables). The user/extension directory and registered phone
sets live in the call-server database and are **not reachable** over telnet or SNMP.

No directory/phone/extension data is ever fabricated.

## Supported collection paths (only these — implement against one when access is provided)

1. **OmniVista 8770** — the OXE management server. The supported, vendor-blessed source for the
   directory, sets, trunk groups and software inventory. Build a collector against its
   API/export when the 8770 server + credentials are available.
2. **LDAP / directory export** — if the OXE/8770 LDAP directory or a CSV/file export is enabled,
   collect users/extensions from that export.
3. **Shell-capable account (only if explicitly provided)** — `mtcl` here is locked to info-only.
   If an operator provides a higher-privilege account (`swinst`/`root`) that yields a REAL shell
   where `mgr` runs, the `mgr` read path can be implemented. Read-only navigation only.

## Safety

Do not run risky or invasive commands on a production PBX. Identity collection is read-only
(login banner). The directory collector, when built against OmniVista/LDAP/an authorized shell,
must be read-only and must never modify call-server configuration.
