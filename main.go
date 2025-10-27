package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	speedLimitPath = "/proc/sys/dev/raid/speed_limit_max"
	mdstatPath     = "/proc/mdstat"
	syncActionPath = "/sys/block/md0/md/sync_action"

	normalSpeed = "200000"
	highSpeed   = "2000000"
	lowSpeed    = "3000"
)

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
			re := regexp.MustCompile(`finish=([^\\s]+)`)
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

// setSyncAction writes to the sync_action file
func setSyncAction(action string) error {
	return os.WriteFile(syncActionPath, []byte(action), 0644)
}

func main() {
	var rootCmd = &cobra.Command{
		Use:   "raid-helper",
		Short: "A tool for managing Linux software RAID operations",
		Long:  "raid-helper provides commands to control RAID check speeds, start/stop operations, and manage reboots.",
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
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("Stopping raid check")
			if err := setSyncAction("idle"); err != nil {
				log.Fatalf("Error stopping raid check: %v", err)
			}
		},
	}

	var startCmd = &cobra.Command{
		Use:   "start",
		Short: "Start RAID check",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("Starting raid check")
			if err := setSyncAction("check"); err != nil {
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
		Run: func(cmd *cobra.Command, args []string) {
			waitForRaidAndReboot(false)
		},
	}

	var forceRebootCmd = &cobra.Command{
		Use:   "forcereboot",
		Short: "Stop RAID check and reboot",
		Run: func(cmd *cobra.Command, args []string) {
			if err := setSyncAction("idle"); err != nil {
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

	rootCmd.AddCommand(normalCmd, highCmd, lowCmd, stopCmd, startCmd, checkCmd, progressCmd, rebootCmd, forceRebootCmd, showStatusOnlyCmd)

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
	fmt.Println("Available commands:")
	fmt.Println("check       - Returns >0 if the raid is checking")
	fmt.Println("normal      - Set speed normal")
	fmt.Println("high        - Set speed high")
	fmt.Println("low        - Set speed low")
	fmt.Println("progress    - Show RAID check progress")
	fmt.Println("reboot      - Reboot the machine once the raid check is done")
	fmt.Println("forcereboot - Stop raid check and reboot")
	fmt.Println("stop        - Stop raid check")
	fmt.Println("start       - Start raid check")
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
