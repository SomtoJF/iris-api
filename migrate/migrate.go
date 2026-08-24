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
	log.Println("Migrated table job application")

	// log.Println("Starting migration on table job application profile")
	// if err := db.AutoMigrate(&model.JobApplicationProfile{}); err != nil {
	// 	log.Fatal(err)
	// }

	// log.Println("Starting migration on table resume")
	// if err := db.AutoMigrate(&model.Resume{}); err != nil {
	// 	log.Fatal(err)
	// }
	// log.Println("Migrated table resume")

	// ensureOneActiveResumePerUser()
	// log.Println("Starting migration on table user action")
	// if err := db.AutoMigrate(&model.UserAction{}); err != nil {
	// 	log.Fatal(err)
	// }

	// log.Println("Starting migration on table job application data")
	// if err := db.AutoMigrate(&model.JobApplicationData{}); err != nil {
	// 	log.Fatal(err)
	// }

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

// dropAllApplicationTables removes Iris models' tables so AutoMigrate starts from a clean slate.
func dropAllApplicationTables() {
	log.Println("Dropping all application tables")
	if err := db.Migrator().DropTable(
		&model.User{},
		&model.Resume{},
		&model.UserAction{},
		&model.JobApplication{},
		// &model.JobApplicationProfile{},
		&model.JobApplicationData{},
		&model.CostTracking{},
		&model.Issue{},
		&model.IssueComment{},
		&model.IssueCommentUpvote{},
		&model.IssueUpvote{},
		&model.WebsiteCache{},
	); err != nil {
		log.Fatal(err)
	}
	log.Println("Dropped all application tables")
}

// ensureOneActiveResumePerUser deactivates all live resumes per user, then activates
// the most recently created already-active resume, or the most recently created
// resume if none are active.
func ensureOneActiveResumePerUser() {
	log.Println("Ensuring one active resume per user")

	var userIDs []uint
	if err := db.Model(&model.Resume{}).
		Where("deleted_at IS NULL").
		Distinct("id_user").
		Pluck("id_user", &userIDs).Error; err != nil {
		log.Fatal(err)
	}

	for _, userID := range userIDs {
		err := db.Transaction(func(tx *gorm.DB) error {
			var resumes []model.Resume
			if err := tx.Where("id_user = ? AND deleted_at IS NULL", userID).
				Order("created_at DESC, id_resume DESC").
				Find(&resumes).Error; err != nil {
				return err
			}
			if len(resumes) == 0 {
				return nil
			}

			chosenID := resumes[0].IdResume
			for _, r := range resumes {
				if r.IsActive {
					chosenID = r.IdResume
					break
				}
			}

			if err := tx.Model(&model.Resume{}).
				Where("id_user = ? AND deleted_at IS NULL", userID).
				Update("is_active", false).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.Resume{}).
				Where("id_resume = ?", chosenID).
				Update("is_active", true).Error; err != nil {
				return err
			}

			log.Printf("user %d: set resume %d as active", userID, chosenID)
			return nil
		})
		if err != nil {
			log.Fatal(err)
		}
	}

	log.Println("Finished ensuring one active resume per user")
}
