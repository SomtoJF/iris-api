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
	// id_resume is NOT NULL in the model, but AutoMigrate would try to add it NOT NULL
	// to an already-populated table and fail. Add it nullable first, backfill, then let
	// AutoMigrate enforce NOT NULL once every row is populated.
	if !db.Migrator().HasColumn(&model.JobApplication{}, "id_resume") {
		if err := db.Exec("ALTER TABLE job_application ADD COLUMN id_resume bigint").Error; err != nil {
			log.Fatal(err)
		}
	}

	// Backfill job_application.id_resume from each application's job_application_data
	// row, falling back to the user's active resume. Must run BEFORE the
	// job_application_data column is dropped below and BEFORE AutoMigrate enforces NOT NULL.
	backfillJobApplicationResume(db)

	if err := db.AutoMigrate(&model.JobApplication{}); err != nil {
		log.Fatal(err)
	}
	// AutoMigrate won't add NOT NULL to an existing column; enforce it now that every row is backfilled.
	if err := db.Exec("ALTER TABLE job_application ALTER COLUMN id_resume SET NOT NULL").Error; err != nil {
		log.Fatal(err)
	}
	// Replace full unique index with partial index so soft-deleted rows don't block re-apply
	// db.Exec("DROP INDEX IF EXISTS idx_job_application_url_user")
	// db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_job_application_url_user ON job_application (id_user, url) WHERE deleted_at IS NULL")

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
	// Drop the resume FK now that it lives on job_application (AutoMigrate won't drop columns).
	if db.Migrator().HasColumn(&model.JobApplicationData{}, "id_resume") {
		if err := db.Migrator().DropColumn(&model.JobApplicationData{}, "id_resume"); err != nil {
			log.Fatal(err)
		}
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

	// log.Println("Starting migration on table website cache")
	// if err := db.AutoMigrate(&model.WebsiteCache{}); err != nil {
	// 	log.Fatal(err)
	// }
	log.Println("Migration completed")
}

// backfillJobApplicationResume populates job_application.id_resume for existing rows.
// Source of truth is the application's job_application_data.id_resume; when no data
// row exists it falls back to the user's active resume. Rows with no resolvable
// resume are logged and skipped.
func backfillJobApplicationResume(db *gorm.DB) {
	log.Println("Backfilling job_application.id_resume")

	type jobAppRow struct {
		IdJobApplication uint
		IdUser           uint
	}
	var rows []jobAppRow
	if err := db.Raw(
		"SELECT id_job_application, id_user FROM job_application WHERE id_resume IS NULL OR id_resume = 0",
	).Scan(&rows).Error; err != nil {
		log.Fatalf("backfill: load job applications: %v", err)
	}

	var updated, skipped int
	for _, row := range rows {
		var idResume uint

		// 1. resume attached to the application's data row
		db.Raw(
			"SELECT id_resume FROM job_application_data WHERE id_job_application = ? LIMIT 1",
			row.IdJobApplication,
		).Scan(&idResume)

		// 2. fallback: user's active resume
		if idResume == 0 {
			db.Raw(
				"SELECT id_resume FROM resume WHERE id_user = ? AND is_active = true AND deleted_at IS NULL LIMIT 1",
				row.IdUser,
			).Scan(&idResume)
		}

		if idResume == 0 {
			log.Printf("backfill: no resume for job_application %d (user %d), skipping", row.IdJobApplication, row.IdUser)
			skipped++
			continue
		}

		if err := db.Exec(
			"UPDATE job_application SET id_resume = ? WHERE id_job_application = ?",
			idResume, row.IdJobApplication,
		).Error; err != nil {
			log.Fatalf("backfill: update job_application %d: %v", row.IdJobApplication, err)
		}
		updated++
	}

	log.Printf("Backfill complete: %d updated, %d skipped", updated, skipped)
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
