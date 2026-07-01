-- Media type of a disk/volume's backing physical device: SSD | NVMe | HDD (or empty when the
-- collector could not determine it — never guessed). Populated by the deep OS collectors
-- (Windows MSFT_PhysicalDisk/Win32_DiskDrive, Linux lsblk rotational/transport).
ALTER TABLE os_disks ADD COLUMN IF NOT EXISTS media_type text NOT NULL DEFAULT '';
