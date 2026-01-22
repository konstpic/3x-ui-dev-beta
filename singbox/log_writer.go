package singbox

import (
	"regexp"
	"runtime"
	"strings"

	"github.com/mhsanaei/3x-ui/v2/logger"
)

// NewLogWriter returns a new LogWriter for processing sing-box log output.
func NewLogWriter() *LogWriter {
	return &LogWriter{}
}

// LogWriter processes and filters log output from the sing-box process, handling crash detection and message filtering.
type LogWriter struct {
	lastLine string
}

// Write processes and filters log output from the sing-box process, handling crash detection and message filtering.
func (lw *LogWriter) Write(m []byte) (n int, err error) {
	crashRegex := regexp.MustCompile(`(?i)(panic|exception|stack trace|fatal error)`)

	// Convert the data to a string
	message := strings.TrimSpace(string(m))
	msgLowerAll := strings.ToLower(message)

	// Suppress noisy Windows process-kill signal that surfaces as exit status 1
	if runtime.GOOS == "windows" && strings.Contains(msgLowerAll, "exit status 1") {
		return len(m), nil
	}

	// Check if the message contains a crash
	if crashRegex.MatchString(message) {
		logger.Debug("Core crash detected:\n", message)
		lw.lastLine = message
		return len(m), nil
	}

	// sing-box log format: "2024-01-01T12:00:00.000Z [LEVEL] message"
	regex := regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z) \[([^\]]+)\] (.+)$`)
	messages := strings.Split(message, "\n")

	for _, msg := range messages {
		matches := regex.FindStringSubmatch(msg)

		if len(matches) > 3 {
			level := matches[2]
			msgBody := matches[3]
			msgBodyLower := strings.ToLower(msgBody)

			if strings.Contains(msgBodyLower, "tls handshake error") ||
				strings.Contains(msgBodyLower, "connection ends") {
				logger.Debug("SING-BOX: " + msgBody)
				lw.lastLine = ""
				continue
			}

			if strings.Contains(msgBodyLower, "failed") {
				logger.Error("SING-BOX: " + msgBody)
			} else {
				switch level {
				case "debug", "DEBUG":
					logger.Debug("SING-BOX: " + msgBody)
				case "info", "INFO":
					logger.Info("SING-BOX: " + msgBody)
				case "warn", "WARN", "warning", "WARNING":
					logger.Warning("SING-BOX: " + msgBody)
				case "error", "ERROR":
					logger.Error("SING-BOX: " + msgBody)
				default:
					logger.Debug("SING-BOX: " + msg)
				}
			}
			lw.lastLine = ""
		} else if msg != "" {
			msgLower := strings.ToLower(msg)

			if strings.Contains(msgLower, "tls handshake error") ||
				strings.Contains(msgLower, "connection ends") {
				logger.Debug("SING-BOX: " + msg)
				lw.lastLine = msg
				continue
			}

			if strings.Contains(msgLower, "failed") {
				logger.Error("SING-BOX: " + msg)
			} else {
				logger.Debug("SING-BOX: " + msg)
			}
			lw.lastLine = msg
		}
	}

	return len(m), nil
}
