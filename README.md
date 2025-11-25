# raidhelper

A lightweight, single-file CLI tool for managing Linux software RAID (md) check/rebuild speeds and operations. Designed for quick, direct control over RAID behavior via `/proc` and `/sys` kernel interfaces.

## Features

- **Multi-device support:** Automatically detects and manages all active MD RAID arrays (md0, md1, etc.)
- **Speed control:** Set RAID check speeds to normal, high, or low
- **Check management:** Start, stop, and monitor RAID check operations
- **Progress tracking:** Real-time progress bar and time estimates
- **Disk monitoring:** List physical disks with serial numbers; compare snapshots to detect changes
- **Reboot control:** Schedule reboots after RAID checks or force immediate reboots
- **Minimal dependencies:** Single binary, relies only on `github.com/spf13/cobra`

## Installation

### Build from source

```bash
cd raidhelper
go build -o raid-helper
```

**Requirements:** Go >= 1.24.5 (see `go.mod`)

### Verify the build

```bash
./raid-helper --help
```

## Usage

All commands that write to kernel files (`/proc`, `/sys`) or reboot require **root privileges**.

### Commands

#### Speed Control

```bash
# Set RAID check to normal speed (200000 KB/s)
sudo ./raid-helper normal

# Set RAID check to high speed (2000000 KB/s)
sudo ./raid-helper high

# Set high speed for a limited time, then revert to normal
sudo ./raid-helper high 10   # High speed for 10 minutes

# Set RAID check to low speed (3000 KB/s)
sudo ./raid-helper low
```

#### RAID Check Management

```bash
# Start RAID check on all detected arrays
sudo ./raid-helper start

# Stop RAID check on all detected arrays
sudo ./raid-helper stop

# Check if RAID is currently being checked (returns 0 or 1)
./raid-helper check

# Show live progress with progress bar
./raid-helper progress
```

#### Status and Monitoring

```bash
# Show current RAID status (default command)
./raid-helper

# Show status only (without help text)
./raid-helper showstatus

# List physical disks with serial numbers (table format)
sudo ./raid-helper show-disks

# Export current disk state as JSON snapshot
sudo ./raid-helper show-disks --format json > disks-backup.json

# Compare current disk state to a saved snapshot
sudo ./raid-helper compare-disks disks-backup.json
```

#### Reboot Operations

```bash
# Wait for RAID checks to complete, then reboot
sudo ./raid-helper reboot

# Stop all RAID checks immediately and reboot
sudo ./raid-helper forcereboot
```

## Disk Monitoring Workflow

The `show-disks` and `compare-disks` commands help detect disk failures, replacements, and changes:

### 1. Create a Baseline Snapshot

After RAID setup, save the current disk state:

```bash
sudo ./raid-helper show-disks --format json > baseline-disks.json
```

This captures:
- All disks in RAID arrays (from `/proc/mdstat`)
- All standalone physical disks
- Manufacturer serial numbers (via `/sys/block` and `hdparm -I`)
- Timestamp (ISO 8601 format)

### 2. View Current Disk State

Display all disks with serials in a human-readable table:

```bash
sudo ./raid-helper show-disks
```

**Output example:**
```
################################
Physical Disks in RAID Arrays
################################
Device    Serial Number
------    ----------------
sda       WD-WCC4N1234567
sdb       WD-WCC4N7654321
################################

################################
Other Physical Disks
################################
Device    Serial Number
------    ----------------
sdc       SK-HYNIX9876543
################################
```

### 3. Detect Changes After Maintenance

After replacing a disk or adding new hardware, compare to baseline:

```bash
sudo ./raid-helper compare-disks baseline-disks.json
```

**Output example:**
```
################################
Disk Comparison Report
################################
Saved snapshot: baseline-disks.json
Saved timestamp: 2025-11-25T10:30:45Z
Current timestamp: 2025-11-25T14:22:10Z

[+] Added disks (1):
    sdd (serial: NEWDISK123456)

[-] Removed disks (1):
    sdc (serial: SK-HYNIX9876543)

[~] Changed disks (0):

################################
```

## Architecture

### System Integration

- **Reads:** `/proc/mdstat` (RAID status), `/sys/block/sd*/device/serial` (disk serials)
- **Writes:** `/proc/sys/dev/raid/speed_limit_max` (speed limit), `/sys/block/md*/md/sync_action` (check control)
- **Executes:** `reboot` (privileged), `hdparm -I /dev/sdX` (disk queries, root required)

### Code Structure

- **Single file:** `main.go` — all CLI commands, parsing, and system integration
- **CLI framework:** `github.com/spf13/cobra` (Cobra v1.8.1)
- **Parsing:** `bufio.Scanner` + `regexp` for `/proc/mdstat` parsing
- **Error handling:** `log.Fatalf` for fatal errors (immediate exit), `log.Printf` for warnings

### Key Functions

| Function | Purpose |
|----------|---------|
| `enumerateMdDevices()` | Scan `/proc/mdstat` for active RAID arrays |
| `enumeratePhysicalDisksInRaid()` | Extract base disk names from RAID arrays |
| `getAllPhysicalDisks()` | Enumerate all `sd*` devices via `/sys/block` |
| `getDiskSerial()` | Query manufacturer serial (sysfs → hdparm fallback) |
| `getMdChecking()` | Check if RAID check is in progress |
| `getMdProgress()` | Extract progress percentage from `/proc/mdstat` |
| `setSpeed()` | Write speed limit to kernel |
| `setSyncActionAll()` | Control check operations on all arrays |

## Testing

### Non-Destructive Testing (No Root Required)

```bash
# Build
go build -o raid-helper

# Read-only commands
./raid-helper showstatus
./raid-helper check
./raid-helper progress
./raid-helper show-disks  # May show "N/A" for serials without root
```

### Integration Testing (Requires Root)

```bash
# Start a RAID check
sudo ./raid-helper start

# Monitor progress
sudo ./raid-helper progress

# Control speed
sudo ./raid-helper high
sudo ./raid-helper normal

# Stop check
sudo ./raid-helper stop

# Save and compare disk state
sudo ./raid-helper show-disks --format json > test-disks.json
sudo ./raid-helper compare-disks test-disks.json
```

### Unit Testing & Mocking

All filesystem paths are package-level variables, allowing tests to mock `/proc` and `/sys`:

```go
// In tests, override paths:
oldPath := mdstatPath
mdstatPath = "/tmp/mock-mdstat"
defer func() { mdstatPath = oldPath }()

// Now functions use the mock path
```

## Troubleshooting

### Serial Numbers Show as "N/A"

This typically means:
1. **Missing `hdparm`:** Install it with `sudo apt-get install hdparm` (Debian/Ubuntu)
2. **Disk permissions:** Some systems require root to read `/sys/block/sdX/device/serial`
3. **Workaround:** Always run `show-disks` with `sudo`

### Commands Fail with "Permission Denied"

Commands that write to kernel files require root:
- `normal`, `high`, `low` — write to `/proc/sys/dev/raid/speed_limit_max`
- `start`, `stop` — write to `/sys/block/md*/md/sync_action`
- `reboot`, `forcereboot` — execute `reboot` syscall

**Solution:** Run with `sudo`

### Multiple RAID Arrays Not Detected

Ensure arrays are active in `/proc/mdstat`:

```bash
cat /proc/mdstat
```

Expected output:
```
md0 : active raid1 sda1[0] sdb1[1]
      ...
md1 : active raid5 sdc1[0] sdd1[1] sde1[2]
      ...
```

If arrays aren't listed, they're either inactive or not assembled. Activate with `sudo mdadm --assemble --scan`.

## Contributing

When adding features:
1. Keep the single-file structure (`main.go`)
2. Use `bufio.Scanner` + `regexp` for parsing (consistent with existing code)
3. Make filesystem paths package-level `var` (not `const`) for test mocking
4. Document root requirements in Cobra command `Long` field
5. Use `log.Fatalf` for fatal errors, `log.Printf` for warnings
6. Preserve backward compatibility with existing CLI surface

## License

[Specify your license here, e.g., MIT, GPL-3.0, etc.]

## References

- [mdadm manual](https://linux.die.net/man/8/mdadm)
- [Linux Software RAID (md)](https://en.wikipedia.org/wiki/Mdadm)
- [hdparm manual](https://linux.die.net/man/8/hdparm)
- [Cobra CLI framework](https://cobra.dev)
