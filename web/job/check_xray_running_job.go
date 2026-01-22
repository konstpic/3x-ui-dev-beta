// Package job provides background job implementations for the 3x-ui web panel,
// including traffic monitoring, system checks, and periodic maintenance tasks.
package job

import (
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/web/service"
)

// CheckXrayRunningJob monitors core process health and restarts it if it crashes.
type CheckXrayRunningJob struct {
	xrayService service.XrayService
	checkTime   int
}

// NewCheckXrayRunningJob creates a new core health check job instance.
func NewCheckXrayRunningJob() *CheckXrayRunningJob {
	return new(CheckXrayRunningJob)
}

// Run checks if the core has crashed and restarts it after confirming it's down for 2 consecutive checks.
func (j *CheckXrayRunningJob) Run() {
	// Skip in multi-node mode - there's no local core process to check
	settingService := service.SettingService{}
	multiMode, err := settingService.GetMultiNodeMode()
	if err == nil && multiMode {
		return // Skip if multi-node mode is enabled
	}

	// Get current core service
	coreService, err := service.GetCoreService()
	if err != nil {
		return // Skip if we can't get core service
	}

	// Check if core is running
	if coreService.IsRunning() {
		j.checkTime = 0
		return
	}

	// Core is not running - check if it crashed (only for Xray, as we have DidXrayCrash method)
	if xrayAdapter, ok := coreService.(*service.XrayCoreAdapter); ok {
		if !xrayAdapter.DidXrayCrash() {
			j.checkTime = 0
			return
		}
	}

	// Core is down - increment check time
	j.checkTime++
	// only restart if it's down 2 times in a row
	if j.checkTime > 1 {
		err := coreService.Restart(false)
		j.checkTime = 0
		if err != nil {
			logger.Error("Restart core failed:", err)
		}
	}
}
