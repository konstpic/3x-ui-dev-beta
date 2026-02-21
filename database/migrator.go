// Package database provides migration management utilities for versioned schema migrations.
package database

import (
	"embed"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// SchemaMigration represents a migration record in the database.
type SchemaMigration struct {
	Version   int64  `gorm:"primaryKey"`
	Name      string `gorm:"not null"`
	AppliedAt int64  `gorm:"not null"`
}

// TableName specifies the table name for SchemaMigration.
func (SchemaMigration) TableName() string {
	return "schema_migrations"
}

// MigrationFile represents a migration file with its version and name.
type MigrationFile struct {
	Version int64
	Name    string
	Content string
}

// Migrator handles database schema migrations.
type Migrator struct {
	db *gorm.DB
}

// NewMigrator creates a new Migrator instance.
func NewMigrator(db *gorm.DB) *Migrator {
	return &Migrator{db: db}
}

// EnsureMigrationsTable creates the schema_migrations table if it doesn't exist.
func (m *Migrator) EnsureMigrationsTable() error {
	return m.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version BIGINT PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			applied_at BIGINT NOT NULL
		)
	`).Error
}

// GetAppliedMigrations returns a map of applied migration versions.
func (m *Migrator) GetAppliedMigrations() (map[int64]bool, error) {
	var migrations []SchemaMigration
	if err := m.db.Find(&migrations).Error; err != nil {
		return nil, err
	}

	applied := make(map[int64]bool)
	for _, m := range migrations {
		applied[m.Version] = true
	}
	return applied, nil
}

// LoadMigrations loads all migration files from the embedded filesystem.
func (m *Migrator) LoadMigrations() ([]MigrationFile, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("failed to read migrations directory: %w", err)
	}

	var migrations []MigrationFile

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		if !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		// Parse version from filename (format: 0001_name.sql)
		baseName := strings.TrimSuffix(entry.Name(), ".sql")
		parts := strings.SplitN(baseName, "_", 2)
		if len(parts) < 2 {
			log.Printf("Warning: skipping migration file with invalid name format: %s", entry.Name())
			continue
		}

		version, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			log.Printf("Warning: skipping migration file with invalid version: %s", entry.Name())
			continue
		}

		content, err := migrationsFS.ReadFile(filepath.Join("migrations", entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("failed to read migration file %s: %w", entry.Name(), err)
		}

		migrations = append(migrations, MigrationFile{
			Version: version,
			Name:    entry.Name(),
			Content: string(content),
		})
	}

	// Sort migrations by version
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	return migrations, nil
}

// ApplyMigration applies a single migration within a transaction.
// Migrations are idempotent (use IF EXISTS, IF NOT EXISTS), so they can be safely applied multiple times.
func (m *Migrator) ApplyMigration(migration MigrationFile) error {
	return m.db.Transaction(func(tx *gorm.DB) error {
		// Execute migration SQL (idempotent - safe to run multiple times)
		if err := tx.Exec(migration.Content).Error; err != nil {
			return fmt.Errorf("failed to execute migration %s: %w", migration.Name, err)
		}

		// Record migration as applied (check if already exists to allow re-running)
		// This allows migrations to be re-run safely
		var existing SchemaMigration
		err := tx.Where("version = ?", migration.Version).First(&existing).Error
		if err == gorm.ErrRecordNotFound {
			// Migration not recorded yet, insert it
			record := SchemaMigration{
				Version:   migration.Version,
				Name:      migration.Name,
				AppliedAt: getCurrentTimestamp(),
			}
			if err := tx.Create(&record).Error; err != nil {
				return fmt.Errorf("failed to record migration %s: %w", migration.Name, err)
			}
		} else if err != nil {
			return fmt.Errorf("failed to check migration %s: %w", migration.Name, err)
		}
		// If migration already exists, just continue (it's already recorded)

		log.Printf("Applied migration: %s (version %d)", migration.Name, migration.Version)
		return nil
	})
}

// Migrate applies all pending migrations.
func (m *Migrator) Migrate() error {
	// Ensure migrations table exists
	if err := m.EnsureMigrationsTable(); err != nil {
		return fmt.Errorf("failed to ensure migrations table: %w", err)
	}

	// Get applied migrations
	applied, err := m.GetAppliedMigrations()
	if err != nil {
		return fmt.Errorf("failed to get applied migrations: %w", err)
	}

	// Load all migrations
	migrations, err := m.LoadMigrations()
	if err != nil {
		return fmt.Errorf("failed to load migrations: %w", err)
	}

	// Apply all migrations in order (they are idempotent, so safe to re-run)
	// This ensures all migrations are always applied, even if they were partially applied before
	appliedCount := 0
	for _, migration := range migrations {
		// Always apply migration (it's idempotent)
		// The migration itself will handle IF EXISTS/IF NOT EXISTS checks
		if err := m.ApplyMigration(migration); err != nil {
			return fmt.Errorf("failed to apply migration %s: %w", migration.Name, err)
		}

		// Check if this was a new migration or a re-application
		if !applied[migration.Version] {
			appliedCount++
		} else {
			log.Printf("Re-applied migration: %s (version %d) - ensuring consistency", migration.Name, migration.Version)
		}
	}

	if appliedCount > 0 {
		log.Printf("Applied %d migration(s)", appliedCount)
	} else {
		log.Printf("Database is up to date, no migrations to apply")
	}

	return nil
}

// GetCurrentVersion returns the highest applied migration version.
func (m *Migrator) GetCurrentVersion() (int64, error) {
	var migration SchemaMigration
	if err := m.db.Order("version DESC").First(&migration).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return 0, nil // No migrations applied yet
		}
		return 0, err
	}
	return migration.Version, nil
}

// GetLatestVersion returns the highest available migration version.
func (m *Migrator) GetLatestVersion() (int64, error) {
	migrations, err := m.LoadMigrations()
	if err != nil {
		return 0, err
	}

	if len(migrations) == 0 {
		return 0, nil
	}

	// Migrations are sorted, so the last one is the latest
	return migrations[len(migrations)-1].Version, nil
}

// CheckSchemaVersion checks if the database schema version is compatible with the application.
func (m *Migrator) CheckSchemaVersion(minRequiredVersion int64) error {
	currentVersion, err := m.GetCurrentVersion()
	if err != nil {
		return fmt.Errorf("failed to get current schema version: %w", err)
	}

	if currentVersion < minRequiredVersion {
		return fmt.Errorf(
			"database schema version (%d) is older than required version (%d). Please run migrations",
			currentVersion,
			minRequiredVersion,
		)
	}

	latestVersion, err := m.GetLatestVersion()
	if err != nil {
		return fmt.Errorf("failed to get latest migration version: %w", err)
	}

	if currentVersion > latestVersion {
		return fmt.Errorf(
			"database schema version (%d) is newer than application version (%d). Please update the application",
			currentVersion,
			latestVersion,
		)
	}

	return nil
}

// getCurrentTimestamp returns the current Unix timestamp in seconds.
func getCurrentTimestamp() int64 {
	return time.Now().Unix()
}
