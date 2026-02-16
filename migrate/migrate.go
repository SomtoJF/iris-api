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

	err := db.AutoMigrate(&model.JobApplication{})
	if err != nil {
		log.Fatal(err)
	}

	err = db.AutoMigrate(&model.Resume{})
	if err != nil {
		log.Fatal(err)
	}

	log.Println("Migration completed")
}
