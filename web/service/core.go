package service

import (
	"github.com/mhsanaei/3x-ui/v2/xray"
)

// CoreService defines the interface for proxy core operations.
// This allows the system to work with different cores (xray, sing-box) through a unified interface.
type CoreService interface {
	// GetConfig returns the current configuration
	GetConfig() (interface{}, error)
	
	// Restart restarts the core process
	Restart(isForce bool) error
	
	// IsRunning checks if the core is currently running
	IsRunning() bool
	
	// GetTraffic returns traffic statistics
	GetTraffic() ([]*xray.Traffic, []*xray.ClientTraffic, error)
	
	// GetVersion returns the version of the running core
	GetVersion() string
	
	// Stop stops the core process
	Stop() error
	
	// EnsureConfigFile ensures the configuration file is pre-generated
	EnsureConfigFile() error
}

// XrayCoreAdapter adapts XrayService to CoreService interface.
type XrayCoreAdapter struct {
	service *XrayService
}

// NewXrayCoreAdapter creates a new XrayCoreAdapter.
func NewXrayCoreAdapter(service *XrayService) *XrayCoreAdapter {
	return &XrayCoreAdapter{service: service}
}

// GetConfig returns the Xray configuration.
func (a *XrayCoreAdapter) GetConfig() (interface{}, error) {
	return a.service.GetXrayConfig()
}

// Restart restarts Xray.
func (a *XrayCoreAdapter) Restart(isForce bool) error {
	return a.service.RestartXray(isForce)
}

// IsRunning checks if Xray is running.
func (a *XrayCoreAdapter) IsRunning() bool {
	return a.service.IsXrayRunning()
}

// GetTraffic returns Xray traffic statistics.
func (a *XrayCoreAdapter) GetTraffic() ([]*xray.Traffic, []*xray.ClientTraffic, error) {
	return a.service.GetXrayTraffic()
}

// GetVersion returns the Xray version.
func (a *XrayCoreAdapter) GetVersion() string {
	return a.service.GetXrayVersion()
}

// Stop stops Xray.
func (a *XrayCoreAdapter) Stop() error {
	return a.service.StopXray()
}

// EnsureConfigFile ensures the Xray configuration file is pre-generated.
func (a *XrayCoreAdapter) EnsureConfigFile() error {
	return a.service.EnsureXrayConfigFile()
}

// DidXrayCrash checks if Xray crashed by verifying it's not running and wasn't manually stopped.
func (a *XrayCoreAdapter) DidXrayCrash() bool {
	return a.service.DidXrayCrash()
}

// IsNeedRestartAndSetFalse checks if restart is needed and resets the flag to false.
func (a *XrayCoreAdapter) IsNeedRestartAndSetFalse() bool {
	return a.service.IsNeedRestartAndSetFalse()
}

// SingBoxCoreAdapter adapts SingBoxService to CoreService interface.
type SingBoxCoreAdapter struct {
	service *SingBoxService
}

// NewSingBoxCoreAdapter creates a new SingBoxCoreAdapter.
func NewSingBoxCoreAdapter(service *SingBoxService) *SingBoxCoreAdapter {
	return &SingBoxCoreAdapter{service: service}
}

// GetConfig returns the sing-box configuration.
func (a *SingBoxCoreAdapter) GetConfig() (interface{}, error) {
	return a.service.GetSingBoxConfig()
}

// Restart restarts sing-box.
func (a *SingBoxCoreAdapter) Restart(isForce bool) error {
	return a.service.RestartSingBox(isForce)
}

// IsRunning checks if sing-box is running.
func (a *SingBoxCoreAdapter) IsRunning() bool {
	return a.service.IsSingBoxRunning()
}

// GetTraffic returns sing-box traffic statistics, converted to xray format for compatibility.
func (a *SingBoxCoreAdapter) GetTraffic() ([]*xray.Traffic, []*xray.ClientTraffic, error) {
	sbTraffic, sbClientTraffic, err := a.service.GetSingBoxTraffic()
	if err != nil {
		return nil, nil, err
	}
	
	// Convert sing-box traffic to xray format
	xrayTraffic := make([]*xray.Traffic, len(sbTraffic))
	for i, t := range sbTraffic {
		xrayTraffic[i] = &xray.Traffic{
			IsInbound:  t.IsInbound,
			IsOutbound: t.IsOutbound,
			Tag:        t.Tag,
			Up:         t.Up,
			Down:       t.Down,
		}
	}
	
	xrayClientTraffic := make([]*xray.ClientTraffic, len(sbClientTraffic))
	for i, ct := range sbClientTraffic {
		xrayClientTraffic[i] = &xray.ClientTraffic{
			Email: ct.Email,
			Up:    ct.Up,
			Down:  ct.Down,
		}
	}
	
	return xrayTraffic, xrayClientTraffic, nil
}

// GetVersion returns the sing-box version.
func (a *SingBoxCoreAdapter) GetVersion() string {
	return a.service.GetSingBoxVersion()
}

// Stop stops sing-box.
func (a *SingBoxCoreAdapter) Stop() error {
	return a.service.StopSingBox()
}

// EnsureConfigFile ensures the sing-box configuration file is pre-generated.
func (a *SingBoxCoreAdapter) EnsureConfigFile() error {
	return a.service.EnsureSingBoxConfigFile()
}

// GetCoreService returns the appropriate CoreService based on the current core type setting.
func GetCoreService() (CoreService, error) {
	settingService := SettingService{}
	coreType, err := settingService.GetCoreType()
	if err != nil {
		return nil, err
	}
	
	switch coreType {
	case "xray":
		xrayService := NewXrayService()
		return NewXrayCoreAdapter(&xrayService), nil
	case "sing-box":
		singboxService := NewSingBoxService()
		return NewSingBoxCoreAdapter(&singboxService), nil
	default:
		// Default to xray
		xrayService := NewXrayService()
		return NewXrayCoreAdapter(&xrayService), nil
	}
}
