package controller

import (
	"fmt"
	"os"
	"time"

	"github.com/konstpic/sharx-code/v2/web/service"

	"github.com/gin-gonic/gin"
)

// MigrationController handles SQLite to PostgreSQL migration operations.
type MigrationController struct {
	BaseController
	migrationService service.MigrationService
	panelService     service.PanelService
}

// NewMigrationController creates a new MigrationController and initializes its routes.
func NewMigrationController(g *gin.RouterGroup) *MigrationController {
	a := &MigrationController{
		panelService: service.PanelService{},
	}
	a.initRouter(g)
	return a
}

// initRouter sets up the routes for migration management.
func (a *MigrationController) initRouter(g *gin.RouterGroup) {
	g = g.Group("/migration")

	g.POST("/preview", a.previewMigration)
	g.POST("/execute", a.executeMigration)
}

// previewMigration handles preview of migration data from SQLite file.
func (a *MigrationController) previewMigration(c *gin.Context) {
	// Get uploaded file
	file, err := c.FormFile("file")
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.migration.error.uploadFile"), err)
		return
	}

	// Validate file size (max 100MB)
	if file.Size > 100*1024*1024 {
		jsonMsg(c, I18nWeb(c, "pages.settings.migration.error.fileTooLarge"), 
			gin.Error{Err: err, Type: gin.ErrorTypePublic})
		return
	}

	// Open uploaded file
	src, err := file.Open()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.migration.error.openFile"), err)
		return
	}
	defer src.Close()

	// Save to temporary file
	tempPath, err := a.migrationService.SaveUploadedFile(src, file.Filename)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.migration.error.saveFile"), err)
		return
	}
	defer os.Remove(tempPath)

	// Preview migration
	preview, err := a.migrationService.PreviewMigration(tempPath)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.migration.error.preview"), err)
		return
	}

	jsonObj(c, preview, nil)
}

// executeMigration handles execution of migration from SQLite to PostgreSQL.
func (a *MigrationController) executeMigration(c *gin.Context) {
	// Get uploaded file
	file, err := c.FormFile("file")
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.migration.error.uploadFile"), err)
		return
	}

	// Validate file size (max 100MB)
	if file.Size > 100*1024*1024 {
		jsonMsg(c, I18nWeb(c, "pages.settings.migration.error.fileTooLarge"), 
			gin.Error{Err: err, Type: gin.ErrorTypePublic})
		return
	}

	// Open uploaded file
	src, err := file.Open()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.migration.error.openFile"), err)
		return
	}
	defer src.Close()

	// Save to temporary file
	tempPath, err := a.migrationService.SaveUploadedFile(src, file.Filename)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.migration.error.saveFile"), err)
		return
	}
	defer os.Remove(tempPath)

	// Execute migration
	result, err := a.migrationService.ExecuteMigration(tempPath)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.migration.error.execute"), err)
		return
	}

	if !result.Success {
		// Return result even if not successful, so frontend can display errors
		jsonObj(c, result, nil)
		return
	}

	// Restart container after successful migration to apply new settings
	if result.Success {
		if err := a.panelService.RestartContainer(time.Second * 3); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("Failed to restart container: %v", err))
		}
	}

	jsonObj(c, result, nil)
}

