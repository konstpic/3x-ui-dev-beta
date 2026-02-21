// Package service provides migration service for SQLite to PostgreSQL data migration.
package service

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mhsanaei/3x-ui/v2/database"
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/logger"
	"github.com/mhsanaei/3x-ui/v2/util/common"

	_ "modernc.org/sqlite"
	"gorm.io/gorm"
)

// MigrationService provides SQLite to PostgreSQL migration functionality.
type MigrationService struct{}

// MigrationPreview contains preview information about data to be migrated.
type MigrationPreview struct {
	UsersCount            int    `json:"usersCount"`
	SettingsCount         int    `json:"settingsCount"`
	InboundsCount         int    `json:"inboundsCount"`
	ClientsCount          int    `json:"clientsCount"`
	ClientTrafficsCount   int    `json:"clientTrafficsCount"`
	InboundClientIpsCount int    `json:"inboundClientIpsCount"`
	OutboundTrafficsCount int    `json:"outboundTrafficsCount"`
	HistoryOfSeedersCount int    `json:"historyOfSeedersCount"`
	PanelSettings         *PanelSettingsPreview `json:"panelSettings,omitempty"`
	Errors                []string `json:"errors,omitempty"`
}

// PanelSettingsPreview contains panel settings found in source database.
// NOTE: These settings are intentionally NOT migrated to preserve current panel configuration.
type PanelSettingsPreview struct {
	WebPort     string `json:"webPort,omitempty"`
	WebBasePath string `json:"webBasePath,omitempty"`
	WebListen   string `json:"webListen,omitempty"`
	WebDomain   string `json:"webDomain,omitempty"`
	WebCertFile string `json:"webCertFile,omitempty"`
	WebKeyFile  string `json:"webKeyFile,omitempty"`
	// Ignored indicates that these settings will NOT be migrated
	Ignored bool `json:"ignored"`
}

// MigrationResult contains the result of migration execution.
type MigrationResult struct {
	Success               bool     `json:"success"`
	UsersMigrated         int      `json:"usersMigrated"`
	SettingsMigrated      int      `json:"settingsMigrated"`
	InboundsMigrated      int      `json:"inboundsMigrated"`
	ClientsMigrated       int      `json:"clientsMigrated"`
	ClientTrafficsMigrated int     `json:"clientTrafficsMigrated"`
	InboundClientIpsMigrated int   `json:"inboundClientIpsMigrated"`
	OutboundTrafficsMigrated int  `json:"outboundTrafficsMigrated"`
	HistoryOfSeedersMigrated int  `json:"historyOfSeedersMigrated"`
	PanelSettingsIgnored   []string `json:"panelSettingsIgnored,omitempty"` // List of panel settings that were NOT migrated
	Errors                []string `json:"errors,omitempty"`
	Warnings              []string `json:"warnings,omitempty"`
}

// OldSQLiteInbound represents the old SQLite inbound structure.
type OldSQLiteInbound struct {
	Id                   int
	UserId               sql.NullInt64
	Up                   sql.NullInt64
	Down                 sql.NullInt64
	Total                sql.NullInt64
	AllTime              sql.NullInt64
	Remark               sql.NullString
	Enable               sql.NullBool
	ExpiryTime           sql.NullInt64
	TrafficReset         sql.NullString
	LastTrafficResetTime sql.NullInt64
	Listen               sql.NullString
	Port                 sql.NullInt64
	Protocol             sql.NullString
	Settings             sql.NullString
	StreamSettings       sql.NullString
	Tag                  sql.NullString
	Sniffing             sql.NullString
}

// OldSQLiteClientTraffic represents the old SQLite client_traffics structure.
type OldSQLiteClientTraffic struct {
	Id         int
	InboundId  sql.NullInt64
	Enable     sql.NullBool
	Email      sql.NullString
	Up         sql.NullInt64
	Down       sql.NullInt64
	AllTime    sql.NullInt64
	ExpiryTime sql.NullInt64
	Total      sql.NullInt64
	Reset      sql.NullInt64
	LastOnline sql.NullInt64
}

// PreviewMigration analyzes SQLite database and returns preview of data to be migrated.
func (s *MigrationService) PreviewMigration(sqliteFilePath string) (*MigrationPreview, error) {
	preview := &MigrationPreview{
		Errors: []string{},
	}

	// Open SQLite database
	sqliteDB, err := sql.Open("sqlite", sqliteFilePath)
	if err != nil {
		return nil, common.NewErrorf("Failed to open SQLite database: %v", err)
	}
	defer sqliteDB.Close()

	// Test connection
	if err := sqliteDB.Ping(); err != nil {
		return nil, common.NewErrorf("Failed to connect to SQLite database: %v", err)
	}

	// Count records in each table
	preview.UsersCount = s.countTable(sqliteDB, "users")
	preview.SettingsCount = s.countTable(sqliteDB, "settings")
	preview.InboundsCount = s.countTable(sqliteDB, "inbounds")
	preview.ClientTrafficsCount = s.countTable(sqliteDB, "client_traffics")
	preview.InboundClientIpsCount = s.countTable(sqliteDB, "inbound_client_ips")
	preview.OutboundTrafficsCount = s.countTable(sqliteDB, "outbound_traffics")
	preview.HistoryOfSeedersCount = s.countTable(sqliteDB, "history_of_seeders")

	// Count clients from inbounds.settings JSON
	clientsCount, err := s.countClientsFromInbounds(sqliteDB)
	if err != nil {
		preview.Errors = append(preview.Errors, fmt.Sprintf("Failed to count clients: %v", err))
	} else {
		preview.ClientsCount = clientsCount
	}

	// Read panel settings from SQLite
	panelSettings, err := s.readPanelSettings(sqliteDB)
	if err != nil {
		preview.Errors = append(preview.Errors, fmt.Sprintf("Failed to read panel settings: %v", err))
	} else {
		preview.PanelSettings = panelSettings
	}

	return preview, nil
}

// ExecuteMigration performs the actual migration from SQLite to PostgreSQL.
func (s *MigrationService) ExecuteMigration(sqliteFilePath string) (*MigrationResult, error) {
	result := &MigrationResult{
		Success:  false,
		Errors:   []string{},
		Warnings: []string{},
	}

	// Open SQLite database
	sqliteDB, err := sql.Open("sqlite", sqliteFilePath)
	if err != nil {
		return nil, common.NewErrorf("Failed to open SQLite database: %v", err)
	}
	defer sqliteDB.Close()

	// Test connection
	if err := sqliteDB.Ping(); err != nil {
		return nil, common.NewErrorf("Failed to connect to SQLite database: %v", err)
	}

	// Get PostgreSQL database
	pgDB := database.GetDB()
	if pgDB == nil {
		return nil, common.NewError("PostgreSQL database connection is not available")
	}

	// Start transaction
	tx := pgDB.Begin()
	if tx.Error != nil {
		return nil, common.NewErrorf("Failed to start transaction: %v", tx.Error)
	}

	// Rollback on error
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			result.Errors = append(result.Errors, fmt.Sprintf("Panic during migration: %v", r))
		}
	}()

	// Migrate users
	if count, err := s.migrateUsers(sqliteDB, tx); err != nil {
		tx.Rollback()
		result.Errors = append(result.Errors, fmt.Sprintf("Failed to migrate users: %v", err))
		return result, err
	} else {
		result.UsersMigrated = count
	}

	// Migrate settings
	if count, err := s.migrateSettings(sqliteDB, tx); err != nil {
		tx.Rollback()
		result.Errors = append(result.Errors, fmt.Sprintf("Failed to migrate settings: %v", err))
		return result, err
	} else {
		result.SettingsMigrated = count
	}

	// Migrate inbounds (without clients extraction yet)
	if count, err := s.migrateInbounds(sqliteDB, tx); err != nil {
		tx.Rollback()
		result.Errors = append(result.Errors, fmt.Sprintf("Failed to migrate inbounds: %v", err))
		return result, err
	} else {
		result.InboundsMigrated = count
	}

	// Extract and migrate clients from inbounds.settings
	if count, warnings, err := s.migrateClientsFromInbounds(sqliteDB, tx); err != nil {
		tx.Rollback()
		result.Errors = append(result.Errors, fmt.Sprintf("Failed to migrate clients: %v", err))
		return result, err
	} else {
		result.ClientsMigrated = count
		result.Warnings = append(result.Warnings, warnings...)
	}

	// Migrate client traffics (merge into client_entities)
	if count, warnings, err := s.migrateClientTraffics(sqliteDB, tx); err != nil {
		tx.Rollback()
		result.Errors = append(result.Errors, fmt.Sprintf("Failed to migrate client traffics: %v", err))
		return result, err
	} else {
		result.ClientTrafficsMigrated = count
		result.Warnings = append(result.Warnings, warnings...)
	}

	// Migrate inbound_client_ips
	if count, err := s.migrateInboundClientIps(sqliteDB, tx); err != nil {
		tx.Rollback()
		result.Errors = append(result.Errors, fmt.Sprintf("Failed to migrate inbound_client_ips: %v", err))
		return result, err
	} else {
		result.InboundClientIpsMigrated = count
	}

	// Migrate outbound_traffics
	if count, err := s.migrateOutboundTraffics(sqliteDB, tx); err != nil {
		tx.Rollback()
		result.Errors = append(result.Errors, fmt.Sprintf("Failed to migrate outbound_traffics: %v", err))
		return result, err
	} else {
		result.OutboundTrafficsMigrated = count
	}

	// Migrate history_of_seeders
	if count, err := s.migrateHistoryOfSeeders(sqliteDB, tx); err != nil {
		tx.Rollback()
		result.Errors = append(result.Errors, fmt.Sprintf("Failed to migrate history_of_seeders: %v", err))
		return result, err
	} else {
		result.HistoryOfSeedersMigrated = count
	}

	// Rebuild inbounds.settings from client_entities (clients are now in separate table)
	if err := s.rebuildInboundsSettings(tx); err != nil {
		tx.Rollback()
		result.Errors = append(result.Errors, fmt.Sprintf("Failed to rebuild inbounds settings: %v", err))
		return result, err
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("Failed to commit transaction: %v", err))
		return result, err
	}

	// NOTE: Panel settings are intentionally NOT migrated to preserve current panel configuration.
	// This prevents accidentally changing the panel's access settings during migration.
	// Collect all ignored settings for reporting
	result.PanelSettingsIgnored = []string{}
	for key := range ignoredPanelSettings {
		result.PanelSettingsIgnored = append(result.PanelSettingsIgnored, key)
	}
	logger.Info("Migration completed. Panel settings were NOT migrated to preserve current configuration.")

	result.Success = true
	return result, nil
}

// SaveUploadedFile saves uploaded SQLite file to temporary location.
func (s *MigrationService) SaveUploadedFile(file io.Reader, filename string) (string, error) {
	// Create temp directory if it doesn't exist
	tempDir := filepath.Join(os.TempDir(), "x-ui-migration")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", common.NewErrorf("Failed to create temp directory: %v", err)
	}

	// Create temp file
	tempFile, err := os.CreateTemp(tempDir, "x-ui-*.db")
	if err != nil {
		return "", common.NewErrorf("Failed to create temp file: %v", err)
	}
	defer tempFile.Close()

	// Copy file content
	if _, err := io.Copy(tempFile, file); err != nil {
		os.Remove(tempFile.Name())
		return "", common.NewErrorf("Failed to save uploaded file: %v", err)
	}

	return tempFile.Name(), nil
}

// Helper functions

func (s *MigrationService) countTable(db *sql.DB, tableName string) int {
	var count int
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s", tableName)
	err := db.QueryRow(query).Scan(&count)
	if err != nil {
		logger.Warningf("Failed to count table %s: %v", tableName, err)
		return 0
	}
	return count
}

func (s *MigrationService) countClientsFromInbounds(db *sql.DB) (int, error) {
	rows, err := db.Query("SELECT settings FROM inbounds WHERE settings IS NOT NULL AND settings != ''")
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	totalClients := 0
	for rows.Next() {
		var settingsJSON sql.NullString
		if err := rows.Scan(&settingsJSON); err != nil {
			continue
		}
		if !settingsJSON.Valid {
			continue
		}

		var settings map[string]interface{}
		if err := json.Unmarshal([]byte(settingsJSON.String), &settings); err != nil {
			continue
		}

		clients, ok := settings["clients"].([]interface{})
		if ok {
			totalClients += len(clients)
		}
	}

	return totalClients, nil
}

func (s *MigrationService) migrateUsers(sqliteDB *sql.DB, tx *gorm.DB) (int, error) {
	rows, err := sqliteDB.Query("SELECT id, username, password FROM users")
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id int
		var username, password sql.NullString

		if err := rows.Scan(&id, &username, &password); err != nil {
			continue
		}

		// Check if user already exists
		var existing model.User
		if err := tx.Where("id = ?", id).First(&existing).Error; err == nil {
			// User exists, skip
			continue
		}

		user := model.User{
			Id:       id,
			Username: username.String,
			Password: password.String,
		}

		if err := tx.Create(&user).Error; err != nil {
			logger.Warningf("Failed to migrate user %d: %v", id, err)
			continue
		}

		count++
	}

	return count, nil
}

// ignoredPanelSettings contains settings that should NOT be migrated
// to preserve the current panel configuration (port, paths, certificates, etc.)
var ignoredPanelSettings = map[string]bool{
	"webPort":                      true,
	"webBasePath":                  true,
	"secret":                       true,
	"webCertFile":                  true,
	"webKeyFile":                   true,
	"xrayTemplateConfig":           true,
	"webListen":                    true,
	"webDomain":                    true,
	"sessionMaxAge":                true,
	"pageSize":                     true,
	"expireDiff":                   true,
	"trafficDiff":                  true,
	"remarkModel":                  true,
	"datepicker":                   true,
	"tgBotEnable":                  true,
	"tgBotToken":                   true,
	"tgBotProxy":                   true,
	"tgBotAPIServer":               true,
	"tgBotChatId":                  true,
	"tgRunTime":                    true,
	"tgBotBackup":                  true,
	"tgBotLoginNotify":             true,
	"tgCpu":                        true,
	"tgLang":                       true,
	"timeLocation":                 true,
	"twoFactorEnable":              true,
	"twoFactorToken":               true,
	"subEnable":                    true,
	"subJsonEnable":                true,
	"subTitle":                     true,
	"subListen":                    true,
	"subPort":                      true,
	"subPath":                      true,
	"subDomain":                    true,
	"subCertFile":                  true,
	"subKeyFile":                   true,
	"subUpdates":                   true,
	"externalTrafficInformEnable":  true,
	"externalTrafficInformURI":     true,
	"subEncrypt":                   true,
	"subShowInfo":                  true,
	"subURI":                       true,
	"subJsonPath":                  true,
	"subJsonURI":                   true,
	"subJsonFragment":              true,
	"subJsonNoises":                true,
	"subJsonMux":                   true,
	"subJsonRules":                 true,
	"ldapEnable":                   true,
	"ldapHost":                     true,
	"ldapPort":                     true,
	"ldapUseTLS":                   true,
	"ldapBindDN":                   true,
	"ldapPassword":                  true,
	"ldapBaseDN":                   true,
	"ldapUserFilter":               true,
	"ldapUserAttr":                 true,
	"ldapVlessField":               true,
	"ldapSyncCron":                 true,
	"ldapFlagField":                true,
	"ldapTruthyValues":             true,
	"ldapInvertFlag":               true,
	"ldapInboundTags":              true,
	"ldapAutoCreate":               true,
	"ldapAutoDelete":               true,
	"ldapDefaultTotalGB":           true,
	"ldapDefaultExpiryDays":        true,
	"ldapDefaultLimitIP":           true,
}

func (s *MigrationService) migrateSettings(sqliteDB *sql.DB, tx *gorm.DB) (int, error) {
	rows, err := sqliteDB.Query("SELECT id, key, value FROM settings")
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id int
		var key, value sql.NullString

		if err := rows.Scan(&id, &key, &value); err != nil {
			continue
		}

		// Skip panel-specific settings to preserve current panel configuration
		if ignoredPanelSettings[key.String] {
			logger.Debugf("Skipping panel setting during migration: %s", key.String)
			continue
		}

		// Check if setting already exists
		var existing model.Setting
		if err := tx.Where("key = ?", key.String).First(&existing).Error; err == nil {
			// Update existing setting
			if err := tx.Model(&existing).Update("value", value.String).Error; err != nil {
				logger.Warningf("Failed to update setting %s: %v", key.String, err)
				continue
			}
		} else {
			// Create new setting
			setting := model.Setting{
				Key:   key.String,
				Value: value.String,
			}
			if err := tx.Create(&setting).Error; err != nil {
				logger.Warningf("Failed to migrate setting %s: %v", key.String, err)
				continue
			}
		}

		count++
	}

	return count, nil
}

func (s *MigrationService) migrateInbounds(sqliteDB *sql.DB, tx *gorm.DB) (int, error) {
	rows, err := sqliteDB.Query(`
		SELECT id, user_id, up, down, total, all_time, remark, enable, expiry_time,
		       traffic_reset, last_traffic_reset_time, listen, port, protocol,
		       settings, stream_settings, tag, sniffing
		FROM inbounds
	`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var oldInbound OldSQLiteInbound
		if err := rows.Scan(
			&oldInbound.Id, &oldInbound.UserId, &oldInbound.Up, &oldInbound.Down,
			&oldInbound.Total, &oldInbound.AllTime, &oldInbound.Remark, &oldInbound.Enable,
			&oldInbound.ExpiryTime, &oldInbound.TrafficReset, &oldInbound.LastTrafficResetTime,
			&oldInbound.Listen, &oldInbound.Port, &oldInbound.Protocol,
			&oldInbound.Settings, &oldInbound.StreamSettings, &oldInbound.Tag, &oldInbound.Sniffing,
		); err != nil {
			continue
		}

		inbound := model.Inbound{
			Id:                   oldInbound.Id,
			UserId:               int(oldInbound.UserId.Int64),
			Up:                   oldInbound.Up.Int64,
			Down:                 oldInbound.Down.Int64,
			Total:                oldInbound.Total.Int64,
			AllTime:              oldInbound.AllTime.Int64,
			Remark:               oldInbound.Remark.String,
			Enable:               oldInbound.Enable.Bool,
			ExpiryTime:           oldInbound.ExpiryTime.Int64,
			TrafficReset:         oldInbound.TrafficReset.String,
			LastTrafficResetTime: oldInbound.LastTrafficResetTime.Int64,
			Listen:               oldInbound.Listen.String,
			Port:                 int(oldInbound.Port.Int64),
			Protocol:              model.Protocol(oldInbound.Protocol.String),
			Settings:             oldInbound.Settings.String,
			StreamSettings:       oldInbound.StreamSettings.String,
			Tag:                  oldInbound.Tag.String,
			Sniffing:             oldInbound.Sniffing.String,
		}

		// Check if inbound already exists
		var existing model.Inbound
		if err := tx.Where("id = ?", oldInbound.Id).First(&existing).Error; err == nil {
			// Inbound exists, update all fields
			inbound.Id = existing.Id // Preserve ID
			if err := tx.Save(&inbound).Error; err != nil {
				logger.Warningf("Failed to update inbound %d: %v", oldInbound.Id, err)
				continue
			}
		} else {
			// Create new inbound
			if err := tx.Create(&inbound).Error; err != nil {
				logger.Warningf("Failed to migrate inbound %d: %v", oldInbound.Id, err)
				continue
			}
		}

		count++
	}

	return count, nil
}

func (s *MigrationService) migrateClientsFromInbounds(sqliteDB *sql.DB, tx *gorm.DB) (int, []string, error) {
	rows, err := sqliteDB.Query(`
		SELECT id, settings, protocol FROM inbounds
		WHERE settings IS NOT NULL AND settings != ''
	`)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()

	clientEmailMap := make(map[string]*model.ClientEntity) // email -> client
	count := 0
	warnings := []string{}

	for rows.Next() {
		var inboundId int
		var settingsJSON, protocol sql.NullString

		if err := rows.Scan(&inboundId, &settingsJSON, &protocol); err != nil {
			continue
		}

		if !settingsJSON.Valid {
			continue
		}

		var settings map[string]interface{}
		if err := json.Unmarshal([]byte(settingsJSON.String), &settings); err != nil {
			warnings = append(warnings, fmt.Sprintf("Failed to parse settings for inbound %d: %v", inboundId, err))
			continue
		}

		clients, ok := settings["clients"].([]interface{})
		if !ok {
			continue
		}

		for _, clientData := range clients {
			clientMap, ok := clientData.(map[string]interface{})
			if !ok {
				continue
			}

			// Extract client fields
			email := s.getStringFromMap(clientMap, "email")
			if email == "" {
				warnings = append(warnings, fmt.Sprintf("Skipping client without email in inbound %d", inboundId))
				continue
			}

			// Normalize email to lowercase
			email = strings.ToLower(email)

			// Check if client already exists (by email)
			var clientEntity *model.ClientEntity
			if existing, exists := clientEmailMap[email]; exists {
				clientEntity = existing
			} else {
				// Check in database
				var existingDB model.ClientEntity
				if err := tx.Where("LOWER(email) = ?", email).First(&existingDB).Error; err == nil {
				// Client exists, update it
				existingDB.Enable = s.getBoolFromMap(clientMap, "enable", true)
				existingDB.Status = "active"
				// LimitIP removed - using HWID only
				// Convert bytes to GB (old format stores bytes, new format stores GB)
				totalBytes := s.getInt64FromMap(clientMap, "totalGB")
				existingDB.TotalGB = float64(totalBytes) / (1024 * 1024 * 1024)
				existingDB.ExpiryTime = s.getInt64FromMap(clientMap, "expiryTime")
					existingDB.TgID = s.getInt64FromMap(clientMap, "tgId")
					existingDB.SubID = s.getStringFromMap(clientMap, "subId")
					existingDB.Comment = s.getStringFromMap(clientMap, "comment")
					existingDB.Reset = int(s.getInt64FromMap(clientMap, "reset"))
					
					// Update UUID/ID or Password based on protocol
					protocolStr := protocol.String
					if protocolStr == "vmess" || protocolStr == "vless" {
						if uuid := s.getStringFromMap(clientMap, "id"); uuid != "" {
							existingDB.UUID = uuid
						}
						if protocolStr == "vmess" {
							if security := s.getStringFromMap(clientMap, "security"); security != "" {
								existingDB.Security = security
							}
						}
						if protocolStr == "vless" {
							if flow := s.getStringFromMap(clientMap, "flow"); flow != "" {
								existingDB.Flow = flow
							}
						}
					} else if protocolStr == "trojan" || protocolStr == "shadowsocks" {
						if password := s.getStringFromMap(clientMap, "password"); password != "" {
							existingDB.Password = password
						}
					}
					
					if err := tx.Save(&existingDB).Error; err != nil {
						warnings = append(warnings, fmt.Sprintf("Failed to update client %s: %v", email, err))
						continue
					}
					
					clientEntity = &existingDB
					clientEmailMap[email] = clientEntity
				} else {
				// Create new client entity
				// Convert bytes to GB (old format stores bytes, new format stores GB)
				totalBytesNew := s.getInt64FromMap(clientMap, "totalGB")
				clientEntity = &model.ClientEntity{
					UserId:     1, // Default user
					Email:      email,
					Enable:     s.getBoolFromMap(clientMap, "enable", true),
					Status:     "active",
					// LimitIP removed - using HWID only
					TotalGB:    float64(totalBytesNew) / (1024 * 1024 * 1024),
					ExpiryTime: s.getInt64FromMap(clientMap, "expiryTime"),
					TgID:       s.getInt64FromMap(clientMap, "tgId"),
					SubID:      s.getStringFromMap(clientMap, "subId"),
					Comment:    s.getStringFromMap(clientMap, "comment"),
					Reset:      int(s.getInt64FromMap(clientMap, "reset")),
					CreatedAt:  s.getInt64FromMap(clientMap, "created_at"),
					UpdatedAt:  s.getInt64FromMap(clientMap, "updated_at"),
				}

					// Set UUID/ID or Password based on protocol
					protocolStr := protocol.String
					if protocolStr == "vmess" || protocolStr == "vless" {
						clientEntity.UUID = s.getStringFromMap(clientMap, "id")
						if protocolStr == "vmess" {
							clientEntity.Security = s.getStringFromMap(clientMap, "security")
						}
						if protocolStr == "vless" {
							clientEntity.Flow = s.getStringFromMap(clientMap, "flow")
						}
					} else if protocolStr == "trojan" || protocolStr == "shadowsocks" {
						clientEntity.Password = s.getStringFromMap(clientMap, "password")
					}

					// Set defaults for timestamps
					now := time.Now().Unix() * 1000
					if clientEntity.CreatedAt == 0 {
						clientEntity.CreatedAt = now
					}
					if clientEntity.UpdatedAt == 0 {
						clientEntity.UpdatedAt = now
					}

					// Create client entity
					if err := tx.Create(clientEntity).Error; err != nil {
						warnings = append(warnings, fmt.Sprintf("Failed to create client %s: %v", email, err))
						continue
					}

					clientEmailMap[email] = clientEntity
					count++
				}
			}

			// Create client-inbound mapping
			mapping := model.ClientInboundMapping{
				ClientId:  clientEntity.Id,
				InboundId: inboundId,
			}

			// Check if mapping already exists
			var existingMapping model.ClientInboundMapping
			if err := tx.Where("client_id = ? AND inbound_id = ?", clientEntity.Id, inboundId).First(&existingMapping).Error; err == nil {
				// Mapping exists, skip
				continue
			}

			if err := tx.Create(&mapping).Error; err != nil {
				warnings = append(warnings, fmt.Sprintf("Failed to create mapping for client %s and inbound %d: %v", email, inboundId, err))
				continue
			}
		}
	}

	return count, warnings, nil
}

func (s *MigrationService) migrateClientTraffics(sqliteDB *sql.DB, tx *gorm.DB) (int, []string, error) {
	rows, err := sqliteDB.Query(`
		SELECT inbound_id, enable, email, up, down, all_time, expiry_time, total, reset, last_online
		FROM client_traffics
	`)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()

	count := 0
	warnings := []string{}

	for rows.Next() {
		var oldTraffic OldSQLiteClientTraffic
		if err := rows.Scan(
			&oldTraffic.InboundId, &oldTraffic.Enable, &oldTraffic.Email,
			&oldTraffic.Up, &oldTraffic.Down, &oldTraffic.AllTime,
			&oldTraffic.ExpiryTime, &oldTraffic.Total, &oldTraffic.Reset, &oldTraffic.LastOnline,
		); err != nil {
			continue
		}

		if !oldTraffic.Email.Valid || oldTraffic.Email.String == "" {
			continue
		}

		// Normalize email to lowercase (same as in migrateClientsFromInbounds)
		email := strings.ToLower(oldTraffic.Email.String)

		// Find client by email (case-insensitive search using LOWER function for PostgreSQL)
		var client model.ClientEntity
		if err := tx.Where("LOWER(email) = ?", email).First(&client).Error; err != nil {
			warnings = append(warnings, fmt.Sprintf("Client with email %s not found for traffic migration", email))
			continue
		}

		// Update traffic statistics
		up := oldTraffic.Up.Int64
		down := oldTraffic.Down.Int64
		allTime := oldTraffic.AllTime.Int64
		lastOnline := oldTraffic.LastOnline.Int64

		// Merge traffic (add to existing)
		// Get current values first
		currentUp := client.Up
		currentDown := client.Down
		currentAllTime := client.AllTime

		updates := map[string]interface{}{
			"up":       currentUp + up,
			"down":     currentDown + down,
			"all_time": currentAllTime + allTime,
		}

		if lastOnline > client.LastOnline {
			updates["last_online"] = lastOnline
		}

		if err := tx.Model(&client).Updates(updates).Error; err != nil {
			warnings = append(warnings, fmt.Sprintf("Failed to update traffic for client %s: %v", email, err))
			continue
		}

		count++
	}

	return count, warnings, nil
}

func (s *MigrationService) migrateInboundClientIps(sqliteDB *sql.DB, tx *gorm.DB) (int, error) {
	rows, err := sqliteDB.Query("SELECT id, client_email, ips FROM inbound_client_ips")
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id int
		var clientEmail, ips sql.NullString

		if err := rows.Scan(&id, &clientEmail, &ips); err != nil {
			continue
		}

		// Check if already exists
		var existing model.InboundClientIps
		if err := tx.Where("client_email = ?", clientEmail.String).First(&existing).Error; err == nil {
			// Update existing
			if err := tx.Model(&existing).Update("ips", ips.String).Error; err != nil {
				logger.Warningf("Failed to update inbound_client_ips for %s: %v", clientEmail.String, err)
				continue
			}
		} else {
			// Create new
			inboundClientIps := model.InboundClientIps{
				ClientEmail: clientEmail.String,
				Ips:         ips.String,
			}
			if err := tx.Create(&inboundClientIps).Error; err != nil {
				logger.Warningf("Failed to migrate inbound_client_ips for %s: %v", clientEmail.String, err)
				continue
			}
		}

		count++
	}

	return count, nil
}

func (s *MigrationService) migrateOutboundTraffics(sqliteDB *sql.DB, tx *gorm.DB) (int, error) {
	rows, err := sqliteDB.Query("SELECT id, tag, up, down, total FROM outbound_traffics")
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id int
		var tag sql.NullString
		var up, down, total sql.NullInt64

		if err := rows.Scan(&id, &tag, &up, &down, &total); err != nil {
			continue
		}

		// Check if already exists
		var existing model.OutboundTraffics
		if err := tx.Where("tag = ?", tag.String).First(&existing).Error; err == nil {
			// Update existing - merge traffic
			updates := map[string]interface{}{
				"up":    existing.Up + up.Int64,
				"down":  existing.Down + down.Int64,
				"total": existing.Total + total.Int64,
			}
			if err := tx.Model(&existing).Updates(updates).Error; err != nil {
				logger.Warningf("Failed to update outbound_traffics for tag %s: %v", tag.String, err)
				continue
			}
		} else {
			// Create new
			outboundTraffic := model.OutboundTraffics{
				Tag:   tag.String,
				Up:    up.Int64,
				Down:  down.Int64,
				Total: total.Int64,
			}
			if err := tx.Create(&outboundTraffic).Error; err != nil {
				logger.Warningf("Failed to migrate outbound_traffics for tag %s: %v", tag.String, err)
				continue
			}
		}

		count++
	}

	return count, nil
}

func (s *MigrationService) migrateHistoryOfSeeders(sqliteDB *sql.DB, tx *gorm.DB) (int, error) {
	rows, err := sqliteDB.Query("SELECT id, seeder_name FROM history_of_seeders")
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id int
		var seederName sql.NullString

		if err := rows.Scan(&id, &seederName); err != nil {
			continue
		}

		// Check if already exists
		var existing model.HistoryOfSeeders
		if err := tx.Where("seeder_name = ?", seederName.String).First(&existing).Error; err == nil {
			// Already exists, skip
			continue
		}

		// Create new
		historyOfSeeders := model.HistoryOfSeeders{
			SeederName: seederName.String,
		}
		if err := tx.Create(&historyOfSeeders).Error; err != nil {
			logger.Warningf("Failed to migrate history_of_seeders for %s: %v", seederName.String, err)
			continue
		}

		count++
	}

	return count, nil
}

func (s *MigrationService) rebuildInboundsSettings(tx *gorm.DB) error {
	var inbounds []model.Inbound
	if err := tx.Find(&inbounds).Error; err != nil {
		return err
	}

	// Import service needed for rebuilding settings
	inboundService := &InboundService{}

	for _, inbound := range inbounds {
		// Get all clients for this inbound from client_entities using transaction
		var mappings []model.ClientInboundMapping
		if err := tx.Where("inbound_id = ?", inbound.Id).Find(&mappings).Error; err != nil {
			logger.Warningf("Failed to get mappings for inbound %d: %v", inbound.Id, err)
			continue
		}

		if len(mappings) == 0 {
			// No clients, just remove clients array from settings
			var settings map[string]interface{}
			if inbound.Settings != "" {
				if err := json.Unmarshal([]byte(inbound.Settings), &settings); err == nil {
					delete(settings, "clients")
					if settingsJSON, err := json.Marshal(settings); err == nil {
						if err := tx.Model(&inbound).Update("settings", string(settingsJSON)).Error; err != nil {
							logger.Warningf("Failed to update settings for inbound %d: %v", inbound.Id, err)
						}
					}
				}
			}
			continue
		}

		clientIds := make([]int, len(mappings))
		for i, mapping := range mappings {
			clientIds[i] = mapping.ClientId
		}

		var clientEntities []*model.ClientEntity
		if err := tx.Where("id IN ?", clientIds).Find(&clientEntities).Error; err != nil {
			logger.Warningf("Failed to get clients for inbound %d: %v", inbound.Id, err)
			continue
		}

		// Build new settings from client entities
		newSettings, err := inboundService.BuildSettingsFromClientEntities(&inbound, clientEntities)
		if err != nil {
			logger.Warningf("Failed to build settings for inbound %d: %v", inbound.Id, err)
			continue
		}

		// Update inbound settings
		if err := tx.Model(&inbound).Update("settings", newSettings).Error; err != nil {
			logger.Warningf("Failed to update settings for inbound %d: %v", inbound.Id, err)
			continue
		}
	}

	return nil
}

// readPanelSettings reads panel settings from SQLite database.
func (s *MigrationService) readPanelSettings(sqliteDB *sql.DB) (*PanelSettingsPreview, error) {
	rows, err := sqliteDB.Query("SELECT key, value FROM settings WHERE key IN ('webPort', 'webBasePath', 'webListen', 'webDomain', 'webCertFile', 'webKeyFile')")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	panelSettings := &PanelSettingsPreview{}
	settingsMap := make(map[string]string)

	for rows.Next() {
		var key, value sql.NullString
		if err := rows.Scan(&key, &value); err != nil {
			continue
		}
		if key.Valid && value.Valid {
			settingsMap[key.String] = value.String
		}
	}

	// Only include non-empty values
	if settingsMap["webPort"] != "" {
		panelSettings.WebPort = settingsMap["webPort"]
	}
	if settingsMap["webBasePath"] != "" {
		panelSettings.WebBasePath = settingsMap["webBasePath"]
	}
	if settingsMap["webListen"] != "" {
		panelSettings.WebListen = settingsMap["webListen"]
	}
	if settingsMap["webDomain"] != "" {
		panelSettings.WebDomain = settingsMap["webDomain"]
	}
	if settingsMap["webCertFile"] != "" {
		panelSettings.WebCertFile = settingsMap["webCertFile"]
	}
	if settingsMap["webKeyFile"] != "" {
		panelSettings.WebKeyFile = settingsMap["webKeyFile"]
	}

	// Return nil if no settings found
	if panelSettings.WebPort == "" && panelSettings.WebBasePath == "" && panelSettings.WebListen == "" &&
		panelSettings.WebDomain == "" && panelSettings.WebCertFile == "" && panelSettings.WebKeyFile == "" {
		return nil, nil
	}

	// Mark as ignored - these settings will NOT be migrated
	panelSettings.Ignored = true

	return panelSettings, nil
}

// applyPanelSettings is DEPRECATED and no longer used.
// Panel settings (webPort, webBasePath, webCertFile, webKeyFile, webListen, webDomain)
// are intentionally NOT migrated to preserve current panel configuration.
// This function is kept for potential future use but is not called anywhere.
//
//nolint:unused
func (s *MigrationService) applyPanelSettings(panelSettings *PanelSettingsPreview) error {
	if panelSettings == nil {
		return nil
	}

	db := database.GetDB()
	if db == nil {
		return common.NewError("PostgreSQL database connection is not available")
	}

	settingsToUpdate := map[string]string{}
	if panelSettings.WebPort != "" {
		settingsToUpdate["webPort"] = panelSettings.WebPort
	}
	if panelSettings.WebBasePath != "" {
		settingsToUpdate["webBasePath"] = panelSettings.WebBasePath
	}
	if panelSettings.WebListen != "" {
		settingsToUpdate["webListen"] = panelSettings.WebListen
	}
	if panelSettings.WebDomain != "" {
		settingsToUpdate["webDomain"] = panelSettings.WebDomain
	}
	if panelSettings.WebCertFile != "" {
		settingsToUpdate["webCertFile"] = panelSettings.WebCertFile
	}
	if panelSettings.WebKeyFile != "" {
		settingsToUpdate["webKeyFile"] = panelSettings.WebKeyFile
	}

	for key, value := range settingsToUpdate {
		var setting model.Setting
		if err := db.Where("key = ?", key).First(&setting).Error; err == nil {
			// Update existing setting
			if err := db.Model(&setting).Update("value", value).Error; err != nil {
				logger.Warningf("Failed to update setting %s: %v", key, err)
			}
		} else {
			// Create new setting
			setting := model.Setting{
				Key:   key,
				Value: value,
			}
			if err := db.Create(&setting).Error; err != nil {
				logger.Warningf("Failed to create setting %s: %v", key, err)
			}
		}
	}

	return nil
}

// Helper functions for extracting values from map

func (s *MigrationService) getStringFromMap(m map[string]interface{}, key string) string {
	if val, ok := m[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func (s *MigrationService) getInt64FromMap(m map[string]interface{}, key string) int64 {
	if val, ok := m[key]; ok {
		switch v := val.(type) {
		case int64:
			return v
		case int:
			return int64(v)
		case float64:
			return int64(v)
		case string:
			// Try to parse as int64
			var result int64
			if _, err := fmt.Sscanf(v, "%d", &result); err == nil {
				return result
			}
		}
	}
	return 0
}

func (s *MigrationService) getBoolFromMap(m map[string]interface{}, key string, defaultValue bool) bool {
	if val, ok := m[key]; ok {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return defaultValue
}

