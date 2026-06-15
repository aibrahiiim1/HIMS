# Enabling HIMS management of Windows workstations (172.21.60.0/24) — GPO checklist

**Why:** HIMS discovers every Windows host, but can only *manage* (collect inventory
from) a host that permits a remote-management protocol. By default Windows
workstations block both WinRM and remote WMI/DCOM, so they stay "discovered but
unmanaged." This is a one-time domain policy, not a per-host or credential task —
the `dpm@coralsearesorts.com` credential is already loaded in HIMS and works once a
protocol is reachable.

Pick **Option A (WinRM)** — simplest, agentless, what HIMS prefers. Option B
(WMI/DCOM) is the fallback already used by the site Relay Agent for legacy hosts.
You can apply both; HIMS tries WinRM first, then WMI.

No passwords appear in any command below. HIMS holds the credential; the
verification commands are connectivity-only.

---

## Target OU (both options)

Link the GPO to the OU that contains the workstation computer objects for this
subnet — e.g. `OU=Workstations,OU=CHR,DC=coralsearesorts,DC=com` (adjust to your
tree). Apply to **Computer Configuration**. After linking, force/await refresh:

- On a sample host (or via the GPO): `gpupdate /target:computer /force`
- Or wait one policy refresh cycle (~90 min) + reboot.

---

## Option A — Enable WinRM (recommended)

**GPO settings** — *Computer Configuration → Policies → Administrative Templates*:

1. **Windows Remote Management (WinRM) → WinRM Service → "Allow remote server
   management through WinRM"** → **Enabled**; IPv4 filter `*` (or scope to the
   management subnet, e.g. `172.21.60.0-172.21.60.255`).
2. **Windows Remote Management (WinRM) → WinRM Service → "Allow unencrypted
   traffic"** → leave **Disabled** (HIMS uses NTLM over 5985; keep encryption).
3. **System Services → "Windows Remote Management (WS-Management)"** → startup
   **Automatic** (so the service runs at boot). *Computer Configuration → Policies
   → Windows Settings → Security Settings → System Services.*

**Firewall rule** — *Computer Configuration → Policies → Windows Settings →
Security Settings → Windows Defender Firewall with Advanced Security → Inbound
Rules*. Allow the predefined group **"Windows Remote Management (HTTP-In)"**
(TCP **5985**), scoped to the HIMS host / management subnet as Remote IP.

> One-shot equivalent (what the GPO automates), for reference only:
> `Enable-PSRemoting -Force` enables the service + 5985 firewall rule.

**Verification from the HIMS host (172.21.60.20)** — connectivity only, no creds:

```powershell
Test-NetConnection 172.21.60.12 -Port 5985        # expect TcpTestSucceeded: True
Test-WSMan 172.21.60.12                            # expect a WSMan identity response
```

Then in HIMS: re-scan 172.21.60.0/24 → those hosts collect over WinRM with the
stored `dpm` credential.

---

## Option B — Enable WMI / DCOM (fallback; used by the site Relay Agent)

**Firewall rule** — *Computer Configuration → Policies → Windows Settings →
Security Settings → Windows Defender Firewall with Advanced Security → Inbound
Rules*. Allow the predefined group **"Windows Management Instrumentation (WMI)"**,
which opens:

- **WMI-In** (the DCOM/WMI service, `%systemroot%\system32\svchost.exe` / WMI),
- **DCOM-In** (TCP **135**, the RPC endpoint mapper),
- **ASync-In**.

Scope **Remote IP** to the HIMS host / management subnet.

**DCOM / RPC considerations:**

- DCOM uses **TCP 135** (endpoint mapper) + a **dynamic RPC port range** (default
  49152–65535). The WMI firewall group handles the callback ports; do **not** also
  need to open the whole dynamic range if the predefined group is used.
- Remote WMI requires the calling account to be a **local Administrator** on the
  target (domain admin satisfies this via the Administrators group) — already true
  for `dpm`.
- **UAC remote restrictions:** for *local* (non-domain) admin accounts, set
  `LocalAccountTokenFilterPolicy=1`. **Not needed for `dpm`** (a domain account) —
  only relevant if you intend to use the local `administrator` account remotely.
  GPO path: *Preferences → Registry* →
  `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System\LocalAccountTokenFilterPolicy = 1 (DWORD)`.

**Verification from the HIMS host (172.21.60.20)** — connectivity only, no creds:

```powershell
Test-NetConnection 172.21.60.12 -Port 135         # expect TcpTestSucceeded: True
```

(The authenticated WMI check is performed by HIMS / the site Relay Agent using the
stored `dpm` credential — you do not run it by hand.)

Then in HIMS: re-scan 172.21.60.0/24 → WinRM-disabled hosts route to the site Relay
Agent (`CHR`), which collects them via WMI/DCOM with `dpm`.

---

## After applying the GPO

Tell HIMS to re-scan `172.21.60.0/24`. With the routing fix already deployed, every
alive Windows host now attempts WinRM → then WMI/agent, and lands as **managed**
where the policy permits it. Hosts still failing will report the *exact* reason
(`winrm_disabled`, `wmi_firewall_blocked`, `agent_offline`) — never "credential
failed" when the credential is valid.
