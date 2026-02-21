package job

import (
	"strconv"

	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/web/service"
)

// RotateClientKeysJob rotates client keys (UUID/password) before subscription update interval.
// If subscription update interval is 60 minutes, keys will be rotated at 59 minutes.
type RotateClientKeysJob struct {
	settingService service.SettingService
	clientService  service.ClientService
}

// NewRotateClientKeysJob creates a new client keys rotation job instance.
func NewRotateClientKeysJob() *RotateClientKeysJob {
	return &RotateClientKeysJob{}
}

// Run rotates keys for all active clients if auto-rotation is enabled.
func (j *RotateClientKeysJob) Run() {
	// Check if auto-rotation is enabled
	autoRotate, err := j.settingService.GetSubAutoRotateKeys()
	if err != nil {
		logger.Warningf("RotateClientKeysJob: Failed to get subAutoRotateKeys setting: %v", err)
		return
	}
	
	if !autoRotate {
		logger.Debug("RotateClientKeysJob: Auto-rotation is disabled, skipping")
		return
	}
	
	// Get subscription update interval
	// Priority: 1) Custom header ProfileUpdateInterval (in hours) - takes precedence
	//           2) Base subUpdates setting (in minutes)
	var subUpdatesMinutes int
	
	customHeaders, err := j.settingService.GetSubHeadersParsed()
	if err == nil && customHeaders != nil && customHeaders.ProfileUpdateInterval != "" {
		// Use custom header value if set (it's in hours, need to convert to minutes)
		profileUpdateIntervalHours, err := strconv.Atoi(customHeaders.ProfileUpdateInterval)
		if err != nil {
			logger.Warningf("RotateClientKeysJob: Invalid ProfileUpdateInterval value '%s': %v", customHeaders.ProfileUpdateInterval, err)
			return
		}
		if profileUpdateIntervalHours <= 0 {
			logger.Debug("RotateClientKeysJob: ProfileUpdateInterval is 0 or negative, skipping")
			return
		}
		// Convert hours to minutes
		subUpdatesMinutes = profileUpdateIntervalHours * 60
		logger.Infof("RotateClientKeysJob: Using custom ProfileUpdateInterval from headers: %d hours (%d minutes)", 
			profileUpdateIntervalHours, subUpdatesMinutes)
	} else {
		// Use base subUpdates setting (already in minutes)
		subUpdatesStr, err := j.settingService.GetSubUpdates()
		if err != nil {
			logger.Warningf("RotateClientKeysJob: Failed to get subUpdates setting: %v", err)
			return
		}
		subUpdatesMinutes, err = strconv.Atoi(subUpdatesStr)
		if err != nil {
			logger.Warningf("RotateClientKeysJob: Invalid subUpdates value '%s': %v", subUpdatesStr, err)
			return
		}
		if subUpdatesMinutes <= 0 {
			logger.Debug("RotateClientKeysJob: Subscription update interval is 0 or negative, skipping")
			return
		}
		logger.Infof("RotateClientKeysJob: Using base subUpdates setting: %d minutes", subUpdatesMinutes)
	}
	
	// Rotate keys 1 minute before subscription update interval
	// If subscription update is 60 minutes, keys will be rotated at 59 minutes
	rotateIntervalMinutes := subUpdatesMinutes - 1
	if rotateIntervalMinutes < 1 {
		rotateIntervalMinutes = 1
	}
	
	logger.Infof("RotateClientKeysJob: Starting key rotation (subscription update interval: %d minutes, rotation will occur every %d minutes)", 
		subUpdatesMinutes, rotateIntervalMinutes)
	
	// Rotate keys for all active clients
	updatedCount, err := j.clientService.RotateAllClientKeys()
	if err != nil {
		logger.Errorf("RotateClientKeysJob: Failed to rotate client keys: %v", err)
		return
	}
	
	if updatedCount > 0 {
		logger.Infof("RotateClientKeysJob: Successfully rotated keys for %d clients", updatedCount)
	} else {
		logger.Debug("RotateClientKeysJob: No clients were updated")
	}
}
