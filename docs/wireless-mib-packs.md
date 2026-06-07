# Wireless SNMP MIB Packs — how collection works & how to add a controller

HIMS collects wireless **AP / SSID / client** data from a controller over SNMP by
walking specific OID **tables** and mapping their columns into the inventory.
A **MIB pack** is the config that tells the collector *which* tables to walk and
*which column = which field* (AP name, serial, status, client MAC, SSID, …).

## Why "I uploaded the MIB but no data showed up"

Uploading a vendor's `.mib`/`.txt` files (or a `.zip` of them) only teaches HIMS
the **names of the OIDs** — it does **not** tell it which tables are the AP/SSID/
client rosters or how to read their columns. Without that **mapping**, the walker
has nothing to collect, so the controller shows zero APs/SSIDs/clients.

Two ways to get data:

1. **Built-in packs (no work for you).** Common controllers ship with a built-in,
   pre-mapped pack that matches automatically by the device's SNMP identity:
   - **Extreme / HiPath** wireless controllers (sysObjectID `1.3.6.1.4.1.1916` / `5624`)
   - **Ruckus ZoneDirector** (sysObjectID `1.3.6.1.4.1.25053`, e.g. ZD1100/3050/5000)
   For these, just make sure the device is classified `wireless_controller` and has
   a working SNMP credential, then collection works out of the box.

2. **Upload + map a custom pack** (for a controller we don't ship a built-in for).

## How a device is matched to a pack

When collection runs, HIMS picks the **most specific** enabled pack whose
*Applies-to* matches the device's stored SNMP identity:

| Match on | Specificity |
|---|---|
| sysObjectID prefix (e.g. `1.3.6.1.4.1.25053`) | strongest (vendor-exact) |
| sysDescr contains (e.g. `ruckus`, `zonedirector`) | medium |
| device category (`wireless_controller`) | weakest |

The vendor-exact match always wins, so two controllers of different vendors never
collect with each other's pack.

## Uploading + mapping a custom MIB pack (Discovery → MIB Management)

1. **Upload** the vendor MIB file or `.zip` ("Upload MIB pack"). HIMS parses the
   OID definitions and creates a pack (Source = *user*).
2. **Applies-to**: set the sysObjectID prefix and/or a sysDescr keyword so the pack
   matches your controller (look at the device's *SNMP identity* on its page).
3. **Map tables**: for each roster, add a table with:
   - **Root OID** = the *table* OID (HIMS appends `.1` for the entry row internally).
   - **Purpose** = `aps` | `ssids` | `clients` | `radios` | `events`.
   - **Column map** = field → column number, e.g. for APs
     `ap_name`, `ap_mac`, `ap_serial`, `ap_status`, `ap_model`, `ap_ip`,
     `ap_client_count`; for clients `client_mac`, `client_ap`, `client_ssid`,
     `client_ip`, `client_rssi`; for SSIDs `ssid_name`, `ssid_vlan`,
     `ssid_client_count`.
   Use **Test against device** + **View raw rows** to see the live OIDs/values and
   confirm which column number holds each field before saving.
4. **Enable** the pack.
5. **Collect**: open the controller's **Wireless** page → **Run SNMP MIB**, or run a
   network scan that includes it. Data lands in the AP / SSID / Client tables with
   source `snmp_wireless_mib`.

> The collector walks only the **mapped columns** of a mapped table (not all ~50
> columns), so even a controller with hundreds of APs and thousands of clients
> collects quickly and completely.

## Ruckus ZoneDirector — reference (built-in `Ruckus ZoneDirector Wireless MIB`)

Root `1.3.6.1.4.1.25053.1.2.2.1` (RUCKUS-ZD-WLAN-MIB):

| Purpose | Table | Root OID | Key columns |
|---|---|---|---|
| aps | ruckusZDWLANAPTable | `…2.1.1.2.1` | mac=1, name=2, status=3, model=4, serial=5, fw=7, ip=10, client_count=15 |
| ssids | ruckusZDWLANTable | `…2.1.1.1.1` | ssid/name=1, vlan=7, client_count=12 |
| clients | ruckusZDWLANStaTable | `…2.1.1.3.1` | mac=1, ap=2, ssid=4, hostname=5, band=6, ip=8, rssi=81 |

AP status enum: `disconnected(0)`→offline, `connected(1)`→online.
