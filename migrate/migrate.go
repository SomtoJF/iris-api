package main

import (
	"log"

	"github.com/SomtoJF/iris-api/initializers/sqldb"
	"github.com/SomtoJF/iris-api/model"
	"gorm.io/gorm"
)

var db *gorm.DB

func init() {
	var err error
	db, err = sqldb.ConnectToSQLite()
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	if err := db.AutoMigrate(&model.User{}); err != nil {
		log.Fatal(err)
	}

	// if err := db.AutoMigrate(&model.JobApplication{}); err != nil {
	// 	log.Fatal(err)
	// }

	// if err := db.AutoMigrate(&model.Resume{}); err != nil {
	// 	log.Fatal(err)
	// }

	if err := db.AutoMigrate(&model.JobApplicationProfile{}); err != nil {
		log.Fatal(err)
	}
	log.Println("Migration completed")
}
