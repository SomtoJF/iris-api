package sqldb

import (
	"os"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func ConnectToSQLite() (*gorm.DB, error) {
	var err error
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	// Create the directory path if it doesn't exist
	dbDir := homeDir + "/iris/db"
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return nil, err
	}

	dbPath := dbDir + "/gorm.db"

	DB, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	return DB, nil
}
