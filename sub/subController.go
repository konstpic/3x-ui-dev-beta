package sub

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mhsanaei/3x-ui/v2/config"
	"github.com/mhsanaei/3x-ui/v2/logger"
	service "github.com/mhsanaei/3x-ui/v2/web/service"

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

// subs handles HTTP requests for subscription links, returning either HTML page or base64-encoded subscription data.
func (a *SUBController) subs(c *gin.Context) {
	subId := c.Param("subid")
	scheme, host, hostWithPort, hostHeader := a.subService.ResolveRequest(c)
	subs, lastOnline, traffic, err := a.subService.GetSubs(subId, host, c) // Pass context for HWID registration
	if err != nil || len(subs) == 0 {
		c.String(400, "Error!")
	} else {
		result := ""
		for _, sub := range subs {
			result += sub + "\n"
		}

		// Get User-Agent and log it
		userAgent := c.GetHeader("User-Agent")
		logger.Infof("subController: Request to /sub/%s from User-Agent: %s", subId, userAgent)
		
		// Check if request is from Happ or V2RayTun app and encryption is enabled
		userAgentLower := strings.ToLower(userAgent)
		isHappApp := strings.Contains(userAgentLower, "happ")
		isV2RayTunApp := strings.Contains(userAgentLower, "v2raytun")
		
		// If request is from Happ or V2RayTun app, return encrypted URL directly
		if (isHappApp || isV2RayTunApp) {
			// Build URLs to get encrypted versions
			subURL, subJsonURL := a.subService.BuildURLs(scheme, hostWithPort, a.subPath, a.subJsonPath, subId)
			basePath, exists := c.Get("base_path")
			if !exists {
				basePath = "/"
			}
			basePathStr := basePath.(string)
			if basePathStr == "/" {
				basePathStr = "/" + subId + "/"
			} else {
				basePathStr = strings.TrimRight(basePathStr, "/") + "/" + subId + "/"
			}
			page := a.subService.BuildPageData(subId, hostHeader, traffic, lastOnline, subs, subURL, subJsonURL, basePathStr)
			
			if isHappApp && page.HappEncryptedUrl != "" {
				logger.Infof("subController: Detected Happ app, returning encrypted URL: %s", page.HappEncryptedUrl)
				c.String(200, page.HappEncryptedUrl)
				return
			} else if isV2RayTunApp && page.V2RayTunEncryptedUrl != "" {
				logger.Infof("subController: Detected V2RayTun app, returning encrypted URL: %s", page.V2RayTunEncryptedUrl)
				c.String(200, page.V2RayTunEncryptedUrl)
				return
			} else {
				logger.Warningf("subController: Detected app (%s) but encryption is disabled, returning plain result", userAgent)
			}
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
			})
			return
		}

		// Add headers
		header := fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d", traffic.Up, traffic.Down, traffic.Total, traffic.ExpiryTime/1000)
		a.ApplyCommonHeaders(c, header, a.updateInterval, a.subTitle, subId)

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
	_, host, _, _ := a.subService.ResolveRequest(c)
	jsonSub, header, err := a.subJsonService.GetJson(subId, host, c) // Pass context for HWID registration
	if err != nil || len(jsonSub) == 0 {
		c.String(400, "Error!")
	} else {

		// Add headers
		a.ApplyCommonHeaders(c, header, a.updateInterval, a.subTitle, subId)

		c.String(200, jsonSub)
	}
}

// ApplyCommonHeaders sets common HTTP headers for subscription responses including user info, update interval, and profile title.
// Also adds X-Subscription-ID header so clients can use it as HWID if needed.
func (a *SUBController) ApplyCommonHeaders(c *gin.Context, header, updateInterval, profileTitle, subId string) {
	c.Writer.Header().Set("Subscription-Userinfo", header)
	c.Writer.Header().Set("Profile-Update-Interval", updateInterval)
	c.Writer.Header().Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte(profileTitle)))
	// Add subscription ID header so clients can use it as HWID identifier
	c.Writer.Header().Set("X-Subscription-ID", subId)
}
