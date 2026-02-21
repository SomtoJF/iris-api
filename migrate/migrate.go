package main

import (
	"log"

	"github.com/SomtoJF/iris-api/initializers/sqldb"
	"github.com/SomtoJF/iris-api/model"
)

func init() {
	err := sqldb.ConnectToSQLite()
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	db := sqldb.DB

	if err := db.AutoMigrate(&model.User{}); err != nil {
		log.Fatal(err)
	}

	if err := db.AutoMigrate(&model.JobApplication{}); err != nil {
		log.Fatal(err)
	}

	if err := db.AutoMigrate(&model.Resume{}); err != nil {
		log.Fatal(err)
	}
	log.Println("Migration completed")
}
