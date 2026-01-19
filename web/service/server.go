package service

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mhsanaei/3x-ui/v2/config"
	"github.com/mhsanaei/3x-ui/v2/database"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/util/common"
	"github.com/mhsanaei/3x-ui/v2/util/sys"
	"github.com/mhsanaei/3x-ui/v2/xray"

	"github.com/google/uuid"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
)

// ProcessState represents the current state of a system process.
type ProcessState string

// Process state constants
const (
	Running ProcessState = "running" // Process is running normally
	Stop    ProcessState = "stop"    // Process is stopped
	Error   ProcessState = "error"   // Process is in error state
)

// Status represents comprehensive system and application status information.
// It includes CPU, memory, disk, network statistics, and Xray process status.
type Status struct {
	T           time.Time `json:"-"`
	Cpu         float64   `json:"cpu"`
	CpuCores    int       `json:"cpuCores"`
	LogicalPro  int       `json:"logicalPro"`
	CpuSpeedMhz float64   `json:"cpuSpeedMhz"`
	Mem         struct {
		Current uint64 `json:"current"`
		Total   uint64 `json:"total"`
	} `json:"mem"`
	Swap struct {
		Current uint64 `json:"current"`
		Total   uint64 `json:"total"`
	} `json:"swap"`
	Disk struct {
		Current uint64 `json:"current"`
		Total   uint64 `json:"total"`
	} `json:"disk"`
	Xray struct {
		State    ProcessState `json:"state"`
		ErrorMsg string       `json:"errorMsg"`
		Version  string       `json:"version"`
	} `json:"xray"`
	Uptime   uint64    `json:"uptime"`
	Loads    []float64 `json:"loads"`
	TcpCount int       `json:"tcpCount"`
	UdpCount int       `json:"udpCount"`
	NetIO    struct {
		Up   uint64 `json:"up"`
		Down uint64 `json:"down"`
	} `json:"netIO"`
	NetTraffic struct {
		Sent uint64 `json:"sent"`
		Recv uint64 `json:"recv"`
	} `json:"netTraffic"`
	PublicIP struct {
		IPv4 string `json:"ipv4"`
		IPv6 string `json:"ipv6"`
	} `json:"publicIP"`
	AppStats struct {
		Threads uint32 `json:"threads"`
		Mem     uint64 `json:"mem"`
		Uptime  uint64 `json:"uptime"`
	} `json:"appStats"`
	Nodes struct {
		Online int `json:"online"`
		Total  int `json:"total"`
	} `json:"nodes"`
	Database struct {
		Size          uint64 `json:"size"`          // Database size in bytes
		Tables        int    `json:"tables"`        // Number of tables
		TotalRows     uint64 `json:"totalRows"`     // Total number of rows across all tables
		OpenConns     int    `json:"openConns"`     // Current open connections
		IdleConns     int    `json:"idleConns"`     // Current idle connections
		MaxOpenConns  int    `json:"maxOpenConns"`  // Maximum open connections
		MaxIdleConns  int    `json:"maxIdleConns"`  // Maximum idle connections
	} `json:"database"`
}

// Release represents information about a software release from GitHub.
type Release struct {
	TagName string `json:"tag_name"` // The tag name of the release
}

// ServerService provides business logic for server monitoring and management.
// It handles system status collection, IP detection, and application statistics.
type ServerService struct {
	xrayService        XrayService
	inboundService     InboundService
	cachedIPv4         string
	cachedIPv6         string
	noIPv6             bool
	mu                 sync.Mutex
	lastCPUTimes       cpu.TimesStat
	hasLastCPUSample   bool
	hasNativeCPUSample bool
	emaCPU             float64
	cpuHistory         []CPUSample
	cachedCpuSpeedMhz  float64
	lastCpuInfoAttempt time.Time
}

// AggregateCpuHistory returns up to maxPoints averaged buckets of size bucketSeconds over recent data.
func (s *ServerService) AggregateCpuHistory(bucketSeconds int, maxPoints int) []map[string]any {
	if bucketSeconds <= 0 || maxPoints <= 0 {
		return nil
	}
	cutoff := time.Now().Add(-time.Duration(bucketSeconds*maxPoints) * time.Second).Unix()
	s.mu.Lock()
	// find start index (history sorted ascending)
	hist := s.cpuHistory
	// binary-ish scan (simple linear from end since size capped ~10800 is fine)
	startIdx := 0
	for i := len(hist) - 1; i >= 0; i-- {
		if hist[i].T < cutoff {
			startIdx = i + 1
			break
		}
	}
	if startIdx >= len(hist) {
		s.mu.Unlock()
		return []map[string]any{}
	}
	slice := hist[startIdx:]
	// copy for unlock
	tmp := make([]CPUSample, len(slice))
	copy(tmp, slice)
	s.mu.Unlock()
	if len(tmp) == 0 {
		return []map[string]any{}
	}
	var out []map[string]any
	var acc []float64
	bSize := int64(bucketSeconds)
	curBucket := (tmp[0].T / bSize) * bSize
	flush := func(ts int64) {
		if len(acc) == 0 {
			return
		}
		sum := 0.0
		for _, v := range acc {
			sum += v
		}
		avg := sum / float64(len(acc))
		out = append(out, map[string]any{"t": ts, "cpu": avg})
		acc = acc[:0]
	}
	for _, p := range tmp {
		b := (p.T / bSize) * bSize
		if b != curBucket {
			flush(curBucket)
			curBucket = b
		}
		acc = append(acc, p.Cpu)
	}
	flush(curBucket)
	if len(out) > maxPoints {
		out = out[len(out)-maxPoints:]
	}
	return out
}

// CPUSample single CPU utilization sample
type CPUSample struct {
	T   int64   `json:"t"`   // unix seconds
	Cpu float64 `json:"cpu"` // percent 0..100
}

type LogEntry struct {
	DateTime    time.Time
	FromAddress string
	ToAddress   string
	Inbound     string
	Outbound    string
	Email       string
	Event       int
}

func getPublicIP(url string) string {
	client := &http.Client{
		Timeout: 3 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		return "N/A"
	}
	defer resp.Body.Close()

	// Don't retry if access is blocked or region-restricted
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnavailableForLegalReasons {
		return "N/A"
	}
	if resp.StatusCode != http.StatusOK {
		return "N/A"
	}

	ip, err := io.ReadAll(resp.Body)
	if err != nil {
		return "N/A"
	}

	ipString := strings.TrimSpace(string(ip))
	if ipString == "" {
		return "N/A"
	}

	return ipString
}

func (s *ServerService) GetStatus(lastStatus *Status) *Status {
	now := time.Now()
	status := &Status{
		T: now,
	}

	// Check if running in container (once for both CPU and memory)
	containerStats := sys.GetContainerStats()

	// CPU stats - try container first, fallback to host
	var err error
	if containerStats != nil && containerStats.IsContainer && containerStats.CPUQuota > 0 && containerStats.CPUPeriod > 0 {
		// Calculate available CPU cores from container limits
		availableCores := float64(containerStats.CPUQuota) / float64(containerStats.CPUPeriod)
		status.CpuCores = int(availableCores)
		if status.CpuCores == 0 {
			status.CpuCores = 1 // At least 1 core
		}
		status.LogicalPro = status.CpuCores
	} else {
		// Not in container or no limits, use host CPU
		status.CpuCores, err = cpu.Counts(false)
		if err != nil {
			logger.Warning("get cpu cores count failed:", err)
		}
		status.LogicalPro = runtime.NumCPU()
	}

	util, err := s.sampleCPUUtilization()
	if err != nil {
		logger.Warning("get cpu percent failed:", err)
	} else {
		status.Cpu = util
	}

	if status.CpuSpeedMhz = s.cachedCpuSpeedMhz; s.cachedCpuSpeedMhz == 0 && time.Since(s.lastCpuInfoAttempt) > 5*time.Minute {
		s.lastCpuInfoAttempt = time.Now()
		done := make(chan struct{})
		go func() {
			defer close(done)
			cpuInfos, err := cpu.Info()
			if err != nil {
				logger.Warning("get cpu info failed:", err)
				return
			}
			if len(cpuInfos) > 0 {
				s.cachedCpuSpeedMhz = cpuInfos[0].Mhz
				status.CpuSpeedMhz = s.cachedCpuSpeedMhz
			} else {
				logger.Warning("could not find cpu info")
			}
		}()
		select {
		case <-done:
		case <-time.After(1500 * time.Millisecond):
			logger.Warning("cpu info query timed out; will retry later")
		}
	} else if s.cachedCpuSpeedMhz != 0 {
		status.CpuSpeedMhz = s.cachedCpuSpeedMhz
	}

	// Uptime
	upTime, err := host.Uptime()
	if err != nil {
		logger.Warning("get uptime failed:", err)
	} else {
		status.Uptime = upTime
	}

	// Memory stats - try container first, fallback to host
	if containerStats != nil && containerStats.IsContainer {
		// Use container memory limits
		status.Mem.Current = containerStats.MemoryUsed
		if containerStats.MemoryLimit > 0 {
			status.Mem.Total = containerStats.MemoryLimit
		} else {
			// If limit is 0 (unlimited), fall back to host memory
			memInfo, err := mem.VirtualMemory()
			if err == nil {
				status.Mem.Total = memInfo.Total
			}
		}
	} else {
		// Not in container, use host memory
		memInfo, err := mem.VirtualMemory()
		if err != nil {
			logger.Warning("get virtual memory failed:", err)
		} else {
			status.Mem.Current = memInfo.Used
			status.Mem.Total = memInfo.Total
		}
	}

	swapInfo, err := mem.SwapMemory()
	if err != nil {
		logger.Warning("get swap memory failed:", err)
	} else {
		status.Swap.Current = swapInfo.Used
		status.Swap.Total = swapInfo.Total
	}

	// Disk stats
	diskInfo, err := disk.Usage("/")
	if err != nil {
		logger.Warning("get disk usage failed:", err)
	} else {
		status.Disk.Current = diskInfo.Used
		status.Disk.Total = diskInfo.Total
	}

	// Load averages
	avgState, err := load.Avg()
	if err != nil {
		logger.Warning("get load avg failed:", err)
	} else {
		status.Loads = []float64{avgState.Load1, avgState.Load5, avgState.Load15}
	}

	// Network stats
	ioStats, err := net.IOCounters(false)
	if err != nil {
		logger.Warning("get io counters failed:", err)
	} else if len(ioStats) > 0 {
		ioStat := ioStats[0]
		status.NetTraffic.Sent = ioStat.BytesSent
		status.NetTraffic.Recv = ioStat.BytesRecv

		if lastStatus != nil {
			duration := now.Sub(lastStatus.T)
			seconds := float64(duration) / float64(time.Second)
			up := uint64(float64(status.NetTraffic.Sent-lastStatus.NetTraffic.Sent) / seconds)
			down := uint64(float64(status.NetTraffic.Recv-lastStatus.NetTraffic.Recv) / seconds)
			status.NetIO.Up = up
			status.NetIO.Down = down
		}
	} else {
		logger.Warning("can not find io counters")
	}

	// TCP/UDP connections
	status.TcpCount, err = sys.GetTCPCount()
	if err != nil {
		logger.Warning("get tcp connections failed:", err)
	}

	status.UdpCount, err = sys.GetUDPCount()
	if err != nil {
		logger.Warning("get udp connections failed:", err)
	}

	// IP fetching with caching
	showIp4ServiceLists := []string{
		"https://api4.ipify.org",
		"https://ipv4.icanhazip.com",
		"https://v4.api.ipinfo.io/ip",
		"https://ipv4.myexternalip.com/raw",
		"https://4.ident.me",
		"https://check-host.net/ip",
	}
	showIp6ServiceLists := []string{
		"https://api6.ipify.org",
		"https://ipv6.icanhazip.com",
		"https://v6.api.ipinfo.io/ip",
		"https://ipv6.myexternalip.com/raw",
		"https://6.ident.me",
	}

	if s.cachedIPv4 == "" {
		for _, ip4Service := range showIp4ServiceLists {
			s.cachedIPv4 = getPublicIP(ip4Service)
			if s.cachedIPv4 != "N/A" {
				break
			}
		}
	}

	if s.cachedIPv6 == "" && !s.noIPv6 {
		for _, ip6Service := range showIp6ServiceLists {
			s.cachedIPv6 = getPublicIP(ip6Service)
			if s.cachedIPv6 != "N/A" {
				break
			}
		}
	}

	if s.cachedIPv6 == "N/A" {
		s.noIPv6 = true
	}

	status.PublicIP.IPv4 = s.cachedIPv4
	status.PublicIP.IPv6 = s.cachedIPv6

	// Xray status
	if s.xrayService.IsXrayRunning() {
		status.Xray.State = Running
		status.Xray.ErrorMsg = ""
	} else {
		err := s.xrayService.GetXrayErr()
		if err != nil {
			status.Xray.State = Error
		} else {
			status.Xray.State = Stop
		}
		status.Xray.ErrorMsg = s.xrayService.GetXrayResult()
	}
	status.Xray.Version = s.xrayService.GetXrayVersion()

	// Application stats
	var rtm runtime.MemStats
	runtime.ReadMemStats(&rtm)
	status.AppStats.Mem = rtm.Sys
	status.AppStats.Threads = uint32(runtime.NumGoroutine())
	if p != nil && p.IsRunning() {
		status.AppStats.Uptime = p.GetUptime()
	} else {
		status.AppStats.Uptime = 0
	}

	// Node statistics (only if multi-node mode is enabled)
	settingService := SettingService{}
	allSetting, err := settingService.GetAllSetting()
	if err == nil && allSetting != nil && allSetting.MultiNodeMode {
		nodeService := NodeService{}
		nodes, err := nodeService.GetAllNodes()
		if err == nil {
			status.Nodes.Total = len(nodes)
			onlineCount := 0
			for _, node := range nodes {
				if node.Status == "online" {
					onlineCount++
				}
			}
			status.Nodes.Online = onlineCount
		} else {
			// If error getting nodes, set to 0
			status.Nodes.Total = 0
			status.Nodes.Online = 0
		}
	} else {
		// If multi-node mode is disabled, set to 0
		status.Nodes.Total = 0
		status.Nodes.Online = 0
	}

	// Database statistics
	dbStats := s.getDatabaseStats()
	status.Database = dbStats

	return status
}

func (s *ServerService) AppendCpuSample(t time.Time, v float64) {
	const capacity = 9000 // ~5 hours @ 2s interval
	s.mu.Lock()
	defer s.mu.Unlock()
	p := CPUSample{T: t.Unix(), Cpu: v}
	if n := len(s.cpuHistory); n > 0 && s.cpuHistory[n-1].T == p.T {
		s.cpuHistory[n-1] = p
	} else {
		s.cpuHistory = append(s.cpuHistory, p)
	}
	if len(s.cpuHistory) > capacity {
		s.cpuHistory = s.cpuHistory[len(s.cpuHistory)-capacity:]
	}
}

func (s *ServerService) sampleCPUUtilization() (float64, error) {
	// Try native platform-specific CPU implementation first (Windows, Linux, macOS)
	if pct, err := sys.CPUPercentRaw(); err == nil {
		s.mu.Lock()
		// First call to native method returns 0 (initializes baseline)
		if !s.hasNativeCPUSample {
			s.hasNativeCPUSample = true
			s.mu.Unlock()
			return 0, nil
		}
		// Smooth with EMA
		const alpha = 0.3
		if s.emaCPU == 0 {
			s.emaCPU = pct
		} else {
			s.emaCPU = alpha*pct + (1-alpha)*s.emaCPU
		}
		val := s.emaCPU
		s.mu.Unlock()
		return val, nil
	}
	// If native call fails, fall back to gopsutil times
	// Read aggregate CPU times (all CPUs combined)
	times, err := cpu.Times(false)
	if err != nil {
		return 0, err
	}
	if len(times) == 0 {
		return 0, fmt.Errorf("no cpu times available")
	}

	cur := times[0]

	s.mu.Lock()
	defer s.mu.Unlock()

	// If this is the first sample, initialize and return current EMA (0 by default)
	if !s.hasLastCPUSample {
		s.lastCPUTimes = cur
		s.hasLastCPUSample = true
		return s.emaCPU, nil
	}

	// Compute busy and total deltas
	// Note: Guest and GuestNice times are already included in User and Nice respectively,
	// so we exclude them to avoid double-counting (Linux kernel accounting)
	idleDelta := cur.Idle - s.lastCPUTimes.Idle
	busyDelta := (cur.User - s.lastCPUTimes.User) +
		(cur.System - s.lastCPUTimes.System) +
		(cur.Nice - s.lastCPUTimes.Nice) +
		(cur.Iowait - s.lastCPUTimes.Iowait) +
		(cur.Irq - s.lastCPUTimes.Irq) +
		(cur.Softirq - s.lastCPUTimes.Softirq) +
		(cur.Steal - s.lastCPUTimes.Steal)

	totalDelta := busyDelta + idleDelta

	// Update last sample for next time
	s.lastCPUTimes = cur

	// Guard against division by zero or negative deltas (e.g., counter resets)
	if totalDelta <= 0 {
		return s.emaCPU, nil
	}

	raw := 100.0 * (busyDelta / totalDelta)
	if raw < 0 {
		raw = 0
	}
	if raw > 100 {
		raw = 100
	}

	// Exponential moving average to smooth spikes
	const alpha = 0.3 // smoothing factor (0<alpha<=1). Higher = more responsive, lower = smoother
	if s.emaCPU == 0 {
		// Initialize EMA with the first real reading to avoid long warm-up from zero
		s.emaCPU = raw
	} else {
		s.emaCPU = alpha*raw + (1-alpha)*s.emaCPU
	}

	return s.emaCPU, nil
}

func (s *ServerService) GetXrayVersions() ([]string, error) {
	const (
		XrayURL    = "https://api.github.com/repos/XTLS/Xray-core/releases"
		bufferSize = 8192
	)

	resp, err := http.Get(XrayURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Check HTTP status code - GitHub API returns object instead of array on error
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		var errorResponse struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(bodyBytes, &errorResponse) == nil && errorResponse.Message != "" {
			return nil, fmt.Errorf("GitHub API error: %s", errorResponse.Message)
		}
		return nil, fmt.Errorf("GitHub API returned status %d: %s", resp.StatusCode, resp.Status)
	}

	buffer := bytes.NewBuffer(make([]byte, bufferSize))
	buffer.Reset()
	if _, err := buffer.ReadFrom(resp.Body); err != nil {
		return nil, err
	}

	var releases []Release
	if err := json.Unmarshal(buffer.Bytes(), &releases); err != nil {
		return nil, err
	}

	var versions []string
	for _, release := range releases {
		tagVersion := strings.TrimPrefix(release.TagName, "v")
		tagParts := strings.Split(tagVersion, ".")
		if len(tagParts) != 3 {
			continue
		}

		major, err1 := strconv.Atoi(tagParts[0])
		minor, err2 := strconv.Atoi(tagParts[1])
		patch, err3 := strconv.Atoi(tagParts[2])
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}

		if major > 25 || (major == 25 && minor > 9) || (major == 25 && minor == 9 && patch >= 11) {
			versions = append(versions, release.TagName)
		}
	}
	return versions, nil
}

func (s *ServerService) StopXrayService() error {
	// Check if Xray is running before trying to stop it
	if !s.xrayService.IsXrayRunning() {
		return nil // Xray is not running, nothing to stop
	}
	err := s.xrayService.StopXray()
	if err != nil {
		logger.Error("stop xray failed:", err)
		return err
	}
	return nil
}

func (s *ServerService) RestartXrayService() error {
	err := s.xrayService.RestartXray(true)
	if err != nil {
		logger.Error("start xray failed:", err)
		return err
	}
	return nil
}

func (s *ServerService) downloadXRay(version string) (string, error) {
	osName := runtime.GOOS
	arch := runtime.GOARCH

	switch osName {
	case "darwin":
		osName = "macos"
	case "windows":
		osName = "windows"
	}

	switch arch {
	case "amd64":
		arch = "64"
	case "arm64":
		arch = "arm64-v8a"
	case "armv7":
		arch = "arm32-v7a"
	case "armv6":
		arch = "arm32-v6"
	case "armv5":
		arch = "arm32-v5"
	case "386":
		arch = "32"
	case "s390x":
		arch = "s390x"
	}

	fileName := fmt.Sprintf("Xray-%s-%s.zip", osName, arch)
	url := fmt.Sprintf("https://github.com/XTLS/Xray-core/releases/download/%s/%s", version, fileName)
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	os.Remove(fileName)
	file, err := os.Create(fileName)
	if err != nil {
		return "", err
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return "", err
	}

	return fileName, nil
}

func (s *ServerService) UpdateXray(version string) error {
	// 1. Stop xray before doing anything (only if it's running)
	wasRunning := s.xrayService.IsXrayRunning()
	if wasRunning {
		if err := s.StopXrayService(); err != nil {
			logger.Warning("failed to stop xray before update:", err)
		}
	}

	// 2. Download the zip
	zipFileName, err := s.downloadXRay(version)
	if err != nil {
		return err
	}
	defer os.Remove(zipFileName)

	zipFile, err := os.Open(zipFileName)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	stat, err := zipFile.Stat()
	if err != nil {
		return err
	}
	reader, err := zip.NewReader(zipFile, stat.Size())
	if err != nil {
		return err
	}

	// 3. Helper to extract files
	copyZipFile := func(zipName string, fileName string) error {
		zipFile, err := reader.Open(zipName)
		if err != nil {
			return err
		}
		defer zipFile.Close()
		os.MkdirAll(filepath.Dir(fileName), 0755)
		os.Remove(fileName)
		file, err := os.OpenFile(fileName, os.O_CREATE|os.O_RDWR|os.O_TRUNC, fs.ModePerm)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(file, zipFile)
		return err
	}

	// 4. Extract correct binary
	if runtime.GOOS == "windows" {
		targetBinary := filepath.Join("bin", "xray-windows-amd64.exe")
		err = copyZipFile("xray.exe", targetBinary)
	} else {
		err = copyZipFile("xray", xray.GetBinaryPath())
	}
	if err != nil {
		return err
	}

	// 5. Restart xray only if it was running before (in multi-node mode, xray may not be running)
	if wasRunning {
		if err := s.xrayService.RestartXray(true); err != nil {
			logger.Error("start xray failed:", err)
			return err
		}
	} else {
		logger.Info("Xray was not running, skipping restart (multi-node mode)")
	}

	return nil
}

func (s *ServerService) GetLogs(count string, level string, syslog string) []string {
	c, _ := strconv.Atoi(count)
	var lines []string

	if syslog == "true" {
		// Check if running on Windows - journalctl is not available
		if runtime.GOOS == "windows" {
			return []string{"Syslog is not supported on Windows. Please use application logs instead by unchecking the 'Syslog' option."}
		}

		// Validate and sanitize count parameter
		countInt, err := strconv.Atoi(count)
		if err != nil || countInt < 1 || countInt > 10000 {
			return []string{"Invalid count parameter - must be a number between 1 and 10000"}
		}

		// Validate level parameter - only allow valid syslog levels
		validLevels := map[string]bool{
			"0": true, "emerg": true,
			"1": true, "alert": true,
			"2": true, "crit": true,
			"3": true, "err": true,
			"4": true, "warning": true,
			"5": true, "notice": true,
			"6": true, "info": true,
			"7": true, "debug": true,
		}
		if !validLevels[level] {
			return []string{"Invalid level parameter - must be a valid syslog level"}
		}

		// Use hardcoded command with validated parameters
		cmd := exec.Command("journalctl", "-u", "x-ui", "--no-pager", "-n", strconv.Itoa(countInt), "-p", level)
		var out bytes.Buffer
		cmd.Stdout = &out
		err = cmd.Run()
		if err != nil {
			return []string{"Failed to run journalctl command! Make sure systemd is available and x-ui service is registered."}
		}
		lines = strings.Split(out.String(), "\n")
	} else {
		lines = logger.GetLogs(c, level)
	}

	return lines
}

func (s *ServerService) GetXrayLogs(
	count string,
	filter string,
	showDirect string,
	showBlocked string,
	showProxy string,
	freedoms []string,
	blackholes []string,
	nodeId string) []LogEntry {

	const (
		Direct = iota
		Blocked
		Proxied
	)

	countInt, _ := strconv.Atoi(count)
	var entries []LogEntry

	// Check if multi-node mode is enabled
	settingService := SettingService{}
	multiMode, err := settingService.GetMultiNodeMode()
	if err == nil && multiMode {
		// In multi-node mode, get logs from node
		if nodeId != "" {
			nodeIdInt, err := strconv.Atoi(nodeId)
			if err == nil {
				nodeService := NodeService{}
				node, err := nodeService.GetNode(nodeIdInt)
				if err == nil && node != nil {
					// Get raw logs from node
					rawLogs, err := nodeService.GetNodeLogs(node, countInt, filter)
					if err == nil {
						// Parse logs into LogEntry format
						for _, line := range rawLogs {
							var entry LogEntry
							parts := strings.Fields(line)

							for i, part := range parts {
								if i == 0 {
									if len(parts) > 1 {
										dateTime, err := time.ParseInLocation("2006/01/02 15:04:05.999999", parts[0]+" "+parts[1], time.Local)
										if err == nil {
											entry.DateTime = dateTime.UTC()
										}
									}
								}

								if part == "from" && i+1 < len(parts) {
									entry.FromAddress = strings.TrimLeft(parts[i+1], "/")
								} else if part == "accepted" && i+1 < len(parts) {
									entry.ToAddress = strings.TrimLeft(parts[i+1], "/")
								} else if strings.HasPrefix(part, "[") {
									entry.Inbound = part[1:]
								} else if strings.HasSuffix(part, "]") {
									entry.Outbound = part[:len(part)-1]
								} else if part == "email:" && i+1 < len(parts) {
									entry.Email = parts[i+1]
								}
							}

							// Determine event type
							if logEntryContains(line, freedoms) {
								if showDirect == "false" {
									continue
								}
								entry.Event = Direct
							} else if logEntryContains(line, blackholes) {
								if showBlocked == "false" {
									continue
								}
								entry.Event = Blocked
							} else {
								if showProxy == "false" {
									continue
								}
								entry.Event = Proxied
							}

							entries = append(entries, entry)
						}
					}
				}
			}
		}
		// If no nodeId provided or node not found, return empty
		return entries
	}

	pathToAccessLog, err := xray.GetAccessLogPath()
	if err != nil {
		return nil
	}

	file, err := os.Open(pathToAccessLog)
	if err != nil {
		return nil
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" || strings.Contains(line, "api -> api") {
			//skipping empty lines and api calls
			continue
		}

		if filter != "" && !strings.Contains(line, filter) {
			//applying filter if it's not empty
			continue
		}

		var entry LogEntry
		parts := strings.Fields(line)

		for i, part := range parts {

			if i == 0 {
				dateTime, err := time.ParseInLocation("2006/01/02 15:04:05.999999", parts[0]+" "+parts[1], time.Local)
				if err != nil {
					continue
				}
				entry.DateTime = dateTime.UTC()
			}

			if part == "from" {
				entry.FromAddress = strings.TrimLeft(parts[i+1], "/")
			} else if part == "accepted" {
				entry.ToAddress = strings.TrimLeft(parts[i+1], "/")
			} else if strings.HasPrefix(part, "[") {
				entry.Inbound = part[1:]
			} else if strings.HasSuffix(part, "]") {
				entry.Outbound = part[:len(part)-1]
			} else if part == "email:" {
				entry.Email = parts[i+1]
			}
		}

		if logEntryContains(line, freedoms) {
			if showDirect == "false" {
				continue
			}
			entry.Event = Direct
		} else if logEntryContains(line, blackholes) {
			if showBlocked == "false" {
				continue
			}
			entry.Event = Blocked
		} else {
			if showProxy == "false" {
				continue
			}
			entry.Event = Proxied
		}

		entries = append(entries, entry)
	}

	if len(entries) > countInt {
		entries = entries[len(entries)-countInt:]
	}

	return entries
}

func logEntryContains(line string, suffixes []string) bool {
	for _, sfx := range suffixes {
		if strings.Contains(line, sfx+"]") {
			return true
		}
	}
	return false
}

func (s *ServerService) GetConfigJson() (any, error) {
	config, err := s.xrayService.GetXrayConfig()
	if err != nil {
		return nil, err
	}
	contents, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, err
	}

	var jsonData any
	err = json.Unmarshal(contents, &jsonData)
	if err != nil {
		return nil, err
	}

	return jsonData, nil
}

func (s *ServerService) GetDb() ([]byte, error) {
	// Try to use pg_dump first if available
	host := config.GetDBHost()
	port := config.GetDBPort()
	user := config.GetDBUser()
	password := config.GetDBPassword()
	dbname := config.GetDBName()
	
	// Set PGPASSWORD environment variable for pg_dump
	env := os.Environ()
	env = append(env, fmt.Sprintf("PGPASSWORD=%s", password))
	
	// Build pg_dump command with --clean and --if-exists for proper restore
	cmd := exec.Command("pg_dump", 
		"-h", host,
		"-p", strconv.Itoa(port),
		"-U", user,
		"-d", dbname,
		"--format=plain",
		"--no-owner",
		"--no-privileges",
		"--clean",
		"--if-exists",
	)
	cmd.Env = env
	
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	
	err := cmd.Run()
	if err == nil {
		// pg_dump succeeded, return the output
		return stdout.Bytes(), nil
	}
	
	// pg_dump failed (likely not installed), fall back to GORM-based export
	logger.Warningf("pg_dump not available, falling back to GORM-based export: %v", err)
	return s.exportDbViaGORM()
}

// exportDbViaGORM exports the database using GORM and raw SQL queries
func (s *ServerService) exportDbViaGORM() ([]byte, error) {
	db := database.GetDB()
	if db == nil {
		return nil, fmt.Errorf("database connection is not available")
	}
	
	var dump strings.Builder
	
	// Write header
	dump.WriteString("-- PostgreSQL database dump\n")
	dump.WriteString(fmt.Sprintf("-- Dumped at %s\n", time.Now().Format("2006-01-02 15:04:05")))
	dump.WriteString("-- Using GORM-based export\n\n")
	dump.WriteString("SET statement_timeout = 0;\n")
	dump.WriteString("SET lock_timeout = 0;\n")
	dump.WriteString("SET idle_in_transaction_session_timeout = 0;\n")
	dump.WriteString("SET client_encoding = 'UTF8';\n")
	dump.WriteString("SET standard_conforming_strings = on;\n")
	dump.WriteString("SELECT pg_catalog.set_config('search_path', '', false);\n")
	dump.WriteString("SET check_function_bodies = false;\n")
	dump.WriteString("SET xmloption = content;\n")
	dump.WriteString("SET client_min_messages = warning;\n")
	dump.WriteString("SET row_security = off;\n\n")
	
	// Get list of all tables
	var tables []struct {
		TableName string `gorm:"column:tablename"`
	}
	err := db.Raw(`
		SELECT tablename 
		FROM pg_tables 
		WHERE schemaname = 'public' 
		ORDER BY tablename
	`).Scan(&tables).Error
	if err != nil {
		return nil, fmt.Errorf("failed to get table list: %v", err)
	}
	
	// Export each table
	for _, table := range tables {
		tableName := table.TableName
		
		// Get table structure using pg_get_tabledef or manual construction
		// First, try to get the table definition using a simpler approach
		var columns []struct {
			ColumnName     string  `gorm:"column:column_name"`
			DataType       string  `gorm:"column:data_type"`
			CharMaxLength  *int    `gorm:"column:character_maximum_length"`
			NumericPrec    *int    `gorm:"column:numeric_precision"`
			NumericScale   *int    `gorm:"column:numeric_scale"`
			IsNullable     string  `gorm:"column:is_nullable"`
			ColumnDefault  *string `gorm:"column:column_default"`
		}
		err := db.Raw(`
			SELECT 
				column_name,
				data_type,
				character_maximum_length,
				numeric_precision,
				numeric_scale,
				is_nullable,
				column_default
			FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = ?
			ORDER BY ordinal_position
		`, tableName).Scan(&columns).Error
		
		if err == nil && len(columns) > 0 {
			dump.WriteString("\n--\n")
			dump.WriteString(fmt.Sprintf("-- Name: %s; Type: TABLE; Schema: public; Owner: -\n", tableName))
			dump.WriteString("--\n\n")
			dump.WriteString(fmt.Sprintf("CREATE TABLE %s (\n", tableName))
			
			colDefs := make([]string, len(columns))
			for i, col := range columns {
				colDef := fmt.Sprintf("    %s ", col.ColumnName)
				
				// Build data type
				switch col.DataType {
				case "character varying":
					if col.CharMaxLength != nil {
						colDef += fmt.Sprintf("character varying(%d)", *col.CharMaxLength)
					} else {
						colDef += "character varying"
					}
				case "character":
					if col.CharMaxLength != nil {
						colDef += fmt.Sprintf("character(%d)", *col.CharMaxLength)
					} else {
						colDef += "character"
					}
				case "numeric":
					if col.NumericPrec != nil && col.NumericScale != nil {
						colDef += fmt.Sprintf("numeric(%d,%d)", *col.NumericPrec, *col.NumericScale)
					} else if col.NumericPrec != nil {
						colDef += fmt.Sprintf("numeric(%d)", *col.NumericPrec)
					} else {
						colDef += "numeric"
					}
				default:
					colDef += col.DataType
				}
				
				// Add NOT NULL constraint
				if col.IsNullable == "NO" {
					colDef += " NOT NULL"
				}
				
				// Add default value
				if col.ColumnDefault != nil {
					colDef += " DEFAULT " + *col.ColumnDefault
				}
				
				colDefs[i] = colDef
			}
			dump.WriteString(strings.Join(colDefs, ",\n"))
			dump.WriteString("\n);\n\n")
		}
		
		// Get table data
		var rowCount int64
		db.Table(tableName).Count(&rowCount)
		
		if rowCount > 0 {
			// Get column info for data export (reuse if available from structure query, otherwise query again)
			var colInfo []struct {
				ColumnName string `gorm:"column:column_name"`
				DataType   string `gorm:"column:data_type"`
			}
			if len(columns) == 0 {
				// If columns not available from structure query, get them separately
				err := db.Raw(`
					SELECT column_name, data_type
					FROM information_schema.columns
					WHERE table_schema = 'public' AND table_name = ?
					ORDER BY ordinal_position
				`, tableName).Scan(&colInfo).Error
				if err != nil {
					continue // Skip this table if we can't get column info
				}
			} else {
				// Use columns from structure query
				colInfo = make([]struct {
					ColumnName string `gorm:"column:column_name"`
					DataType   string `gorm:"column:data_type"`
				}, len(columns))
				for i, col := range columns {
					colInfo[i].ColumnName = col.ColumnName
					colInfo[i].DataType = col.DataType
				}
			}
			
			if len(colInfo) > 0 {
				colNames := make([]string, len(colInfo))
				colTypes := make([]string, len(colInfo))
				for i, col := range colInfo {
					colNames[i] = col.ColumnName
					colTypes[i] = col.DataType
				}
				
				// Build SELECT query with proper column quoting
				quotedCols := make([]string, len(colNames))
				for i, colName := range colNames {
					quotedCols[i] = fmt.Sprintf(`"%s"`, colName)
				}
				selectQuery := fmt.Sprintf(`SELECT %s FROM "%s"`, strings.Join(quotedCols, ", "), tableName)
				
				// Export data in batches using raw SQL
				batchSize := 1000
				offset := 0
				
				for {
					// Use raw SQL to get data
					rows, err := db.Raw(fmt.Sprintf("%s LIMIT %d OFFSET %d", selectQuery, batchSize, offset)).Rows()
					if err != nil {
						break
					}
					
					// Get column names from rows
					colNamesFromRows, err := rows.Columns()
					if err != nil {
						rows.Close()
						break
					}
					
					batchRowCount := 0
					for rows.Next() {
						// Create slice to hold values
						values := make([]interface{}, len(colNamesFromRows))
						valuePtrs := make([]interface{}, len(colNamesFromRows))
						for i := range values {
							valuePtrs[i] = &values[i]
						}
						
						if err := rows.Scan(valuePtrs...); err != nil {
							rows.Close()
							return nil, fmt.Errorf("failed to scan row: %v", err)
						}
						
						// Generate INSERT statement
						dump.WriteString(fmt.Sprintf("INSERT INTO %s (", tableName))
						dump.WriteString(strings.Join(colNames, ", "))
						dump.WriteString(") VALUES (")
						
						valueStrs := make([]string, len(values))
						for i, val := range values {
							if val == nil {
								valueStrs[i] = "NULL"
							} else {
								// Format value based on data type
								var valStr string
								dataType := colTypes[i]
								
								switch dataType {
								case "integer", "bigint", "smallint", "numeric", "real", "double precision":
									// Numeric types - no quotes needed
									valStr = fmt.Sprintf("%v", val)
								case "boolean":
									// Boolean type
									if b, ok := val.(bool); ok {
										if b {
											valStr = "true"
										} else {
											valStr = "false"
										}
									} else if s, ok := val.(string); ok {
										// Sometimes boolean comes as string
										if s == "true" || s == "t" || s == "1" {
											valStr = "true"
										} else {
											valStr = "false"
										}
									} else {
										valStr = fmt.Sprintf("%v", val)
									}
								default:
									// String types - need proper escaping
									valStr = fmt.Sprintf("%v", val)
									// Escape PostgreSQL string literals
									valStr = strings.ReplaceAll(valStr, "\\", "\\\\")
									valStr = strings.ReplaceAll(valStr, "'", "''")
									valStr = "'" + valStr + "'"
								}
								valueStrs[i] = valStr
							}
						}
						dump.WriteString(strings.Join(valueStrs, ", "))
						dump.WriteString(");\n")
						batchRowCount++
					}
					rows.Close()
					
					if batchRowCount == 0 || batchRowCount < batchSize {
						break
					}
					offset += batchSize
				}
				dump.WriteString("\n")
			}
		}
	}
	
	return []byte(dump.String()), nil
}

func (s *ServerService) ImportDB(file multipart.File) error {
	// Reset the file reader to the beginning
	_, err := file.Seek(0, 0)
	if err != nil {
		return common.NewErrorf("Error resetting file reader: %v", err)
	}

	// Create a temporary file to store the SQL dump
	tempFile, err := os.CreateTemp("", "x-ui-db-import-*.sql")
	if err != nil {
		return common.NewErrorf("Error creating temporary SQL file: %v", err)
	}
	tempPath := tempFile.Name()

	// Robust deferred cleanup for the temporary file
	defer func() {
		if tempFile != nil {
			if cerr := tempFile.Close(); cerr != nil {
				logger.Warningf("Warning: failed to close temp file: %v", cerr)
			}
		}
		if _, err := os.Stat(tempPath); err == nil {
			if rerr := os.Remove(tempPath); rerr != nil {
				logger.Warningf("Warning: failed to remove temp file: %v", rerr)
			}
		}
	}()

	// Save uploaded SQL dump to temporary file
	if _, err = io.Copy(tempFile, file); err != nil {
		return common.NewErrorf("Error saving SQL dump: %v", err)
	}

	// Close temp file before importing
	if err = tempFile.Close(); err != nil {
		return common.NewErrorf("Error closing temporary SQL file: %v", err)
	}
	tempFile = nil

	// Stop Xray (ignore error but log)
	if errStop := s.StopXrayService(); errStop != nil {
		logger.Warningf("Failed to stop Xray before DB import: %v", errStop)
	}

	// Get database connection parameters before closing connection
	host := config.GetDBHost()
	port := config.GetDBPort()
	user := config.GetDBUser()
	password := config.GetDBPassword()
	dbname := config.GetDBName()

	// Clear all database objects before import to ensure clean restore
	// This matches the schema structure and ensures data from dump will overwrite existing data
	db := database.GetDB()
	if db != nil {
		logger.Info("Clearing existing database objects before import...")
		
		// Use a single transaction to drop all objects in correct order
		// This matches how pg_dump with --clean works
		clearQuery := `
		DO $$ 
		DECLARE 
			r RECORD;
		BEGIN
			-- Drop all foreign key constraints first
			FOR r IN (
				SELECT conname, conrelid::regclass as table_name
				FROM pg_constraint
				WHERE connamespace = (SELECT oid FROM pg_namespace WHERE nspname = 'public')
				AND contype = 'f'
			) 
			LOOP
				EXECUTE 'ALTER TABLE ' || r.table_name || ' DROP CONSTRAINT IF EXISTS ' || quote_ident(r.conname) || ' CASCADE';
			END LOOP;
			
			-- Drop all tables (CASCADE will handle remaining dependencies)
			FOR r IN (SELECT tablename FROM pg_tables WHERE schemaname = 'public') 
			LOOP
				EXECUTE 'DROP TABLE IF EXISTS public.' || quote_ident(r.tablename) || ' CASCADE';
			END LOOP;
			
			-- Drop all sequences (some may remain after table drops)
			FOR r IN (SELECT sequence_name FROM information_schema.sequences WHERE sequence_schema = 'public') 
			LOOP
				EXECUTE 'DROP SEQUENCE IF EXISTS public.' || quote_ident(r.sequence_name) || ' CASCADE';
			END LOOP;
			
			-- Drop all views
			FOR r IN (SELECT table_name FROM information_schema.views WHERE table_schema = 'public') 
			LOOP
				EXECUTE 'DROP VIEW IF EXISTS public.' || quote_ident(r.table_name) || ' CASCADE';
			END LOOP;
			
			-- Drop all functions
			FOR r IN (
				SELECT proname, oidvectortypes(proargtypes) as argtypes 
				FROM pg_proc 
				WHERE pronamespace = (SELECT oid FROM pg_namespace WHERE nspname = 'public')
			) 
			LOOP
				EXECUTE 'DROP FUNCTION IF EXISTS public.' || quote_ident(r.proname) || '(' || r.argtypes || ') CASCADE';
			END LOOP;
		END $$;`
		
		if err := db.Exec(clearQuery).Error; err != nil {
			logger.Warningf("Failed to clear database objects: %v", err)
		} else {
			logger.Info("Database objects cleared successfully")
		}
	}

	// Close existing DB connection
	if errClose := database.CloseDB(); errClose != nil {
		logger.Warningf("Failed to close existing DB before import: %v", errClose)
	}

	// Set PGPASSWORD environment variable for psql
	env := os.Environ()
	env = append(env, fmt.Sprintf("PGPASSWORD=%s", password))

	// Use psql to import the SQL dump
	// We don't use --single-transaction because it aborts on first error
	// Instead, we use ON_ERROR_STOP=0 to continue on errors
	// This allows the import to complete even if some objects already exist
	// The dump with --clean --if-exists will have DROP commands that may fail if objects don't exist,
	// which is expected and non-critical
	cmd := exec.Command("psql",
		"-h", host,
		"-p", strconv.Itoa(port),
		"-U", user,
		"-d", dbname,
		"-f", tempPath,
		"--quiet",
		"--set", "ON_ERROR_STOP=0",
	)
	cmd.Env = env

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stderr // Capture both stdout and stderr

	err = cmd.Run()
	
	// Parse stderr to check for critical errors
	stderrStr := stderr.String()
	
	// Filter out non-critical errors that are expected when restoring
	// Errors like "already exists" are expected when dump has --clean --if-exists
	// and we've already cleared the database, so some DROP commands may fail
	criticalErrors := []string{
		"FATAL",
		"connection",
		"authentication",
		"permission denied",
		"database.*does not exist",
		"role.*does not exist",
	}
	
	hasCriticalError := false
	lowerStderr := strings.ToLower(stderrStr)
	for _, criticalErr := range criticalErrors {
		matched, _ := regexp.MatchString(strings.ToLower(criticalErr), lowerStderr)
		if matched {
			hasCriticalError = true
			break
		}
	}
	
	// Check for expected non-critical errors
	// These are errors that are acceptable when restoring a dump with --clean --if-exists
	expectedErrors := []string{
		"already exists",
		"does not exist",        // For DROP IF EXISTS when object doesn't exist
		"json_extract",          // SQLite-specific functions in old dumps
		"JSON_EXTRACT",          // SQLite-specific functions
		"JSON_EACH",             // SQLite-specific functions
		"transaction_timeout",   // Non-standard PostgreSQL parameter (may be in old dumps)
		"unrecognized configuration parameter", // Non-standard parameters in old dumps
	}
	
	hasOnlyExpectedErrors := true
	if stderrStr != "" {
		// Check if there are any non-expected errors
		errorLines := strings.Split(stderrStr, "\n")
		for _, line := range errorLines {
			if strings.Contains(line, "ERROR:") {
				isExpected := false
				for _, expectedErr := range expectedErrors {
					if strings.Contains(line, expectedErr) {
						isExpected = true
						break
					}
				}
				if !isExpected {
					hasOnlyExpectedErrors = false
					break
				}
			}
		}
	} else {
		hasOnlyExpectedErrors = true // No errors at all
	}
	
	if err != nil && hasCriticalError {
		return common.NewErrorf("psql import failed with critical error: %v, stderr: %s", err, stderrStr)
	}
	
	// Log warnings but don't fail if only expected/non-critical errors
	if stderrStr != "" && !hasCriticalError {
		if hasOnlyExpectedErrors {
			logger.Info("psql import completed successfully (some expected warnings about existing objects were ignored)")
		} else {
			logger.Warningf("psql import completed with warnings: %s", stderrStr)
		}
	} else if err == nil {
		logger.Info("psql import completed successfully")
	}

	// Reconnect to database
	if err = database.InitDB(config.GetDBConnectionString()); err != nil {
		return common.NewErrorf("Error reconnecting to database after import: %v", err)
	}

	// Run migrations
	s.inboundService.MigrateDB()

	// Start Xray
	if err = s.RestartXrayService(); err != nil {
		return common.NewErrorf("Imported DB but failed to start Xray: %v", err)
	}

	return nil
}

// IsValidGeofileName validates that the filename is safe for geofile operations.
// It checks for path traversal attempts and ensures the filename contains only safe characters.
func (s *ServerService) IsValidGeofileName(filename string) bool {
	if filename == "" {
		return false
	}

	// Check for path traversal attempts
	if strings.Contains(filename, "..") {
		return false
	}

	// Check for path separators (both forward and backward slash)
	if strings.ContainsAny(filename, `/\`) {
		return false
	}

	// Check for absolute path indicators
	if filepath.IsAbs(filename) {
		return false
	}

	// Additional security: only allow alphanumeric, dots, underscores, and hyphens
	// This is stricter than the general filename regex
	validGeofilePattern := `^[a-zA-Z0-9._-]+\.dat$`
	matched, _ := regexp.MatchString(validGeofilePattern, filename)
	return matched
}

func (s *ServerService) UpdateGeofile(fileName string) error {
	files := []struct {
		URL      string
		FileName string
	}{
		{"https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat", "geoip.dat"},
		{"https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat", "geosite.dat"},
		{"https://github.com/chocolate4u/Iran-v2ray-rules/releases/latest/download/geoip.dat", "geoip_IR.dat"},
		{"https://github.com/chocolate4u/Iran-v2ray-rules/releases/latest/download/geosite.dat", "geosite_IR.dat"},
		{"https://github.com/runetfreedom/russia-v2ray-rules-dat/releases/latest/download/geoip.dat", "geoip_RU.dat"},
		{"https://github.com/runetfreedom/russia-v2ray-rules-dat/releases/latest/download/geosite.dat", "geosite_RU.dat"},
	}

	// Strict allowlist check to avoid writing uncontrolled files
	if fileName != "" {
		// Use the centralized validation function
		if !s.IsValidGeofileName(fileName) {
			return common.NewErrorf("Invalid geofile name: contains unsafe path characters: %s", fileName)
		}

		// Ensure the filename matches exactly one from our allowlist
		isAllowed := false
		for _, file := range files {
			if fileName == file.FileName {
				isAllowed = true
				break
			}
		}
		if !isAllowed {
			return common.NewErrorf("Invalid geofile name: %s not in allowlist", fileName)
		}
	}
	downloadFile := func(url, destPath string) error {
		resp, err := http.Get(url)
		if err != nil {
			return common.NewErrorf("Failed to download Geofile from %s: %v", url, err)
		}
		defer resp.Body.Close()

		file, err := os.Create(destPath)
		if err != nil {
			return common.NewErrorf("Failed to create Geofile %s: %v", destPath, err)
		}
		defer file.Close()

		_, err = io.Copy(file, resp.Body)
		if err != nil {
			return common.NewErrorf("Failed to save Geofile %s: %v", destPath, err)
		}

		return nil
	}

	var errorMessages []string

	if fileName == "" {
		for _, file := range files {
			// Sanitize the filename from our allowlist as an extra precaution
			destPath := filepath.Join(config.GetBinFolderPath(), filepath.Base(file.FileName))

			if err := downloadFile(file.URL, destPath); err != nil {
				errorMessages = append(errorMessages, fmt.Sprintf("Error downloading Geofile '%s': %v", file.FileName, err))
			}
		}
	} else {
		// Use filepath.Base to ensure we only get the filename component, no path traversal
		safeName := filepath.Base(fileName)
		destPath := filepath.Join(config.GetBinFolderPath(), safeName)

		var fileURL string
		for _, file := range files {
			if file.FileName == fileName {
				fileURL = file.URL
				break
			}
		}

		if fileURL == "" {
			errorMessages = append(errorMessages, fmt.Sprintf("File '%s' not found in the list of Geofiles", fileName))
		} else {
			if err := downloadFile(fileURL, destPath); err != nil {
				errorMessages = append(errorMessages, fmt.Sprintf("Error downloading Geofile '%s': %v", fileName, err))
			}
		}
	}

	err := s.RestartXrayService()
	if err != nil {
		errorMessages = append(errorMessages, fmt.Sprintf("Updated Geofile '%s' but Failed to start Xray: %v", fileName, err))
	}

	if len(errorMessages) > 0 {
		return common.NewErrorf("%s", strings.Join(errorMessages, "\r\n"))
	}

	return nil
}

func (s *ServerService) GetNewX25519Cert() (any, error) {
	// Run the command
	cmd := exec.Command(xray.GetBinaryPath(), "x25519")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(out.String(), "\n")

	privateKeyLine := strings.Split(lines[0], ":")
	publicKeyLine := strings.Split(lines[1], ":")

	privateKey := strings.TrimSpace(privateKeyLine[1])
	publicKey := strings.TrimSpace(publicKeyLine[1])

	keyPair := map[string]any{
		"privateKey": privateKey,
		"publicKey":  publicKey,
	}

	return keyPair, nil
}

func (s *ServerService) GetNewmldsa65() (any, error) {
	// Run the command
	cmd := exec.Command(xray.GetBinaryPath(), "mldsa65")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(out.String(), "\n")

	SeedLine := strings.Split(lines[0], ":")
	VerifyLine := strings.Split(lines[1], ":")

	seed := strings.TrimSpace(SeedLine[1])
	verify := strings.TrimSpace(VerifyLine[1])

	keyPair := map[string]any{
		"seed":   seed,
		"verify": verify,
	}

	return keyPair, nil
}

func (s *ServerService) GetNewEchCert(sni string) (any, error) {
	// Run the command
	cmd := exec.Command(xray.GetBinaryPath(), "tls", "ech", "--serverName", sni)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(out.String(), "\n")
	if len(lines) < 4 {
		return nil, common.NewError("invalid ech cert")
	}

	configList := lines[1]
	serverKeys := lines[3]

	return map[string]any{
		"echServerKeys": serverKeys,
		"echConfigList": configList,
	}, nil
}

func (s *ServerService) GetNewVlessEnc() (any, error) {
	cmd := exec.Command(xray.GetBinaryPath(), "vlessenc")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}

	lines := strings.Split(out.String(), "\n")
	var auths []map[string]string
	var current map[string]string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Authentication:") {
			if current != nil {
				auths = append(auths, current)
			}
			current = map[string]string{
				"label": strings.TrimSpace(strings.TrimPrefix(line, "Authentication:")),
			}
		} else if strings.HasPrefix(line, `"decryption"`) || strings.HasPrefix(line, `"encryption"`) {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 && current != nil {
				key := strings.Trim(parts[0], `" `)
				val := strings.Trim(parts[1], `" `)
				current[key] = val
			}
		}
	}

	if current != nil {
		auths = append(auths, current)
	}

	return map[string]any{
		"auths": auths,
	}, nil
}

func (s *ServerService) GetNewUUID() (map[string]string, error) {
	newUUID, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("failed to generate UUID: %w", err)
	}

	return map[string]string{
		"uuid": newUUID.String(),
	}, nil
}

// getDatabaseStats retrieves database statistics including size, table count, row count, and connection pool info.
func (s *ServerService) getDatabaseStats() struct {
	Size         uint64 `json:"size"`
	Tables       int    `json:"tables"`
	TotalRows    uint64 `json:"totalRows"`
	OpenConns    int    `json:"openConns"`
	IdleConns    int    `json:"idleConns"`
	MaxOpenConns int    `json:"maxOpenConns"`
	MaxIdleConns int    `json:"maxIdleConns"`
} {
	db := database.GetDB()
	if db == nil {
		return struct {
			Size         uint64 `json:"size"`
			Tables       int    `json:"tables"`
			TotalRows    uint64 `json:"totalRows"`
			OpenConns    int    `json:"openConns"`
			IdleConns    int    `json:"idleConns"`
			MaxOpenConns int    `json:"maxOpenConns"`
			MaxIdleConns int    `json:"maxIdleConns"`
		}{}
	}

	stats := struct {
		Size         uint64 `json:"size"`
		Tables       int    `json:"tables"`
		TotalRows    uint64 `json:"totalRows"`
		OpenConns    int    `json:"openConns"`
		IdleConns    int    `json:"idleConns"`
		MaxOpenConns int    `json:"maxOpenConns"`
		MaxIdleConns int    `json:"maxIdleConns"`
	}{}

	// Get connection pool statistics
	sqlDB, err := db.DB()
	if err == nil {
		dbStats := sqlDB.Stats()
		stats.OpenConns = dbStats.OpenConnections
		stats.IdleConns = dbStats.Idle
		stats.MaxOpenConns = dbStats.MaxOpenConnections
		// MaxIdleConns is set during initialization, we need to get it from config or use a reasonable default
		// Since we can't easily get it from Stats(), we'll use a default value (5 as set in InitDB)
		stats.MaxIdleConns = 5 // Default from database/db.go
	}

	// Get database size (PostgreSQL)
	var dbSize uint64
	err = db.Raw(`SELECT pg_database_size(current_database())`).Scan(&dbSize).Error
	if err == nil {
		stats.Size = dbSize
	}

	// Get table count
	var tableCount int64
	err = db.Raw(`
		SELECT COUNT(*) 
		FROM information_schema.tables 
		WHERE table_schema = 'public' AND table_type = 'BASE TABLE'
	`).Scan(&tableCount).Error
	if err == nil {
		stats.Tables = int(tableCount)
	}

	// Get total row count across all tables
	var totalRows uint64
	err = db.Raw(`
		SELECT SUM(n_live_tup)::BIGINT
		FROM pg_stat_user_tables
	`).Scan(&totalRows).Error
	if err == nil {
		stats.TotalRows = totalRows
	}

	return stats
}

func (s *ServerService) GetNewmlkem768() (any, error) {
	// Run the command
	cmd := exec.Command(xray.GetBinaryPath(), "mlkem768")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(out.String(), "\n")

	SeedLine := strings.Split(lines[0], ":")
	ClientLine := strings.Split(lines[1], ":")

	seed := strings.TrimSpace(SeedLine[1])
	client := strings.TrimSpace(ClientLine[1])

	keyPair := map[string]any{
		"seed":   seed,
		"client": client,
	}

	return keyPair, nil
}
