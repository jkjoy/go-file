package model

import (
	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/mysql"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
	"fmt"
	"go-file/common"
	"os"
	"strings"
)

var DB *gorm.DB

func createAdminAccount() {
	var user User
	DB.Where(User{Role: common.RoleAdminUser}).Attrs(User{
		Username:    "admin",
		Password:    "123456",
		Role:        common.RoleAdminUser,
		Status:      common.UserStatusEnabled,
		DisplayName: "Administrator",
	}).FirstOrCreate(&user)
}

func CountTable(tableName string) (num int) {
	DB.Table(tableName).Count(&num)
	return
}

// enableSQLiteWAL lets readers keep working while a write is in progress.
// The mode is stored in the database file, so it only needs to be set once.
func enableSQLiteWAL(db *gorm.DB) {
	var mode struct{ JournalMode string }
	if err := db.Raw("PRAGMA journal_mode = WAL").Scan(&mode).Error; err != nil || !strings.EqualFold(mode.JournalMode, "wal") {
		common.SysError(fmt.Sprintf("failed to enable SQLite WAL mode (current mode: %q): %v", mode.JournalMode, err))
		return
	}
	// NORMAL is safe in WAL mode and avoids an fsync on every commit.
	db.Exec("PRAGMA synchronous = NORMAL")
}

func InitDB() (db *gorm.DB, err error) {
	if os.Getenv("SQL_DSN") != "" {
		// Use MySQL
		db, err = gorm.Open("mysql", os.Getenv("SQL_DSN"))
	} else {
		// Use SQLite
		db, err = gorm.Open("sqlite3", common.SQLitePath)
	}
	if err == nil {
		DB = db
		if os.Getenv("SQL_DSN") == "" {
			enableSQLiteWAL(db)
		}
		db.AutoMigrate(&File{})
		db.AutoMigrate(&Image{})
		db.AutoMigrate(&User{})
		db.AutoMigrate(&Option{})
		db.AutoMigrate(&StatHour{}, &StatIP{}, &StatURL{})
		createAdminAccount()
		return DB, err
	} else {
		common.FatalLog("failed to connect to database: " + err.Error())
	}
	return nil, err
}
