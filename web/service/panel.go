package service

import (
	"os"
	"syscall"
	"time"

	"github.com/konstpic/sharx/v2/logger"
)

// PanelService provides business logic for panel management operations.
// It handles panel restart, updates, and system-level panel controls.
type PanelService struct{}

func (s *PanelService) RestartPanel(delay time.Duration) error {
	p, err := os.FindProcess(syscall.Getpid())
	if err != nil {
		return err
	}
	go func() {
		time.Sleep(delay)
		err := p.Signal(syscall.SIGHUP)
		if err != nil {
			logger.Error("failed to send SIGHUP signal:", err)
		}
	}()
	return nil
}

// RestartContainer restarts the Docker container.
// It sends SIGTERM to PID 1 (main process in container) to trigger container restart.
// Docker will automatically restart the container if restart policy is set.
func (s *PanelService) RestartContainer(delay time.Duration) error {
	logger.Info("Attempting to restart Docker container by sending SIGTERM to PID 1")

	// Send SIGTERM to PID 1 (main process in container) after delay
	go func() {
		time.Sleep(delay)
		
		// Try to send SIGTERM to PID 1 (main process in container)
		// This will cause the container to exit, and Docker will restart it if restart policy is set
		err := syscall.Kill(1, syscall.SIGTERM)
		if err != nil {
			logger.Warningf("Failed to send SIGTERM to PID 1: %v. Trying os.Exit(0) as fallback.", err)
			
			// Fallback: exit gracefully
			// This will cause the container to exit, and Docker will restart it
			time.Sleep(time.Second)
			os.Exit(0)
		} else {
			logger.Info("Sent SIGTERM to PID 1, container will restart")
		}
	}()

	return nil
}
