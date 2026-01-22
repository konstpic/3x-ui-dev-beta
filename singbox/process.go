package singbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/mhsanaei/3x-ui/v2/config"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/util/common"
)

// GetBinaryName returns the sing-box binary filename for the current OS and architecture.
func GetBinaryName() string {
	return fmt.Sprintf("sing-box-%s-%s", runtime.GOOS, runtime.GOARCH)
}

// GetBinaryPath returns the full path to the sing-box binary executable.
// It tries multiple naming conventions to find the binary.
func GetBinaryPath() string {
	binFolder := config.GetBinFolderPath()
	primaryPath := filepath.Join(binFolder, GetBinaryName())
	
	// Check if primary path exists
	if _, err := os.Stat(primaryPath); err == nil {
		return primaryPath
	}
	
	// Try alternative naming conventions (matches DockerInit.sh naming)
	alternativeNames := []string{
		fmt.Sprintf("sing-box-linux-%s", runtime.GOARCH),
		"sing-box-linux-amd64",
		"sing-box-linux-arm64",
		"sing-box-linux-armv7",
		"sing-box",
	}
	
	for _, altName := range alternativeNames {
		altPath := filepath.Join(binFolder, altName)
		if _, err := os.Stat(altPath); err == nil {
			return altPath
		}
	}
	
	// Return primary path even if not found (will fail later with better error message)
	return primaryPath
}

// GetConfigPath returns the path to the sing-box configuration file in the binary folder.
func GetConfigPath() string {
	return config.GetBinFolderPath() + "/sing-box-config.json"
}

// stopProcess calls Stop on the given Process instance.
func stopProcess(p *Process) {
	p.Stop()
}

// Process wraps a sing-box process instance and provides management methods.
type Process struct {
	*process
}

// NewProcess creates a new sing-box process and sets up cleanup on garbage collection.
func NewProcess(singboxConfig *Config) *Process {
	p := &Process{newProcess(singboxConfig)}
	runtime.SetFinalizer(p, stopProcess)
	return p
}

type process struct {
	cmd *exec.Cmd

	version string
	apiPort int

	onlineClients []string

	config    *Config
	logWriter *LogWriter
	exitErr   error
	startTime time.Time
}

// newProcess creates a new internal process struct for sing-box.
func newProcess(config *Config) *process {
	return &process{
		version:   "Unknown",
		config:    config,
		logWriter: NewLogWriter(),
		startTime: time.Now(),
	}
}

// IsRunning returns true if the sing-box process is currently running.
func (p *process) IsRunning() bool {
	if p.cmd == nil || p.cmd.Process == nil {
		return false
	}
	if p.cmd.ProcessState == nil {
		return true
	}
	return false
}

// GetErr returns the last error encountered by the sing-box process.
func (p *process) GetErr() error {
	return p.exitErr
}

// GetResult returns the last log line or error from the sing-box process.
func (p *process) GetResult() string {
	if len(p.logWriter.lastLine) == 0 && p.exitErr != nil {
		return p.exitErr.Error()
	}
	return p.logWriter.lastLine
}

// GetVersion returns the version string of the sing-box process.
func (p *process) GetVersion() string {
	return p.version
}

// GetAPIPort returns the API port used by the sing-box process.
func (p *Process) GetAPIPort() int {
	return p.apiPort
}

// GetConfig returns the configuration used by the sing-box process.
func (p *Process) GetConfig() *Config {
	return p.config
}

// GetOnlineClients returns the list of online clients for the sing-box process.
func (p *Process) GetOnlineClients() []string {
	return p.onlineClients
}

// SetOnlineClients sets the list of online clients for the sing-box process.
func (p *Process) SetOnlineClients(users []string) {
	p.onlineClients = users
}

// GetUptime returns the uptime of the sing-box process in seconds.
func (p *Process) GetUptime() uint64 {
	return uint64(time.Since(p.startTime).Seconds())
}

// refreshAPIPort updates the API port from the experimental API settings.
func (p *process) refreshAPIPort() {
	// sing-box uses experimental.clash_api or experimental.v2ray_api
	// Try to extract API port from config
	var exp map[string]interface{}
	if err := json.Unmarshal(p.config.Experimental, &exp); err == nil {
		if clashAPI, ok := exp["clash_api"].(map[string]interface{}); ok {
			if externalController, ok := clashAPI["external_controller"].(string); ok {
				// Parse port from "127.0.0.1:9090" format
				parts := strings.Split(externalController, ":")
				if len(parts) == 2 {
					var port int
					if _, err := fmt.Sscanf(parts[1], "%d", &port); err == nil {
						p.apiPort = port
						return
					}
				}
			}
		}
		if v2rayAPI, ok := exp["v2ray_api"].(map[string]interface{}); ok {
			if listen, ok := v2rayAPI["listen"].(string); ok {
				// Parse port from "127.0.0.1:9090" format
				parts := strings.Split(listen, ":")
				if len(parts) == 2 {
					var port int
					if _, err := fmt.Sscanf(parts[1], "%d", &port); err == nil {
						p.apiPort = port
						return
					}
				}
			}
		}
	}
	// Default API port for sing-box (if using default)
	p.apiPort = 9090
}

// refreshVersion updates the version string by running the sing-box binary with -version.
func (p *process) refreshVersion() {
	cmd := exec.Command(GetBinaryPath(), "version")
	data, err := cmd.Output()
	if err != nil {
		p.version = "Unknown"
	} else {
		// sing-box version output format is multi-line, example:
		// "version 1.12.17 Environment: go1.25.6 linux/amd64 Tags: ..."
		// We need to extract just the version number (e.g., "1.12.17")
		versionStr := strings.TrimSpace(string(data))
		
		// Try to extract version number using regex-like approach
		// Look for pattern: "version" followed by space and version number
		versionStrLower := strings.ToLower(versionStr)
		versionPrefix := "version "
		if idx := strings.Index(versionStrLower, versionPrefix); idx >= 0 {
			// Found "version " prefix, extract what comes after
			afterPrefix := strings.TrimSpace(versionStr[idx+len(versionPrefix):])
			// Take first word (should be version number like "1.12.17")
			parts := strings.Fields(afterPrefix)
			if len(parts) > 0 {
				versionNum := parts[0]
				// Validate it's a version number (contains dots and starts with digit)
				if strings.Contains(versionNum, ".") && len(versionNum) > 0 && versionNum[0] >= '0' && versionNum[0] <= '9' {
					p.version = versionNum
					return
				}
			}
		}
		
		// Fallback: try to extract version number from first line
		lines := strings.Split(versionStr, "\n")
		if len(lines) > 0 {
			firstLine := strings.TrimSpace(lines[0])
			// Look for "sing-box X.Y.Z" pattern
			if strings.HasPrefix(strings.ToLower(firstLine), "sing-box ") {
				// Fallback: old format "sing-box 1.x.x"
				versionPart := strings.TrimPrefix(firstLine, "sing-box ")
				parts := strings.Fields(versionPart)
				if len(parts) > 0 {
					p.version = parts[0]
				} else {
					p.version = versionPart
				}
			} else {
				// If no known prefix, try to extract version number pattern
				// Look for pattern like "1.12.17" in the line
				parts := strings.Fields(firstLine)
				for _, part := range parts {
					// Check if it looks like a version number (X.Y.Z format)
					if strings.Count(part, ".") >= 1 && len(part) > 0 {
						// Check if it starts with a digit (version number)
						if part[0] >= '0' && part[0] <= '9' {
							p.version = part
							return
						}
					}
				}
				// If we can't parse it, use the first line as-is
				p.version = firstLine
			}
		} else {
			p.version = versionStr
		}
	}
}

// Start launches the sing-box process with the current configuration.
func (p *process) Start() (err error) {
	if p.IsRunning() {
		return errors.New("sing-box is already running")
	}

	defer func() {
		if err != nil {
			logger.Error("Failure in running sing-box process: ", err)
			p.exitErr = err
		}
	}()

	err = os.MkdirAll(config.GetLogFolder(), 0o770)
	if err != nil {
		logger.Warningf("Failed to create log folder: %s", err)
	}

	configPath, err := WriteConfigFile(p.config)
	if err != nil {
		return err
	}

	// Check if binary exists
	binaryPath := GetBinaryPath()
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		return fmt.Errorf("sing-box binary not found at %s", binaryPath)
	}

	// Log config for debugging (first 2000 chars)
	if configData, readErr := os.ReadFile(configPath); readErr == nil {
		configPreview := string(configData)
		if len(configPreview) > 2000 {
			configPreview = configPreview[:2000] + "... (truncated)"
		}
		logger.Debugf("sing-box config to validate (preview): %s", configPreview)
	}

	// Validate config before starting
	checkCmd := exec.Command(binaryPath, "check", "-c", configPath)
	var stderr bytes.Buffer
	checkCmd.Stderr = &stderr
	if err := checkCmd.Run(); err != nil {
		errorOutput := stderr.String()
		if errorOutput == "" {
			errorOutput = err.Error()
		}
		logger.Errorf("sing-box config validation failed. Binary: %s, Config path: %s, Error: %s", binaryPath, configPath, errorOutput)
		
		// Read and log the config file content for debugging
		if configData, readErr := os.ReadFile(configPath); readErr == nil {
			var configMap map[string]interface{}
			if jsonErr := json.Unmarshal(configData, &configMap); jsonErr == nil {
				if inbounds, ok := configMap["inbounds"].([]interface{}); ok {
					logger.Errorf("Config validation failed. Inbounds in config file:")
					for i, ib := range inbounds {
						if ibMap, ok := ib.(map[string]interface{}); ok {
							ibType, _ := ibMap["type"].(string)
							ibTag, _ := ibMap["tag"].(string)
							logger.Errorf("  inbound[%d]: type=%s, tag=%s", i, ibType, ibTag)
							if ibType == "tunnel" {
								logger.Errorf("  ERROR: Found tunnel inbound at index %d! This should have been filtered!", i)
							}
						}
					}
				}
			}
			
			// Also log the config content for debugging (first 2000 chars)
			configPreview := string(configData)
			if len(configPreview) > 2000 {
				configPreview = configPreview[:2000] + "... (truncated)"
			}
			logger.Debugf("Config content preview: %s", configPreview)
		}
		return fmt.Errorf("sing-box config validation failed: %s", errorOutput)
	}

	// Use the same binary path that was validated
	cmd := exec.Command(binaryPath, "run", "-c", configPath)
	p.cmd = cmd

	cmd.Stdout = p.logWriter
	cmd.Stderr = p.logWriter

	go func() {
		err := cmd.Run()
		if err != nil {
			// On Windows, killing the process results in "exit status 1" which isn't an error for us
			if runtime.GOOS == "windows" {
				errStr := strings.ToLower(err.Error())
				if strings.Contains(errStr, "exit status 1") {
					// Suppress noisy log on graceful stop
					p.exitErr = err
					return
				}
			}
			logger.Error("Failure in running sing-box:", err)
			p.exitErr = err
		}
	}()

	p.refreshVersion()
	p.refreshAPIPort()

	return nil
}

// WriteConfigFile writes the sing-box configuration to a file.
// This is used both for starting sing-box and for pre-generating config at startup.
// It returns the path to the written config file.
func WriteConfigFile(singboxConfig *Config) (string, error) {
	data, err := json.MarshalIndent(singboxConfig, "", "  ")
	if err != nil {
		return "", common.NewErrorf("Failed to generate sing-box configuration files: %v", err)
	}

	configPath := GetConfigPath()
	// Check if configPath exists and is a directory (can happen with Docker volume mounts)
	// If it's a directory, we can't remove it (it's mounted), so use an alternative path
	if stat, err := os.Stat(configPath); err == nil && stat.IsDir() {
		logger.Warningf("Config path %s is a directory (likely a Docker volume mount), using alternative path", configPath)
		// Try alternative paths in order of preference
		alternativePaths := []string{
			config.GetBinFolderPath() + "/sing-box-config-alt.json",
			"/app/config/sing-box-config.json",
			"/tmp/sing-box-config.json",
		}
		foundAlternative := false
		for _, altPath := range alternativePaths {
			// Check if this path is available (doesn't exist or is a file, not a directory)
			if stat, err := os.Stat(altPath); err != nil {
				// Path doesn't exist, we can use it
				configPath = altPath
				foundAlternative = true
				break
			} else if !stat.IsDir() {
				// Path exists and is a file, we can use it
				configPath = altPath
				foundAlternative = true
				break
			}
		}
		if !foundAlternative {
			return "", common.NewErrorf("Failed to find alternative config path: all paths are directories")
		}
		logger.Infof("Using alternative config path: %s", configPath)
	}
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(configPath), 0o770); err != nil {
		return "", common.NewErrorf("Failed to create config directory: %v", err)
	}
	
	// Remove old config file if it exists to ensure we write a fresh one
	if _, err := os.Stat(configPath); err == nil {
		if err := os.Remove(configPath); err != nil {
			logger.Warningf("Failed to remove old config file %s: %v", configPath, err)
			// Continue anyway - WriteFile will overwrite
		} else {
			logger.Debugf("Removed old config file: %s", configPath)
		}
	}
	
	if err := os.WriteFile(configPath, data, fs.ModePerm); err != nil {
		return "", common.NewErrorf("Failed to write configuration file: %v", err)
	}
	logger.Debugf("Wrote sing-box config file: %s (%d bytes)", configPath, len(data))
	return configPath, nil
}

// Stop terminates the running sing-box process.
func (p *process) Stop() error {
	if !p.IsRunning() {
		return errors.New("sing-box is not running")
	}

	var err error
	if runtime.GOOS == "windows" {
		err = p.cmd.Process.Kill()
	} else {
		err = p.cmd.Process.Signal(syscall.SIGTERM)
	}
	
	if err != nil {
		return err
	}
	
	// Wait for process to exit (with timeout)
	done := make(chan error, 1)
	go func() {
		done <- p.cmd.Wait()
	}()
	
	select {
	case err := <-done:
		if err != nil {
			// Process exited with error, but it's stopped
			logger.Debugf("sing-box process exited with error: %v", err)
		}
		return nil
	case <-time.After(5 * time.Second):
		// Process didn't exit in time, force kill
		logger.Warningf("sing-box process didn't exit in time, force killing")
		if killErr := p.cmd.Process.Kill(); killErr != nil {
			return fmt.Errorf("failed to kill sing-box process: %w", killErr)
		}
		// Wait a bit more for kill to take effect
		select {
		case <-done:
			return nil
		case <-time.After(1 * time.Second):
			return errors.New("sing-box process didn't exit after kill")
		}
	}
}
