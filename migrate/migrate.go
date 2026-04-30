package main

import (
	"log"

	"github.com/SomtoJF/iris-api/initializers/env"
	"github.com/SomtoJF/iris-api/initializers/sqldb"
	"github.com/SomtoJF/iris-api/model"
	"gorm.io/gorm"
)

var db *gorm.DB

func init() {
	var err error

	err = env.LoadEnvVariables()
	if err != nil {
		log.Fatal(err)
	}

	db, err = sqldb.ConnectToPostgres()
	if err != nil {
		log.Fatal(err)
	}

}

func main() {
	// dropAllApplicationTables()

	// log.Println("Starting migration on table user")
	// if err := db.AutoMigrate(&model.User{}); err != nil {
	// 	log.Fatal(err)
	// }

	log.Println("Starting migration on table job application")
	if err := db.AutoMigrate(&model.JobApplication{}); err != nil {
		log.Fatal(err)
	}
	// Replace full unique index with partial index so soft-deleted rows don't block re-apply
	db.Exec("DROP INDEX IF EXISTS idx_job_application_url_user")
	db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_job_application_url_user ON job_application (id_user, url) WHERE deleted_at IS NULL")

	// log.Println("Starting migration on table resume")
	// if err := db.AutoMigrate(&model.Resume{}); err != nil {
	// 	log.Fatal(err)
	// }

	// log.Println("Starting migration on table user action")
	// if err := db.AutoMigrate(&model.UserAction{}); err != nil {
	// 	log.Fatal(err)
	// }

	// log.Println("Starting migration on table job application profile")
	// if err := db.AutoMigrate(&model.JobApplicationProfile{}); err != nil {
	// 	log.Fatal(err)
	// }

	log.Println("Starting migration on table job application data")
	if err := db.AutoMigrate(&model.JobApplicationData{}); err != nil {
		log.Fatal(err)
	}

	// log.Println("Starting migration on table cost tracking")
	// if err := db.AutoMigrate(&model.CostTracking{}); err != nil {
	// 	log.Fatal(err)
	// }

	// log.Println("Starting migration on table issue")
	// if err := db.AutoMigrate(&model.Issue{}); err != nil {
	// 	log.Fatal(err)
	// }

	// log.Println("Starting migration on table issue comment")
	// if err := db.AutoMigrate(&model.IssueComment{}); err != nil {
	// 	log.Fatal(err)
	// }

	// log.Println("Starting migration on table issue comment upvote")
	// if err := db.AutoMigrate(&model.IssueCommentUpvote{}); err != nil {
	// 	log.Fatal(err)
	// }

	// log.Println("Starting migration on table issue upvote")
	// if err := db.AutoMigrate(&model.IssueUpvote{}); err != nil {
	// 	log.Fatal(err)
	// }
	log.Println("Migration completed")
}

// dropAllApplicationTables removes Iris models' tables so AutoMigrate starts from a clean slate.
// Tables are dropped in FK dependency order (children before user).
func dropAllApplicationTables() {
	log.Println("Dropping existing application tables (if any)")
	tables := []interface{}{
		&model.CostTracking{},
		&model.UserAction{},
		// &model.Resume{},
		&model.JobApplication{},
		// &model.JobApplicationProfile{},
		// &model.User{},
		&model.IssueCommentUpvote{},
		&model.IssueUpvote{},
		&model.IssueComment{},
		&model.Issue{},
		// &model.IssueCommentUpvote{},
		// &model.IssueUpvote{},
	}
	for _, t := range tables {
		if err := db.Migrator().DropTable(t); err != nil {
			log.Fatalf("drop table: %v", err)
		}
	}
}
