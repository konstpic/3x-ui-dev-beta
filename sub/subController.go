package sub

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/mhsanaei/3x-ui/v2/config"
	"github.com/mhsanaei/3x-ui/v2/database"
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	service "github.com/mhsanaei/3x-ui/v2/web/service"
	"github.com/mhsanaei/3x-ui/v2/web/entity"

	"github.com/gin-gonic/gin"
)

// SUBController handles HTTP requests for subscription links and JSON configurations.
type SUBController struct {
	subTitle       string
	subPath        string
	subJsonPath    string
	jsonEnabled    bool
	subEncrypt     bool
	updateInterval string

	subService     *SubService
	subJsonService *SubJsonService
}

// NewSUBController creates a new subscription controller with the given configuration.
func NewSUBController(
	g *gin.RouterGroup,
	subPath string,
	jsonPath string,
	jsonEnabled bool,
	encrypt bool,
	showInfo bool,
	rModel string,
	update string,
	jsonFragment string,
	jsonNoise string,
	jsonMux string,
	jsonRules string,
	subTitle string,
) *SUBController {
	sub := NewSubService(showInfo, rModel)
	// Initialize services for multi-node support and new architecture
	sub.nodeService = service.NodeService{}
	sub.hostService = service.HostService{}
	sub.clientService = service.ClientService{}
	a := &SUBController{
		subTitle:       subTitle,
		subPath:        subPath,
		subJsonPath:    jsonPath,
		jsonEnabled:    jsonEnabled,
		subEncrypt:     encrypt,
		updateInterval: update,

		subService:     sub,
		subJsonService: NewSubJsonService(jsonFragment, jsonNoise, jsonMux, jsonRules, sub),
	}
	a.initRouter(g)
	return a
}

// initRouter registers HTTP routes for subscription links and JSON endpoints
// on the provided router group.
func (a *SUBController) initRouter(g *gin.RouterGroup) {
	gLink := g.Group(a.subPath)
	gLink.GET(":subid", a.subs)
	if a.jsonEnabled {
		gJson := g.Group(a.subJsonPath)
		gJson.GET(":subid", a.subJsons)
	}
}

// isAllowedUserAgent checks if the User-Agent is allowed when encryption is enabled.
// Allows: Happ, v2raytun, or browser (detected by Accept header).
// For Happ/v2raytun, also requires all HWID fields to be present.
func (a *SUBController) isAllowedUserAgent(c *gin.Context) bool {
	userAgent := strings.ToLower(c.GetHeader("User-Agent"))
	accept := strings.ToLower(c.GetHeader("Accept"))
	
	// Check for browser (by Accept header containing text/html)
	if strings.Contains(accept, "text/html") {
		return true
	}
	
	// Check if explicitly requesting HTML view
	if c.Query("html") == "1" || strings.EqualFold(c.Query("view"), "html") {
		return true
	}
	
	// For Happ or v2raytun, require all HWID fields
	isHapp := strings.Contains(userAgent, "happ")
	isV2RayTun := strings.Contains(userAgent, "v2raytun")
	
	if isHapp || isV2RayTun {
		// Check for all required HWID fields
		hwid := c.GetHeader("x-hwid")
		if hwid == "" {
			hwid = c.GetHeader("X-HWID")
		}
		
		deviceOS := c.GetHeader("x-device-os")
		if deviceOS == "" {
			deviceOS = c.GetHeader("X-Device-OS")
		}
		
		deviceModel := c.GetHeader("x-device-model")
		if deviceModel == "" {
			deviceModel = c.GetHeader("X-Device-Model")
		}
		
		osVersion := c.GetHeader("x-ver-os")
		if osVersion == "" {
			osVersion = c.GetHeader("X-Ver-OS")
		}
		
		// All HWID fields must be present
		if hwid != "" && deviceOS != "" && deviceModel != "" && osVersion != "" {
			return true
		}
		
		// Missing HWID fields - not allowed
		return false
	}
	
	return false
}

// subs handles HTTP requests for subscription links, returning either HTML page or base64-encoded subscription data.
func (a *SUBController) subs(c *gin.Context) {
	subId := c.Param("subid")
	
	// Record User-Agent for metrics
	userAgent := c.GetHeader("User-Agent")
	service.RecordUserAgent(userAgent)
	
	// If encryption is enabled, check User-Agent - only allow Happ, v2raytun, or browser
	if a.subEncrypt {
		if !a.isAllowedUserAgent(c) {
			logger.Warningf("Subscription request blocked: encryption enabled but User-Agent not allowed (subId: %s, User-Agent: %s)", 
				subId, userAgent)
			c.String(403, "Forbidden")
			return
		}
	}
	
	scheme, host, hostWithPort, hostHeader := a.subService.ResolveRequest(c)
		subs, lastOnline, traffic, err := a.subService.GetSubs(subId, host, c) // Pass context for HWID registration
		if err != nil || len(subs) == 0 {
			c.String(400, "Error!")
		} else {
			result := ""
			
			// Add Provider ID as comment in subscription body if method is "body" (according to Happ documentation)
			settingService := service.SettingService{}
			providerID, err := settingService.GetSubProviderID()
			if err == nil && providerID != "" {
				providerMethod, err := settingService.GetSubProviderIDMethod()
				if err == nil && providerMethod == "body" {
					result += fmt.Sprintf("#providerid %s\n", providerID)
				}
			}
			
			for _, sub := range subs {
				result += sub + "\n"
			}

		// If the request expects HTML (e.g., browser) or explicitly asked (?html=1 or ?view=html), render the info page here
		accept := c.GetHeader("Accept")
		if strings.Contains(strings.ToLower(accept), "text/html") || c.Query("html") == "1" || strings.EqualFold(c.Query("view"), "html") {
			// Build page data in service
			subURL, subJsonURL := a.subService.BuildURLs(scheme, hostWithPort, a.subPath, a.subJsonPath, subId)
			if !a.jsonEnabled {
				subJsonURL = ""
			}
			// Get base_path from context (set by middleware)
			basePath, exists := c.Get("base_path")
			if !exists {
				basePath = "/"
			}
			// Add subId to base_path for asset URLs
			basePathStr := basePath.(string)
			if basePathStr == "/" {
				basePathStr = "/" + subId + "/"
			} else {
				// Remove trailing slash if exists, add subId, then add trailing slash
				basePathStr = strings.TrimRight(basePathStr, "/") + "/" + subId + "/"
			}
			page := a.subService.BuildPageData(subId, hostHeader, traffic, lastOnline, subs, subURL, subJsonURL, basePathStr)
			logger.Infof("subController: HappEncryptedUrl length=%d, V2RayTunEncryptedUrl length=%d", 
				len(page.HappEncryptedUrl), len(page.V2RayTunEncryptedUrl))
			if len(page.HappEncryptedUrl) > 0 {
				previewLen := 50
				if len(page.HappEncryptedUrl) < previewLen {
					previewLen = len(page.HappEncryptedUrl)
				}
				logger.Infof("subController: HappEncryptedUrl preview=%s...", page.HappEncryptedUrl[:previewLen])
			}
			if len(page.V2RayTunEncryptedUrl) > 0 {
				previewLen := 50
				if len(page.V2RayTunEncryptedUrl) < previewLen {
					previewLen = len(page.V2RayTunEncryptedUrl)
				}
				logger.Infof("subController: V2RayTunEncryptedUrl preview=%s...", page.V2RayTunEncryptedUrl[:previewLen])
			}
			
			// Create JSON string for encrypted URLs
			encryptedUrlsJSON := "{}"
			encryptedUrlsData := map[string]interface{}{
				"happEncryptedUrl":    page.HappEncryptedUrl,
				"v2raytunEncryptedUrl": page.V2RayTunEncryptedUrl,
			}
			if jsonBytes, err := json.Marshal(encryptedUrlsData); err == nil {
				encryptedUrlsJSON = string(jsonBytes)
				logger.Infof("subController: Created encrypted URLs JSON, length=%d", len(encryptedUrlsJSON))
			} else {
				logger.Warningf("subController: Failed to marshal encrypted URLs JSON: %v", err)
			}
			
			c.HTML(200, "subpage.html", gin.H{
				"title":           "subscription.title",
				"cur_ver":         config.GetVersion(),
				"host":            page.Host,
				"base_path":       page.BasePath,
				"sId":             page.SId,
				"download":        page.Download,
				"upload":          page.Upload,
				"total":           page.Total,
				"used":            page.Used,
				"remained":        page.Remained,
				"expire":          page.Expire,
				"lastOnline":      page.LastOnline,
				"datepicker":      page.Datepicker,
				"downloadByte":    page.DownloadByte,
				"uploadByte":      page.UploadByte,
				"totalByte":       page.TotalByte,
			"subUrl":               page.SubUrl,
			"subJsonUrl":           page.SubJsonUrl,
			"result":               page.Result,
			"hideConfigLinks":      page.HideConfigLinks,
			"showOnlyHappV2RayTun": page.ShowOnlyHappV2RayTun,
			"happEncryptedUrl":     page.HappEncryptedUrl,
			"v2raytunEncryptedUrl": page.V2RayTunEncryptedUrl,
			"encryptedUrlsJSON":    encryptedUrlsJSON,
			"theme":                page.Theme,
			"logoUrl":              page.LogoUrl,
			"brandText":            page.BrandText,
			})
			return
		}

		// Add headers
		header := fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d", traffic.Up, traffic.Down, traffic.Total, traffic.ExpiryTime/1000)
		
		// Get client announce if available (overrides subscription header)
		clientAnnounce := ""
		db := database.GetDB()
		var clientEntity *model.ClientEntity
		err = db.Where("sub_id = ? AND enable = ?", subId, true).First(&clientEntity).Error
		if err == nil && clientEntity != nil && clientEntity.Announce != "" {
			clientAnnounce = clientEntity.Announce
		}
		
		a.ApplyCommonHeaders(c, header, a.updateInterval, a.subTitle, subId, clientAnnounce)

		if a.subEncrypt {
			c.String(200, base64.StdEncoding.EncodeToString([]byte(result)))
		} else {
			c.String(200, result)
		}
	}
}

// subJsons handles HTTP requests for JSON subscription configurations.
func (a *SUBController) subJsons(c *gin.Context) {
	subId := c.Param("subid")
	
	// Record User-Agent for metrics
	userAgent := c.GetHeader("User-Agent")
	service.RecordUserAgent(userAgent)
	
	// If encryption is enabled, check User-Agent - only allow Happ, v2raytun, or browser
	if a.subEncrypt {
		if !a.isAllowedUserAgent(c) {
			logger.Warningf("JSON subscription request blocked: encryption enabled but User-Agent not allowed (subId: %s, User-Agent: %s)", 
				subId, userAgent)
			c.String(403, "Forbidden")
			return
		}
	}
	
	_, host, _, _ := a.subService.ResolveRequest(c)
	jsonSub, header, err := a.subJsonService.GetJson(subId, host, c) // Pass context for HWID registration
	if err != nil || len(jsonSub) == 0 {
		c.String(400, "Error!")
	} else {
		// Get client announce if available (overrides subscription header)
		clientAnnounce := ""
		db := database.GetDB()
		var clientEntity *model.ClientEntity
		err = db.Where("sub_id = ? AND enable = ?", subId, true).First(&clientEntity).Error
		if err == nil && clientEntity != nil && clientEntity.Announce != "" {
			clientAnnounce = clientEntity.Announce
		}

		// Add headers
		a.ApplyCommonHeaders(c, header, a.updateInterval, a.subTitle, subId, clientAnnounce)

		c.String(200, jsonSub)
	}
}

// ApplyCommonHeaders sets common HTTP headers for subscription responses including user info, update interval, and profile title.
// Also adds X-Subscription-ID header so clients can use it as HWID if needed.
// Custom headers from settings are applied if available.
// clientAnnounce: If provided, overrides the subscription header announce setting.
func (a *SUBController) ApplyCommonHeaders(c *gin.Context, header, updateInterval, profileTitle, subId, clientAnnounce string) {
	// Apply standard headers (can be overridden by custom headers)
	c.Writer.Header().Set("Subscription-Userinfo", header)
	c.Writer.Header().Set("Profile-Update-Interval", updateInterval)
	// Only set Profile-Title if profileTitle is not empty (now only from custom headers)
	if profileTitle != "" {
		c.Writer.Header().Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte(profileTitle)))
	}
	// Add subscription ID header so clients can use it as HWID identifier
	c.Writer.Header().Set("X-Subscription-ID", subId)
	
	// Add Provider ID to HTTP header if method is "header" (according to Happ documentation: providerid)
	settingService := service.SettingService{}
	providerID, err := settingService.GetSubProviderID()
	if err == nil && providerID != "" {
		providerMethod, err := settingService.GetSubProviderIDMethod()
		if err == nil && providerMethod == "header" {
			c.Writer.Header().Set("providerid", providerID)
		}
	}
	
	// Apply custom headers from settings
	customHeaders, err := settingService.GetSubHeadersParsed()
	if err == nil && customHeaders != nil {
		a.applyCustomHeaders(c, customHeaders, header, updateInterval, profileTitle, clientAnnounce)
	} else if clientAnnounce != "" {
		// If no custom headers but client has announce, apply it directly
		if strings.HasPrefix(clientAnnounce, "base64:") {
			c.Writer.Header().Set("Announce", clientAnnounce)
		} else {
			c.Writer.Header().Set("Announce", clientAnnounce)
		}
	}
}

// applyCustomHeaders applies custom subscription headers from settings
// clientAnnounce: If provided, overrides the subscription header announce setting.
func (a *SUBController) applyCustomHeaders(c *gin.Context, headers *entity.SubscriptionHeaders, defaultUserinfo, defaultUpdateInterval, defaultProfileTitle, clientAnnounce string) {
	// Standard headers (override defaults if provided)
	if headers.ProfileTitle != "" {
		// Check if already base64 encoded
		if strings.HasPrefix(headers.ProfileTitle, "base64:") {
			c.Writer.Header().Set("Profile-Title", headers.ProfileTitle)
		} else {
			c.Writer.Header().Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte(headers.ProfileTitle)))
		}
	}
	
	if headers.SubscriptionUserinfo != "" {
		c.Writer.Header().Set("Subscription-Userinfo", headers.SubscriptionUserinfo)
	}
	
	if headers.ProfileUpdateInterval != "" {
		c.Writer.Header().Set("Profile-Update-Interval", headers.ProfileUpdateInterval)
	}
	
	if headers.SupportUrl != "" {
		c.Writer.Header().Set("Support-Url", headers.SupportUrl)
	}
	
	if headers.ProfileWebPageUrl != "" {
		c.Writer.Header().Set("Profile-Web-Page-Url", headers.ProfileWebPageUrl)
	}
	
	// Client announce takes precedence over subscription header announce
	if clientAnnounce != "" {
		// Client has custom announce - use it (overrides subscription header)
		if strings.HasPrefix(clientAnnounce, "base64:") {
			c.Writer.Header().Set("Announce", clientAnnounce)
		} else {
			c.Writer.Header().Set("Announce", clientAnnounce)
		}
	} else if headers.Announce != "" {
		// Use subscription header announce if client doesn't have one
		if strings.HasPrefix(headers.Announce, "base64:") {
			c.Writer.Header().Set("Announce", headers.Announce)
		} else {
			c.Writer.Header().Set("Announce", headers.Announce)
		}
	}
	
	if headers.AnnounceUrl != "" {
		c.Writer.Header().Set("Announce-Url", headers.AnnounceUrl)
	}
	
	if headers.Routing != "" {
		c.Writer.Header().Set("Routing", headers.Routing)
	}
	
	if headers.RoutingEnable != "" {
		c.Writer.Header().Set("Routing-Enable", headers.RoutingEnable)
	}
	
	if headers.CustomTunnelConfig != "" {
		c.Writer.Header().Set("Custom-Tunnel-Config", headers.CustomTunnelConfig)
	}
	
	// Extended Happ headers
	if headers.NewUrl != "" {
		c.Writer.Header().Set("New-Url", headers.NewUrl)
	}
	
	if headers.NewDomain != "" {
		c.Writer.Header().Set("New-Domain", headers.NewDomain)
	}
	
	if headers.ServerDescription != "" {
		c.Writer.Header().Set("Server-Description", headers.ServerDescription)
	}
	
	if headers.SubExpire != "" {
		c.Writer.Header().Set("Sub-Expire", headers.SubExpire)
	}
	
	if headers.SubExpireButtonLink != "" {
		c.Writer.Header().Set("Sub-Expire-Button-Link", headers.SubExpireButtonLink)
	}
	
	if headers.SubInfoColor != "" {
		c.Writer.Header().Set("Sub-Info-Color", headers.SubInfoColor)
	}
	
	if headers.SubInfoText != "" {
		c.Writer.Header().Set("Sub-Info-Text", headers.SubInfoText)
	}
	
	if headers.SubInfoButtonText != "" {
		c.Writer.Header().Set("Sub-Info-Button-Text", headers.SubInfoButtonText)
	}
	
	if headers.SubInfoButtonLink != "" {
		c.Writer.Header().Set("Sub-Info-Button-Link", headers.SubInfoButtonLink)
	}
	
	// Apply all other Happ headers (using reflection to avoid missing any)
	headersValue := reflect.ValueOf(headers).Elem()
	headersType := headersValue.Type()
	
	for i := 0; i < headersType.NumField(); i++ {
		field := headersType.Field(i)
		fieldValue := headersValue.Field(i)
		
		// Skip if empty or already processed
		if fieldValue.Kind() != reflect.String || fieldValue.String() == "" {
			continue
		}
		
		// Skip standard headers we already processed
		fieldName := field.Name
		if fieldName == "ProfileTitle" || fieldName == "SubscriptionUserinfo" || 
		   fieldName == "ProfileUpdateInterval" || fieldName == "SupportUrl" ||
		   fieldName == "ProfileWebPageUrl" || fieldName == "Announce" ||
		   fieldName == "AnnounceUrl" || fieldName == "Routing" ||
		   fieldName == "RoutingEnable" || fieldName == "CustomTunnelConfig" ||
		   fieldName == "NewUrl" || fieldName == "NewDomain" ||
		   fieldName == "ServerDescription" || fieldName == "SubExpire" ||
		   fieldName == "SubExpireButtonLink" || fieldName == "SubInfoColor" ||
		   fieldName == "SubInfoText" || fieldName == "SubInfoButtonText" ||
		   fieldName == "SubInfoButtonLink" {
			continue
		}
		
		// Convert field name to header name (e.g., SubscriptionAutoconnect -> Subscription-Autoconnect)
		headerName := a.fieldNameToHeaderName(fieldName)
		c.Writer.Header().Set(headerName, fieldValue.String())
	}
}

// fieldNameToHeaderName converts Go field name to HTTP header name
// Example: SubscriptionAutoconnect -> Subscription-Autoconnect
func (a *SUBController) fieldNameToHeaderName(fieldName string) string {
	var result strings.Builder
	for i, r := range fieldName {
		if i > 0 && r >= 'A' && r <= 'Z' {
			result.WriteByte('-')
		}
		result.WriteRune(r)
	}
	return result.String()
}
