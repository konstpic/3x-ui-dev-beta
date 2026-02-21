// Package config provides configuration management utilities for the SharX panel,
// including version information, logging levels, database connection, and environment variable handling.
package config

import (
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

//go:embed version
var version string

//go:embed name
var name string

// LogLevel represents the logging level for the application.
type LogLevel string

// Logging level constants
const (
	Debug   LogLevel = "debug"
	Info    LogLevel = "info"
	Notice  LogLevel = "notice"
	Warning LogLevel = "warning"
	Error   LogLevel = "error"
)

// GetVersion returns the version string of the SharX application.
func GetVersion() string {
	return strings.TrimSpace(version)
}

// GetName returns the name of the SharX application.
func GetName() string {
	return strings.TrimSpace(name)
}

// GetLogLevel returns the current logging level based on environment variables or defaults to Info.
func GetLogLevel() LogLevel {
	if IsDebug() {
		return Debug
	}
	logLevel := os.Getenv("XUI_LOG_LEVEL")
	if logLevel == "" {
		return Info
	}
	return LogLevel(logLevel)
}

// IsDebug returns true if debug mode is enabled via the XUI_DEBUG environment variable.
func IsDebug() bool {
	return os.Getenv("XUI_DEBUG") == "true"
}

// GetBinFolderPath returns the path to the binary folder, defaulting to "bin" if not set via XUI_BIN_FOLDER.
func GetBinFolderPath() string {
	binFolderPath := os.Getenv("XUI_BIN_FOLDER")
	if binFolderPath == "" {
		binFolderPath = "bin"
	}
	return binFolderPath
}

// GetDataFolderPath returns the path to the data folder for storing core state and geo files.
// Defaults to "data" if not set via XUI_DATA_FOLDER, falling back to bin folder for compatibility.
func GetDataFolderPath() string {
	dataFolderPath := os.Getenv("XUI_DATA_FOLDER")
	if dataFolderPath == "" {
		// Use local "data" folder relative to executable
		baseDir := getBaseDir()
		dataFolderPath = filepath.Join(baseDir, "data")
	}
	return dataFolderPath
}

func getBaseDir() string {
	exePath, err := os.Executable()
	if err != nil {
		return "."
	}
	exeDir := filepath.Dir(exePath)
	exeDirLower := strings.ToLower(filepath.ToSlash(exeDir))
	if strings.Contains(exeDirLower, "/appdata/local/temp/") || strings.Contains(exeDirLower, "/go-build") {
		wd, err := os.Getwd()
		if err != nil {
			return "."
		}
		return wd
	}
	return exeDir
}

// GetDBHost returns the PostgreSQL database host from environment variables or defaults to localhost.
func GetDBHost() string {
	host := os.Getenv("XUI_DB_HOST")
	if host == "" {
		return "localhost"
	}
	return host
}

// GetDBPort returns the PostgreSQL database port from environment variables or defaults to 5432.
func GetDBPort() int {
	port := os.Getenv("XUI_DB_PORT")
	if port == "" {
		return 5432
	}
	portInt, err := strconv.Atoi(port)
	if err != nil {
		return 5432
	}
	return portInt
}

// GetDBUser returns the PostgreSQL database user from environment variables.
// This is a required parameter.
func GetDBUser() string {
	return os.Getenv("XUI_DB_USER")
}

// GetDBPassword returns the PostgreSQL database password from environment variables.
// This is a required parameter.
func GetDBPassword() string {
	return os.Getenv("XUI_DB_PASSWORD")
}

// GetDBName returns the PostgreSQL database name from environment variables.
// This is a required parameter.
func GetDBName() string {
	dbName := os.Getenv("XUI_DB_NAME")
	if dbName == "" {
		// Fallback to application name if not specified
		return GetName()
	}
	return dbName
}

// GetDBSSLMode returns the PostgreSQL SSL mode from environment variables or defaults to disable.
func GetDBSSLMode() string {
	sslMode := os.Getenv("XUI_DB_SSLMODE")
	if sslMode == "" {
		return "disable"
	}
	return sslMode
}

// GetDBConnectionString returns a PostgreSQL connection string built from environment variables.
// Format: postgres://user:password@host:port/dbname?sslmode=mode
func GetDBConnectionString() string {
	user := GetDBUser()
	password := GetDBPassword()
	host := GetDBHost()
	port := GetDBPort()
	dbname := GetDBName()
	sslmode := GetDBSSLMode()

	// URL encode password to handle special characters
	encodedPassword := url.QueryEscape(password)

	connStr := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		url.QueryEscape(user),
		encodedPassword,
		host,
		port,
		url.QueryEscape(dbname),
		url.QueryEscape(sslmode),
	)

	return connStr
}

// GetDBPath is kept for backward compatibility but now returns the connection string.
// Deprecated: Use GetDBConnectionString() instead.
func GetDBPath() string {
	return GetDBConnectionString()
}

// GetLogFolder returns the path to the log folder based on environment variables or platform defaults.
func GetLogFolder() string {
	logFolderPath := os.Getenv("XUI_LOG_FOLDER")
	if logFolderPath != "" {
		return logFolderPath
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(".", "log")
	}
	return "/var/log/x-ui"
}

// copyFile removed - no longer needed for PostgreSQL migration

// init function removed - no longer needed for PostgreSQL migration
// The old SQLite file migration logic is no longer applicable
