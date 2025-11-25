package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	normalSpeed = "200000"
	highSpeed   = "2000000"
	lowSpeed    = "3000"
)

// Package-level vars allow test mocking of filesystem paths (per coding instructions)
var (
	speedLimitPath = "/proc/sys/dev/raid/speed_limit_max"
	mdstatPath     = "/proc/mdstat"
	syncActionPath = "/sys/block/md0/md/sync_action"
	sysBlockPath   = "/sys/block"
)

// Device represents a single MD RAID device and its sysfs paths
type Device struct {
	Name           string // e.g., "md0", "md1"
	SyncActionPath string // e.g., "/sys/block/md0/md/sync_action"
}

// DiskInfo represents a physical disk's information
type DiskInfo struct {
	Device string `json:"device"`
	Serial string `json:"serial"`
}

// DisksSnapshot represents a complete disk snapshot (for save/compare)
type DisksSnapshot struct {
	Timestamp  string     `json:"timestamp"`
	InRaid     []DiskInfo `json:"in_raid"`
	Standalone []DiskInfo `json:"standalone"`
}

// enumerateMdDevices scans mdstatPath and returns all active MD devices.
// Enables multi-device support by detecting all md* entries in /proc/mdstat.
func enumerateMdDevices() ([]Device, error) {
	file, err := os.Open(mdstatPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", mdstatPath, err)
	}
	defer file.Close()

	var devices []Device
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()
		// Look for lines starting with md* (e.g., "md0 : active raid1")
		if strings.HasPrefix(line, "md") {
			parts := strings.Fields(line)
			if len(parts) > 0 {
				deviceName := parts[0]
				if !seen[deviceName] {
					seen[deviceName] = true
					devices = append(devices, Device{
						Name:           deviceName,
						SyncActionPath: fmt.Sprintf("/sys/block/%s/md/sync_action", deviceName),
					})
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", mdstatPath, err)
	}

	return devices, nil
}

// enumeratePhysicalDisksInRaid returns a map of base disk names (e.g., "sda", "sdb")
// extracted from /proc/mdstat device list. Normalizes partition references (e.g., sda1 -> sda).
// Consistent with project parsing patterns: bufio.Scanner + regexp.
func enumeratePhysicalDisksInRaid() (map[string]bool, error) {
	file, err := os.Open(mdstatPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", mdstatPath, err)
	}
	defer file.Close()

	disks := make(map[string]bool)
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()
		// Look for device references like sda1[0], sdb[1], sdm3[2]
		// Pattern: sdX followed by optional digits, then [index]
		re := regexp.MustCompile(`(sd[a-z]+)\d*\[\d+\]`)
		matches := re.FindAllStringSubmatch(line, -1)
		for _, match := range matches {
			if len(match) > 1 {
				baseDisk := match[1] // Extract base name: sda1 -> sda
				disks[baseDisk] = true
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", mdstatPath, err)
	}

	return disks, nil
}

// getAllPhysicalDisks returns a map of all sdX disks present on the system
// by reading sysBlockPath directory entries. Filters for sd* devices only.
func getAllPhysicalDisks() (map[string]bool, error) {
	entries, err := ioutil.ReadDir(sysBlockPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", sysBlockPath, err)
	}

	disks := make(map[string]bool)
	for _, entry := range entries {
		name := entry.Name()
		// Match sd* (sda, sdb, sdm, etc.) but not md* or dm*
		if strings.HasPrefix(name, "sd") && len(name) > 2 {
			disks[name] = true
		}
	}

	return disks, nil
}

// getDiskSerial retrieves the manufacturer serial number for a given disk (e.g., "sda").
// Uses hdparm -I to query the drive directly (most reliable, requires root).
// Falls back to /sys/block/sdX/device/serial if hdparm unavailable.
// Returns "N/A" and logs warning if both methods fail (non-fatal, allows continue).
func getDiskSerial(diskName string) (string, error) {
	// Try sysfs first (fastest, no command execution)
	serialPath := filepath.Join(sysBlockPath, diskName, "device", "serial")
	data, err := ioutil.ReadFile(serialPath)
	if err == nil {
		serial := strings.TrimSpace(string(data))
		if serial != "" && serial != "N/A" {
			return serial, nil
		}
	}

	// Primary method: use hdparm to query drive directly (most reliable, requires root)
	devPath := filepath.Join("/dev", diskName)
	cmd := exec.Command("hdparm", "-I", devPath)
	output, err := cmd.Output()
	if err == nil {
		// Parse hdparm output for Serial Number line (manufacturer serial)
		scanner := bufio.NewScanner(strings.NewReader(string(output)))
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "Serial Number") {
				parts := strings.Split(line, ":")
				if len(parts) > 1 {
					serial := strings.TrimSpace(parts[1])
					if serial != "" && serial != "N/A" {
						return serial, nil
					}
				}
			}
		}
	}

	return "N/A", fmt.Errorf("could not read manufacturer serial for %s (tried: sysfs, hdparm -I)", diskName)
}

// formatDisksTable outputs disk info in ASCII table format, consistent with showStatus() style.
// Displays two sections: "In RAID Arrays" and "Other Physical Disks".
func formatDisksTable(inRaid []DiskInfo, standalone []DiskInfo) string {
	var output strings.Builder

	if len(inRaid) > 0 {
		output.WriteString("################################\n")
		output.WriteString("Physical Disks in RAID Arrays\n")
		output.WriteString("################################\n")
		output.WriteString("Device    Serial Number\n")
		output.WriteString("------    ----------------\n")
		for _, disk := range inRaid {
			output.WriteString(fmt.Sprintf("%-9s %s\n", disk.Device, disk.Serial))
		}
		output.WriteString("################################\n\n")
	}

	if len(standalone) > 0 {
		output.WriteString("################################\n")
		output.WriteString("Other Physical Disks\n")
		output.WriteString("################################\n")
		output.WriteString("Device    Serial Number\n")
		output.WriteString("------    ----------------\n")
		for _, disk := range standalone {
			output.WriteString(fmt.Sprintf("%-9s %s\n", disk.Device, disk.Serial))
		}
		output.WriteString("################################\n")
	}

	if len(inRaid) == 0 && len(standalone) == 0 {
		output.WriteString("No physical disks found.\n")
	}

	return output.String()
}

// formatDisksJSON outputs disk info in JSON format with timestamp (for snapshots).
func formatDisksJSON(inRaid []DiskInfo, standalone []DiskInfo) (string, error) {
	snapshot := DisksSnapshot{
		Timestamp:  time.Now().Format(time.RFC3339),
		InRaid:     inRaid,
		Standalone: standalone,
	}

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return "", err
	}

	return string(data), nil
}

// getDisksInfo gathers all disk information by enumerating RAID and system disks,
// querying serials, and categorizing them. Returns two sorted slices:
// inRaid (disks in RAID arrays) and standalone (other disks).
// Non-fatal errors logged; continues processing.
func getDisksInfo() ([]DiskInfo, []DiskInfo, error) {
	inRaidMap, err := enumeratePhysicalDisksInRaid()
	if err != nil {
		log.Printf("Warning: failed to enumerate RAID disks: %v", err)
		inRaidMap = make(map[string]bool)
	}

	allDisksMap, err := getAllPhysicalDisks()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to enumerate all disks: %w", err)
	}

	var inRaid []DiskInfo
	var standalone []DiskInfo

	for diskName := range allDisksMap {
		serial, err := getDiskSerial(diskName)
		if err != nil {
			log.Printf("Warning: failed to get serial for %s: %v", diskName, err)
		}

		info := DiskInfo{Device: diskName, Serial: serial}

		if inRaidMap[diskName] {
			inRaid = append(inRaid, info)
		} else {
			standalone = append(standalone, info)
		}
	}

	// Sort for consistent output
	sort.Slice(inRaid, func(i, j int) bool { return inRaid[i].Device < inRaid[j].Device })
	sort.Slice(standalone, func(i, j int) bool { return standalone[i].Device < standalone[j].Device })

	return inRaid, standalone, nil
}

// getMdChecking returns the number of RAID arrays currently being checked
func getMdChecking() (int, error) {
	file, err := os.Open(mdstatPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open %s: %w", mdstatPath, err)
	}
	defer file.Close()

	count := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), "check") {
			count++
		}
	}

	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("failed to read %s: %w", mdstatPath, err)
	}

	return count, nil
}

// getMdProgress extracts the progress percentage from mdstat
func getMdProgress() (float64, error) {
	file, err := os.Open(mdstatPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open %s: %w", mdstatPath, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "check") || strings.Contains(line, "resync") {
			// Look for progress pattern like [====>......]
			progressRe := regexp.MustCompile(`\[([=>\.]+)\]`)
			matches := progressRe.FindStringSubmatch(line)
			if len(matches) > 1 {
				progress := matches[1]
				completed := strings.Count(progress, "=") + strings.Count(progress, ">")
				total := len(progress)
				return float64(completed) / float64(total) * 100, nil
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("failed to read %s: %w", mdstatPath, err)
	}

	return 0, fmt.Errorf("no progress information found")
}

// drawProgressBar creates a progress bar string with the given percentage
func drawProgressBar(percent float64, width int) string {
	completed := int(float64(width) * percent / 100)
	remaining := width - completed

	bar := strings.Repeat("=", completed)
	if remaining > 0 {
		bar += ">"
		remaining--
	}
	bar += strings.Repeat(".", remaining)

	return fmt.Sprintf("[%s] %.1f%%", bar, percent)
}

// getMdTimeLeft extracts the time left from mdstat
func getMdTimeLeft() (string, error) {
	file, err := os.Open(mdstatPath)
	if err != nil {
		return "", fmt.Errorf("failed to open %s: %w", mdstatPath, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "finish") {
			// Use regex to extract finish=XXXmin
			re := regexp.MustCompile(`finish=([^\s]+)`)
			matches := re.FindStringSubmatch(line)
			if len(matches) > 1 {
				return matches[1], nil
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("failed to read %s: %w", mdstatPath, err)
	}

	return "", nil
}

// getCurrentSpeed reads the current RAID speed limit
func getCurrentSpeed() (string, error) {
	data, err := os.ReadFile(speedLimitPath)
	if err != nil {
		return "", fmt.Errorf("failed to read speed limit: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// setSpeed writes the speed limit to the proc file
func setSpeed(speed string) error {
	return os.WriteFile(speedLimitPath, []byte(speed), 0644)
}

// setSyncAction writes to the sync_action file for a specific device
func setSyncAction(device Device, action string) error {
	return os.WriteFile(device.SyncActionPath, []byte(action), 0644)
}

// setSyncActionAll applies sync action to all detected MD devices.
// Used by start, stop, and forcereboot commands. Requires root.
func setSyncActionAll(action string) error {
	devices, err := enumerateMdDevices()
	if err != nil {
		return fmt.Errorf("failed to enumerate devices: %w", err)
	}

	if len(devices) == 0 {
		return fmt.Errorf("no MD RAID devices found")
	}

	for _, dev := range devices {
		if err := setSyncAction(dev, action); err != nil {
			return fmt.Errorf("failed to set %s on %s: %w", action, dev.Name, err)
		}
	}

	return nil
}

func main() {
	var rootCmd = &cobra.Command{
		Use:   "raid-helper",
		Short: "A tool for managing Linux software RAID operations",
		Long:  "raid-helper provides commands to control RAID check speeds, start/stop operations, and manage reboots. Supports multiple MD devices (md0, md1, etc.).",
		Run: func(cmd *cobra.Command, args []string) {
			showStatus()
		},
	}

	var normalCmd = &cobra.Command{
		Use:   "normal",
		Short: "Set RAID check to normal speed",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("Setting raid check to normal speed")
			if err := setSpeed(normalSpeed); err != nil {
				log.Fatalf("Error setting normal speed: %v", err)
			}
		},
	}

	var highCmd = &cobra.Command{
		Use:   "high [minutes]",
		Short: "Set RAID check to high speed",
		Long:  "Set RAID check to high speed, optionally for a specified number of minutes",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("Setting raid check to high speed")
			if err := setSpeed(highSpeed); err != nil {
				log.Fatalf("Error setting high speed: %v", err)
			}

			if len(args) > 0 {
				minutes, err := strconv.Atoi(args[0])
				if err != nil {
					log.Fatalf("Invalid minutes value: %v", err)
				}
				fmt.Printf("for %d minutes\n", minutes)
				time.Sleep(time.Duration(minutes) * time.Minute)
				if err := setSpeed(normalSpeed); err != nil {
					log.Fatalf("Error resetting to normal speed: %v", err)
				}
			}
		},
	}

	var lowCmd = &cobra.Command{
		Use:   "low",
		Short: "Set RAID check to low speed",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("Setting raid check to low speed")
			if err := setSpeed(lowSpeed); err != nil {
				log.Fatalf("Error setting low speed: %v", err)
			}
		},
	}

	var stopCmd = &cobra.Command{
		Use:   "stop",
		Short: "Stop RAID check",
		Long:  "Stops RAID check operations on all detected MD devices. Requires root.",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("Stopping raid check")
			if err := setSyncActionAll("idle"); err != nil {
				log.Fatalf("Error stopping raid check: %v", err)
			}
		},
	}

	var startCmd = &cobra.Command{
		Use:   "start",
		Short: "Start RAID check",
		Long:  "Starts RAID check operations on all detected MD devices. Requires root.",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("Starting raid check")
			if err := setSyncActionAll("check"); err != nil {
				log.Fatalf("Error starting raid check: %v", err)
			}
		},
	}

	var checkCmd = &cobra.Command{
		Use:   "check",
		Short: "Check if RAID is currently being checked",
		Run: func(cmd *cobra.Command, args []string) {
			count, err := getMdChecking()
			if err != nil {
				log.Fatalf("Error checking RAID status: %v", err)
			}
			fmt.Println(count)
		},
	}

	var progressCmd = &cobra.Command{
		Use:   "progress",
		Short: "Show RAID check progress with a progress bar",
		Run: func(cmd *cobra.Command, args []string) {
			count, err := getMdChecking()
			if err != nil {
				log.Fatalf("Error checking RAID status: %v", err)
			}

			if count == 0 {
				fmt.Println("No RAID check in progress")
				return
			}

			progress, err := getMdProgress()
			if err != nil {
				log.Fatalf("Error getting progress: %v", err)
			}

			timeLeft, err := getMdTimeLeft()
			if err != nil {
				log.Printf("Error getting time left: %v", err)
			}

			fmt.Println(drawProgressBar(progress, 50))
			if timeLeft != "" {
				fmt.Printf("Time remaining: %s\n", timeLeft)
			}
		},
	}

	var rebootCmd = &cobra.Command{
		Use:   "reboot",
		Short: "Reboot the machine once the RAID check is done",
		Long:  "Waits for all RAID checks to complete before rebooting. Requires root.",
		Run: func(cmd *cobra.Command, args []string) {
			waitForRaidAndReboot(false)
		},
	}

	var forceRebootCmd = &cobra.Command{
		Use:   "forcereboot",
		Short: "Stop RAID check and reboot",
		Long:  "Immediately stops RAID check on all devices and reboots. Requires root.",
		Run: func(cmd *cobra.Command, args []string) {
			if err := setSyncActionAll("idle"); err != nil {
				log.Fatalf("Error stopping raid check: %v", err)
			}
			waitForRaidAndReboot(true)
		},
	}

	var showStatusOnlyCmd = &cobra.Command{
		Use:   "showstatus",
		Short: "Shows the status without the list of commands",
		Run: func(cmd *cobra.Command, args []string) {
			showOnlyStatus()
		},
	}

	var showDisksCmd = &cobra.Command{
		Use:   "show-disks",
		Short: "List physical disks with serial numbers",
		Long:  "Shows physical disks in RAID arrays and standalone disks with their serial numbers. Requires root to access /sys/block device information.",
		Run: func(cmd *cobra.Command, args []string) {
			format, _ := cmd.Flags().GetString("format")

			inRaid, standalone, err := getDisksInfo()
			if err != nil {
				log.Fatalf("Error gathering disk information: %v", err)
			}

			var output string
			if format == "json" {
				var jsonErr error
				output, jsonErr = formatDisksJSON(inRaid, standalone)
				if jsonErr != nil {
					log.Fatalf("Error formatting JSON: %v", jsonErr)
				}
			} else {
				output = formatDisksTable(inRaid, standalone)
			}

			fmt.Print(output)
		},
	}
	showDisksCmd.Flags().String("format", "table", "Output format: table or json")

	var compareDisksCmd = &cobra.Command{
		Use:   "compare-disks [json-file]",
		Short: "Compare current disk state to a saved snapshot",
		Long:  "Compares current physical disks and serial numbers to a previously saved JSON snapshot. Reports added, removed, and changed disks. Requires root.",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			filePath := args[0]

			// Read saved snapshot
			data, err := ioutil.ReadFile(filePath)
			if err != nil {
				log.Fatalf("Error reading snapshot file: %v", err)
			}

			var savedSnapshot DisksSnapshot
			if err := json.Unmarshal(data, &savedSnapshot); err != nil {
				log.Fatalf("Error parsing snapshot JSON: %v", err)
			}

			// Get current state
			inRaid, standalone, err := getDisksInfo()
			if err != nil {
				log.Fatalf("Error gathering current disk information: %v", err)
			}

			// Combine for comparison
			saved := make(map[string]string)
			for _, disk := range savedSnapshot.InRaid {
				saved[disk.Device] = disk.Serial
			}
			for _, disk := range savedSnapshot.Standalone {
				saved[disk.Device] = disk.Serial
			}

			current := make(map[string]string)
			for _, disk := range inRaid {
				current[disk.Device] = disk.Serial
			}
			for _, disk := range standalone {
				current[disk.Device] = disk.Serial
			}

			// Detect changes
			var added []string
			var removed []string
			var changed []struct {
				device    string
				oldSerial string
				newSerial string
			}

			for device, currentSerial := range current {
				if savedSerial, exists := saved[device]; !exists {
					added = append(added, device)
				} else if savedSerial != currentSerial {
					changed = append(changed, struct {
						device    string
						oldSerial string
						newSerial string
					}{device, savedSerial, currentSerial})
				}
			}

			for device := range saved {
				if _, exists := current[device]; !exists {
					removed = append(removed, device)
				}
			}

			// Sort for consistent output
			sort.Strings(added)
			sort.Strings(removed)

			// Display results
			fmt.Println("################################")
			fmt.Println("Disk Comparison Report")
			fmt.Println("################################")
			fmt.Printf("Saved snapshot: %s\n", filePath)
			fmt.Printf("Saved timestamp: %s\n", savedSnapshot.Timestamp)
			fmt.Printf("Current timestamp: %s\n\n", time.Now().Format(time.RFC3339))

			if len(added) == 0 && len(removed) == 0 && len(changed) == 0 {
				fmt.Println("✓ No changes detected. Disk configuration is identical.")
			} else {
				if len(added) > 0 {
					fmt.Printf("\n[+] Added disks (%d):\n", len(added))
					for _, device := range added {
						fmt.Printf("    %s (serial: %s)\n", device, current[device])
					}
				}

				if len(removed) > 0 {
					fmt.Printf("\n[-] Removed disks (%d):\n", len(removed))
					for _, device := range removed {
						fmt.Printf("    %s (serial: %s)\n", device, saved[device])
					}
				}

				if len(changed) > 0 {
					fmt.Printf("\n[~] Changed disks (%d):\n", len(changed))
					for _, c := range changed {
						fmt.Printf("    %s: %s → %s\n", c.device, c.oldSerial, c.newSerial)
					}
				}
			}

			fmt.Println("\n################################")
		},
	}

	rootCmd.AddCommand(normalCmd, highCmd, lowCmd, stopCmd, startCmd, checkCmd, progressCmd, rebootCmd, forceRebootCmd, showStatusOnlyCmd, showDisksCmd, compareDisksCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func showOnlyStatus() {
	fmt.Println("###############################")
	currentTime := time.Now()
	fmt.Println("#", currentTime.Format("2006-01-02 15:04:03"), "        #")
	isChecking, err := getMdChecking()
	if err != nil {
		log.Printf("Error checking RAID status: %v", err)
	} else if isChecking > 0 {
		fmt.Println("# Currently Checking RAID         #")
		if timeLeft, err := getMdTimeLeft(); err == nil && timeLeft != "" {
			fmt.Printf("# Time left %-18s #\n", timeLeft)
		}
	}
	speed, err := getCurrentSpeed()
	if err != nil {
		log.Printf("Error reading current speed: %v", err)
	} else {
		switch speed {
		case normalSpeed:
			fmt.Println("# Speed Normal                 #")
		case highSpeed:
			fmt.Println("# Speed High                   #")
		case lowSpeed:
			fmt.Println("# Speed Low                    #")
		}
	}
	fmt.Println("################################")
}

func showStatus() {
	fmt.Println("################################")
	currentTime := time.Now()
	fmt.Println("#", currentTime.Format("2006-01-02 15:04:03"), "         #")
	isChecking, err := getMdChecking()
	if err != nil {
		log.Printf("Error checking RAID status: %v", err)
	} else if isChecking > 0 {
		fmt.Println("# Currently Checking Raid      #")
		if timeLeft, err := getMdTimeLeft(); err == nil && timeLeft != "" {
			fmt.Printf("# Time left %-18s #\n", timeLeft)
		}
		if progress, err := getMdProgress(); err == nil {
			progressBar := drawProgressBar(progress, 20)
			fmt.Printf("# %-24s #\n", progressBar)
		}
	}

	speed, err := getCurrentSpeed()
	if err != nil {
		log.Printf("Error reading current speed: %v", err)
	} else {
		switch speed {
		case normalSpeed:
			fmt.Println("# Speed Normal                 #")
		case highSpeed:
			fmt.Println("# Speed High                   #")
		case lowSpeed:
			fmt.Println("# Speed Low                    #")
		}
	}

	fmt.Println("################################")
}

func waitForRaidAndReboot(forced bool) {
	for {
		count, err := getMdChecking()
		if err != nil {
			log.Printf("Error checking RAID status: %v", err)
			time.Sleep(10 * time.Second)
			continue
		}

		if count == 0 {
			break
		}

		time.Sleep(100 * time.Second)

		// Clear screen (simple version)
		fmt.Print("\033[2J\033[H")

		timeLeft, err := getMdTimeLeft()
		if err != nil {
			log.Printf("Error getting time left: %v", err)
		}

		fmt.Println(time.Now().Format("Mon Jan 2 15:04:05 MST 2006"))
		if timeLeft != "" {
			fmt.Printf("Reboot will occur in %s\n", timeLeft)
		} else {
			fmt.Println("Reboot will occur when RAID check completes")
		}

		if progress, err := getMdProgress(); err == nil {
			fmt.Println(drawProgressBar(progress, 50))
		}
	}

	// Final check
	count, err := getMdChecking()
	if err != nil {
		log.Fatalf("Error in final RAID check: %v", err)
	}

	if count == 0 {
		fmt.Println("RAID check complete. Rebooting...")
		cmd := exec.Command("reboot")
		if err := cmd.Run(); err != nil {
			log.Fatalf("Error executing reboot: %v", err)
		}
	}
}
