// Package database provides database initialization, migration, and management utilities
// for the 3x-ui panel using GORM with PostgreSQL.
package database

import (
	"fmt"
	"log"
	"slices"
	"time"

	"github.com/mhsanaei/3x-ui/v2/config"
	"github.com/mhsanaei/3x-ui/v2/database/model"
	"github.com/mhsanaei/3x-ui/v2/util/crypto"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var db *gorm.DB

const (
	defaultUsername = "admin"
	defaultPassword = "admin"
	// minRequiredSchemaVersion is the minimum schema version required by this application version
	// Update this when you add new migrations that are required for the app to function
	minRequiredSchemaVersion = 1
)

// initUser creates a default admin user if the users table is empty.
func initUser() error {
	empty, err := isTableEmpty("users")
	if err != nil {
		log.Printf("Error checking if users table is empty: %v", err)
		return err
	}
	if empty {
		hashedPassword, err := crypto.HashPasswordAsBcrypt(defaultPassword)

		if err != nil {
			log.Printf("Error hashing default password: %v", err)
			return err
		}

		user := &model.User{
			Username: defaultUsername,
			Password: hashedPassword,
		}
		return db.Create(user).Error
	}
	return nil
}

// runSeeders migrates user passwords to bcrypt and records seeder execution to prevent re-running.
func runSeeders(isUsersEmpty bool) error {
	empty, err := isTableEmpty("history_of_seeders")
	if err != nil {
		log.Printf("Error checking if users table is empty: %v", err)
		return err
	}

	if empty && isUsersEmpty {
		hashSeeder := &model.HistoryOfSeeders{
			SeederName: "UserPasswordHash",
		}
		return db.Create(hashSeeder).Error
	} else {
		var seedersHistory []string
		db.Model(&model.HistoryOfSeeders{}).Pluck("seeder_name", &seedersHistory)

		if !slices.Contains(seedersHistory, "UserPasswordHash") && !isUsersEmpty {
			var users []model.User
			db.Find(&users)

			for _, user := range users {
				hashedPassword, err := crypto.HashPasswordAsBcrypt(user.Password)
				if err != nil {
					log.Printf("Error hashing password for user '%s': %v", user.Username, err)
					return err
				}
				db.Model(&user).Update("password", hashedPassword)
			}

			hashSeeder := &model.HistoryOfSeeders{
				SeederName: "UserPasswordHash",
			}
			return db.Create(hashSeeder).Error
		}
	}

	return nil
}

// isTableEmpty returns true if the named table contains zero rows.
func isTableEmpty(tableName string) (bool, error) {
	var count int64
	err := db.Table(tableName).Count(&count).Error
	return count == 0, err
}

// InitDB sets up the database connection, migrates models, and runs seeders.
// dbConnectionString should be a PostgreSQL connection string in the format:
// postgres://user:password@host:port/dbname?sslmode=mode
//
// InitDB performs the following steps in order:
// 1. Establishes database connection
// 2. Configures connection pool
// 3. Runs schema migrations
// 4. Checks schema version compatibility
// 5. Initializes default user (if needed)
// 6. Runs seeders
func InitDB(dbConnectionString string) error {
	// Step 1: Establish database connection
	var gormLogger logger.Interface
	if config.IsDebug() {
		gormLogger = logger.Default
	} else {
		gormLogger = logger.Discard
	}

	c := &gorm.Config{
		Logger: gormLogger,
	}

	var err error
	db, err = gorm.Open(postgres.Open(dbConnectionString), c)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	// Step 2: Configure connection pool
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	// Set connection pool settings
	// These values can be overridden via environment variables if needed
	sqlDB.SetMaxOpenConns(25)                    // Maximum number of open connections
	sqlDB.SetMaxIdleConns(5)                     // Maximum number of idle connections
	sqlDB.SetConnMaxLifetime(5 * time.Minute)    // Maximum connection lifetime
	sqlDB.SetConnMaxIdleTime(10 * time.Minute)   // Maximum idle time before closing

	// Step 3: Run schema migrations
	migrator := NewMigrator(db)
	if err := migrator.Migrate(); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	// Step 3.5: Clean up invalid group_id references (safety check)
	// This ensures data integrity even if migrations didn't run or were applied before cleanup was added
	// This is idempotent and safe to run multiple times
	if err := db.Exec(`
		UPDATE client_entities
		SET group_id = NULL
		WHERE group_id IS NOT NULL
		  AND group_id NOT IN (SELECT id FROM client_groups)
	`).Error; err != nil {
		// Log warning but don't fail - this is a data cleanup, not critical
		log.Printf("Warning: failed to cleanup invalid group_id references: %v", err)
	}

	// Step 4: Check schema version compatibility
	if err := migrator.CheckSchemaVersion(minRequiredSchemaVersion); err != nil {
		return fmt.Errorf("schema version check failed: %w", err)
	}

	// Step 5: Initialize default user (if needed)
	isUsersEmpty, err := isTableEmpty("users")
	if err != nil {
		return fmt.Errorf("failed to check if users table is empty: %w", err)
	}

	if err := initUser(); err != nil {
		return fmt.Errorf("failed to initialize default user: %w", err)
	}

	// Step 6: Run seeders
	if err := runSeeders(isUsersEmpty); err != nil {
		return fmt.Errorf("failed to run seeders: %w", err)
	}

	return nil
}

// CloseDB closes the database connection if it exists.
func CloseDB() error {
	if db != nil {
		sqlDB, err := db.DB()
		if err != nil {
			return err
		}
		return sqlDB.Close()
	}
	return nil
}

// GetDB returns the global GORM database instance.
func GetDB() *gorm.DB {
	return db
}

// IsNotFound checks if the given error is a GORM record not found error.
func IsNotFound(err error) bool {
	return err == gorm.ErrRecordNotFound
}
