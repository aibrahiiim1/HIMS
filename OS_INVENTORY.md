# HIMS — Deep OS Inventory (Windows / Linux)

Authenticated deep operating-system inventory: OS, hardware, disks, network,
services, processes, installed software, detected roles, and a Windows event-log
summary. Complements the lightweight OS/NVR classification (see
`CLASSIFICATION.md`) — classification decides *what* a device is; deep inventory
gathers *what's inside* it once a working credential is bound.

## Honesty contract

- A field is written **only when the host actually returned it**. Absent data is
  stored as `NULL` / empty and rendered **"Not collected"** / **"Not collected
  yet"** — never fabricated.
- Every section records **how** (`collection_method` / `collection_source`:
  `winrm` | `ssh` | `snmp`) and **when** (`collected_at` / `last_seen_at`).
- Re-collection **prunes** rows from the same source not seen this poll
  (prune-on-poll), so stale entries disappear automatically.
- HIMS **never guesses passwords** — collection uses the device's operator-bound
  credential. Find/bind a working one via **Credential Testing** first.

## Connection methods

### Windows — WinRM is the authenticated method
PowerShell Remoting over WinRM (port 5985). WMI/CIM data is gathered through
WinRM via `Get-CimInstance` — **not** DCOM. Pure-Go WMI/DCOM from a Linux host is
not viable, so:
- **WinRM / PowerShell** — primary (this module).
- **WMI/CIM** — via `Get-CimInstance` over WinRM (no separate DCOM path).
- **SMB / RPC** — fingerprint evidence only (the classification probe), not deep
  inventory.
- **Active Directory** — computer-object enrichment (the AD import).
- **SNMP** — fallback for OS/hardware basics where WinRM is unavailable
  (host-resources driver).
- **Native agent** — a future option for deeper Windows data (services/process
  detail beyond WinRM, ETW, etc.).

### Linux — SSH
One guarded shell script over SSH (password auth today; key-based auth is a
documented future in `internal/ssh`). SNMP is the fallback where SSH is closed.

## What is collected

| Section | Windows source | Linux source |
|---|---|---|
| Identity (host/FQDN/domain/workgroup/user) | `Win32_ComputerSystem` | `hostname`, `/etc/os-release` |
| OS (caption/version/build/edition/arch/kernel/install/boot/uptime/tz) | `Win32_OperatingSystem` | `os-release`, `uname`, `/proc/uptime`, `timedatectl` |
| Hardware (mfr/model/serial/BIOS/CPU/RAM) | `Win32_ComputerSystem`/`Win32_BIOS`/`Win32_Processor` | `lscpu`, `/proc/meminfo`, `/sys/class/dmi` |
| Disks / volumes | `Win32_LogicalDisk` | `df --output` |
| Network (NIC/MAC/IP/gw/DNS/DHCP) | `Win32_NetworkAdapterConfiguration` | `ip link/addr/route`, `resolv.conf` |
| Services | `Win32_Service` | `systemctl list-units` + `list-unit-files` |
| Processes (top 50 by memory) | `Get-Process` | `ps -eo … --sort=-rss` |
| Installed software | registry uninstall keys | `dpkg-query` / `rpm -qa` |
| Roles | from services | from active services |
| Event-log summary (24h counts) | `Get-WinEvent` | — |

Roles are inferred from **service/package evidence**, never ports alone, and
stored in `os_roles` (free-form: Domain Controller, DNS/DHCP/SQL/IIS/Hyper-V,
Web/Database/File/DNS server, Docker Host, Kubernetes Node, Monitoring Server, …).

## API

- `POST /api/v1/devices/{id}/collect-os` — run an on-demand collection
  (devices.write-gated, site-scoped). Picks WinRM (`os_family=windows`) or SSH
  (`linux`) and uses the device's bound credential. Returns per-section counts;
  audits counts only. Errors are honest: `400` "classify it first" (no
  windows/linux OS family), `400` "bind a credential" / "re-enter its secret".
- `GET /api/v1/devices/{id}/os-inventory` — the bundle: `{inventory, disks,
  nics, services, processes, software, roles}`; `inventory` is `null` until a
  collection runs.

The **Server detail** page shows the *Deep OS Inventory* card (summary + roles +
event summary + collapsible Disks/Network/Services/Processes/Software) with a
**Collect / Re-collect** button. The **Data Quality** centre surfaces
`os_not_inventoried` (servers/endpoints never deeply inventoried).

## Schema

`os_inventory` (1:1 summary) + `os_disks` / `os_nics` / `os_services` /
`os_processes` / `os_software` / `os_roles` (1:N), all source-tagged with
prune-on-poll. Migrations `000039` (inventory + collections) and `000040`
(`os_roles`).

## Verification status

Parsers (Windows `Get-CimInstance` JSON, Linux command output) and the
persistence mapping are unit-tested against captured-output fixtures, and the
persistence round-trip is verified against the live database (idempotent
prune-on-poll). End-to-end authenticated collection (WinRM to a real Windows
host, SSH to a real Linux host) is **host + credential gated** — it activates on
the deployment once a working credential is bound to a reachable Windows/Linux
device.
