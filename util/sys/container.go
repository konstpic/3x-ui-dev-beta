//go:build linux
// +build linux

package sys

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ContainerStats represents container resource statistics
type ContainerStats struct {
	MemoryUsed  uint64
	MemoryLimit uint64
	CPUQuota    int64  // in microseconds
	CPUPeriod   uint64 // in microseconds
	IsContainer bool
}

// GetContainerStats attempts to read container resource limits from cgroup v1 or v2
// Returns nil if not running in a container or if cgroup files are not accessible
func GetContainerStats() *ContainerStats {
	stats := &ContainerStats{IsContainer: false}

	// Try cgroup v2 first (newer systems)
	if stats.tryCgroupV2() {
		stats.IsContainer = true
		return stats
	}

	// Fall back to cgroup v1
	if stats.tryCgroupV1() {
		stats.IsContainer = true
		return stats
	}

	return nil
}

// tryCgroupV2 attempts to read from cgroup v2 (unified hierarchy)
func (s *ContainerStats) tryCgroupV2() bool {
	// Check if cgroup v2 is mounted
	cgroupRoot := "/sys/fs/cgroup"
	memoryCurrent := filepath.Join(cgroupRoot, "memory.current")
	memoryMax := filepath.Join(cgroupRoot, "memory.max")
	cpuMax := filepath.Join(cgroupRoot, "cpu.max")

	// Check if files exist
	if _, err := os.Stat(memoryCurrent); err != nil {
		return false
	}

	// Read memory.current
	if data, err := os.ReadFile(memoryCurrent); err == nil {
		if val, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64); err == nil {
			s.MemoryUsed = val
		}
	}

	// Read memory.max (can be "max" for unlimited)
	if data, err := os.ReadFile(memoryMax); err == nil {
		maxStr := strings.TrimSpace(string(data))
		if maxStr == "max" {
			s.MemoryLimit = 0 // 0 means unlimited
		} else if val, err := strconv.ParseUint(maxStr, 10, 64); err == nil {
			s.MemoryLimit = val
		}
	}

	// Read cpu.max (format: "quota period" or "max" for unlimited)
	if data, err := os.ReadFile(cpuMax); err == nil {
		maxStr := strings.TrimSpace(string(data))
		if maxStr != "max" {
			parts := strings.Fields(maxStr)
			if len(parts) == 2 {
				if quota, err := strconv.ParseInt(parts[0], 10, 64); err == nil {
					s.CPUQuota = quota
				}
				if period, err := strconv.ParseUint(parts[1], 10, 64); err == nil {
					s.CPUPeriod = period
				}
			}
		}
	}

	return s.MemoryUsed > 0 || s.MemoryLimit > 0 || s.CPUQuota > 0
}

// tryCgroupV1 attempts to read from cgroup v1 (legacy hierarchy)
func (s *ContainerStats) tryCgroupV1() bool {
	// Try to find the cgroup mount point
	cgroupPaths := []string{
		"/sys/fs/cgroup/memory",
		"/sys/fs/cgroup/cpu",
	}

	memoryUsed := uint64(0)
	memoryLimit := uint64(0)
	cpuQuota := int64(0)
	cpuPeriod := uint64(0)
	found := false

	// Try to find memory cgroup
	for _, basePath := range cgroupPaths {
		if strings.Contains(basePath, "memory") {
			// Try to find the actual cgroup path for this process
			cgroupPath := s.findCgroupPath(basePath, "memory")
			if cgroupPath != "" {
				memoryUsageFile := filepath.Join(cgroupPath, "memory.usage_in_bytes")
				memoryLimitFile := filepath.Join(cgroupPath, "memory.limit_in_bytes")

				if data, err := os.ReadFile(memoryUsageFile); err == nil {
					if val, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64); err == nil {
						memoryUsed = val
						found = true
					}
				}

				if data, err := os.ReadFile(memoryLimitFile); err == nil {
					limitStr := strings.TrimSpace(string(data))
					// 9223372036854771712 is typically "unlimited" in cgroup v1
					if limitStr != "9223372036854771712" {
						if val, err := strconv.ParseUint(limitStr, 10, 64); err == nil {
							memoryLimit = val
						}
					}
				}
			}
		}

		if strings.Contains(basePath, "cpu") {
			cgroupPath := s.findCgroupPath(basePath, "cpu")
			if cgroupPath != "" {
				cpuQuotaFile := filepath.Join(cgroupPath, "cpu.cfs_quota_us")
				cpuPeriodFile := filepath.Join(cgroupPath, "cpu.cfs_period_us")

				if data, err := os.ReadFile(cpuQuotaFile); err == nil {
					if val, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64); err == nil && val > 0 {
						cpuQuota = val
						found = true
					}
				}

				if data, err := os.ReadFile(cpuPeriodFile); err == nil {
					if val, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64); err == nil {
						cpuPeriod = val
					}
				}
			}
		}
	}

	if found {
		s.MemoryUsed = memoryUsed
		s.MemoryLimit = memoryLimit
		s.CPUQuota = cpuQuota
		s.CPUPeriod = cpuPeriod
		return true
	}

	return false
}

// findCgroupPath attempts to find the cgroup path for the current process
func (s *ContainerStats) findCgroupPath(basePath, subsystem string) string {
	// Read /proc/self/cgroup to find our cgroup path
	cgroupFile := "/proc/self/cgroup"
	file, err := os.Open(cgroupFile)
	if err != nil {
		return ""
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// Format: hierarchy-ID:subsystems:path
		parts := strings.Split(line, ":")
		if len(parts) >= 3 {
			subsystems := parts[1]
			path := parts[2]

			// Check if this line contains the subsystem we're looking for
			if strings.Contains(subsystems, subsystem) || subsystems == "" {
				// Try the path
				fullPath := filepath.Join(basePath, path)
				if _, err := os.Stat(fullPath); err == nil {
					return fullPath
				}
			}
		}
	}

	// Fallback: try common Docker paths
	dockerPaths := []string{
		filepath.Join(basePath, "docker"),
		filepath.Join(basePath, "system.slice"),
	}

	for _, dockerPath := range dockerPaths {
		if _, err := os.Stat(dockerPath); err == nil {
			// Try to find a subdirectory that might be our container
			entries, err := os.ReadDir(dockerPath)
			if err == nil {
				for _, entry := range entries {
					if entry.IsDir() {
						testPath := filepath.Join(dockerPath, entry.Name())
						if _, err := os.Stat(testPath); err == nil {
							return testPath
						}
					}
				}
			}
		}
	}

	return ""
}

// GetContainerCPUPercent calculates CPU usage percentage based on container limits
// Returns the percentage (0-100) if container limits are found, otherwise returns -1
func GetContainerCPUPercent() (float64, error) {
	stats := GetContainerStats()
	if stats == nil || !stats.IsContainer || stats.CPUQuota <= 0 || stats.CPUPeriod == 0 {
		return -1, fmt.Errorf("not in container or no CPU limits")
	}

	// Get actual CPU usage using the standard method
	cpuPercent, err := CPUPercentRaw()
	if err != nil {
		return -1, err
	}

	// Return CPU usage percentage
	// Note: CPU usage is already relative to available cores in the container
	return cpuPercent, nil
}
