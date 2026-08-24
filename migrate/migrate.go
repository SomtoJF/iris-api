package main

import (
	"log"
	"strings"
	"time"

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

	// log.Println("Starting migration on table job application")
	// if err := db.AutoMigrate(&model.JobApplication{}); err != nil {
	// 	log.Fatal(err)
	// }
	// log.Println("Migrated table job application")

	log.Println("Starting migration on table cover letter")
	if err := db.AutoMigrate(&model.CoverLetter{}); err != nil {
		log.Fatal(err)
	}
	log.Println("Migrated table cover letter")

	// migrateCoverLettersFromJobApplications()

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

type legacyCoverLetterApplication struct {
	IdJobApplication uint      `gorm:"column:id_job_application"`
	UserId           uint      `gorm:"column:id_user"`
	ResumeId         uint      `gorm:"column:id_resume"`
	JobTitle         string    `gorm:"column:job_title"`
	CompanyName      string    `gorm:"column:company_name"`
	JobDescription   string    `gorm:"column:job_description"`
	Url              string    `gorm:"column:url"`
	Status           string    `gorm:"column:status"`
	WorkflowID       *string   `gorm:"column:workflow_id"`
	CreatedAt        time.Time `gorm:"column:created_at"`
	UpdatedAt        time.Time `gorm:"column:updated_at"`
}

func jobApplicationHasCoverLetterOnlyColumn() bool {
	var exists bool
	if err := db.Raw(`
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'job_application' AND column_name = 'cover_letter_only'
		)
	`).Scan(&exists).Error; err != nil {
		log.Fatal(err)
	}
	return exists
}

func loadJobApplicationDataCoverLetter(idJobApplication uint) *string {
	var row struct {
		CoverLetter *string `gorm:"column:cover_letter"`
	}
	if err := db.Table("job_application_data").
		Select("cover_letter").
		Where("id_job_application = ?", idJobApplication).
		Scan(&row).Error; err != nil {
		log.Fatal(err)
	}
	if row.CoverLetter != nil && strings.TrimSpace(*row.CoverLetter) == "" {
		return nil
	}
	return row.CoverLetter
}

func coverLetterStatusFromJobApplication(status string, hasBody bool) model.CoverLetterStatus {
	switch model.JobApplicationStatus(status) {
	case model.JobApplicationStatusProcessing:
		return model.CoverLetterStatusProcessing
	case model.JobApplicationStatusApplied:
		return model.CoverLetterStatusReady
	case model.JobApplicationStatusFailed:
		return model.CoverLetterStatusFailed
	default:
		if hasBody {
			return model.CoverLetterStatusReady
		}
		return model.CoverLetterStatusFailed
	}
}

func migrateCoverLettersFromJobApplications() {
	if !jobApplicationHasCoverLetterOnlyColumn() {
		log.Println("cover_letter_only already dropped; skipping cover letter data migration")
		return
	}

	backfillStandaloneCoverLetters()
	backfillAttachedCoverLetters()
	hardDeleteCoverLetterOnlyApplications()

	log.Println("Dropping cover_letter_only column from job_application")
	if err := db.Exec("ALTER TABLE job_application DROP COLUMN IF EXISTS cover_letter_only").Error; err != nil {
		log.Fatal(err)
	}
	log.Println("Dropped cover_letter_only column")
}

func backfillStandaloneCoverLetters() {
	log.Println("Backfilling standalone cover letters from cover_letter_only applications")

	var apps []legacyCoverLetterApplication
	if err := db.Table("job_application").
		Where("cover_letter_only = true AND deleted_at IS NULL").
		Find(&apps).Error; err != nil {
		log.Fatal(err)
	}

	created := 0
	for _, app := range apps {
		var existing int64
		existingQuery := db.Model(&model.CoverLetter{}).Where("id_user = ? AND id_job_application IS NULL", app.UserId)
		if app.WorkflowID != nil && *app.WorkflowID != "" {
			existingQuery = existingQuery.Where("workflow_id = ?", *app.WorkflowID)
		} else {
			existingQuery = existingQuery.Where("job_title = ? AND company_name = ? AND url = ? AND created_at = ?",
				app.JobTitle, app.CompanyName, app.Url, app.CreatedAt)
		}
		if err := existingQuery.Count(&existing).Error; err != nil {
			log.Fatal(err)
		}
		if existing > 0 {
			continue
		}

		body := loadJobApplicationDataCoverLetter(app.IdJobApplication)
		status := coverLetterStatusFromJobApplication(app.Status, body != nil)
		coverLetter := model.CoverLetter{
			UserId:         app.UserId,
			ResumeId:       app.ResumeId,
			JobTitle:       app.JobTitle,
			CompanyName:    app.CompanyName,
			JobDescription: app.JobDescription,
			Url:            app.Url,
			Status:         status,
			Body:           body,
			WorkflowID:     app.WorkflowID,
			CreatedAt:      app.CreatedAt,
			UpdatedAt:      app.UpdatedAt,
		}
		if err := db.Create(&coverLetter).Error; err != nil {
			log.Fatal(err)
		}
		created++
		log.Printf("migrated standalone cover letter %d from job application %d", coverLetter.IdCoverLetter, app.IdJobApplication)
	}

	log.Printf("Backfilled %d standalone cover letters", created)
}

func backfillAttachedCoverLetters() {
	log.Println("Backfilling attached cover letters from real applications")

	var apps []legacyCoverLetterApplication
	if err := db.Table("job_application").
		Where("cover_letter_only = false AND deleted_at IS NULL").
		Find(&apps).Error; err != nil {
		log.Fatal(err)
	}

	created := 0
	for _, app := range apps {
		body := loadJobApplicationDataCoverLetter(app.IdJobApplication)
		if body == nil {
			continue
		}

		var existing int64
		if err := db.Model(&model.CoverLetter{}).
			Where("id_job_application = ?", app.IdJobApplication).
			Count(&existing).Error; err != nil {
			log.Fatal(err)
		}
		if existing > 0 {
			continue
		}

		jobAppID := app.IdJobApplication
		coverLetter := model.CoverLetter{
			UserId:           app.UserId,
			ResumeId:         app.ResumeId,
			JobApplicationId: &jobAppID,
			JobTitle:         app.JobTitle,
			CompanyName:      app.CompanyName,
			JobDescription:   app.JobDescription,
			Url:              app.Url,
			Status:           model.CoverLetterStatusReady,
			Body:             body,
			CreatedAt:        app.CreatedAt,
			UpdatedAt:        app.UpdatedAt,
		}
		if err := db.Create(&coverLetter).Error; err != nil {
			log.Fatal(err)
		}
		created++
		log.Printf("migrated attached cover letter %d from job application %d", coverLetter.IdCoverLetter, app.IdJobApplication)
	}

	log.Printf("Backfilled %d attached cover letters", created)
}

func hardDeleteCoverLetterOnlyApplications() {
	log.Println("Hard-deleting cover_letter_only job applications")

	var ids []uint
	if err := db.Table("job_application").
		Where("cover_letter_only = true").
		Pluck("id_job_application", &ids).Error; err != nil {
		log.Fatal(err)
	}
	if len(ids) == 0 {
		log.Println("No cover_letter_only applications to delete")
		return
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("cost_tracking").Where("id_job_application IN ?", ids).Update("id_job_application", nil).Error; err != nil {
			return err
		}
		if err := tx.Table("issue").Where("id_job_application IN ?", ids).Update("id_job_application", nil).Error; err != nil {
			return err
		}
		if err := tx.Unscoped().Where("id_job_application IN ?", ids).Delete(&model.UserAction{}).Error; err != nil {
			return err
		}
		if err := tx.Unscoped().Where("id_job_application IN ?", ids).Delete(&model.JobApplicationData{}).Error; err != nil {
			return err
		}
		if err := tx.Unscoped().Where("id_job_application IN ?", ids).Delete(&model.JobApplication{}).Error; err != nil {
			return err
		}
		return nil
	}); err != nil {
		log.Fatal(err)
	}

	log.Printf("Hard-deleted %d cover_letter_only job applications", len(ids))
}

// dropAllApplicationTables removes Iris models' tables so AutoMigrate starts from a clean slate.
func dropAllApplicationTables() {
	log.Println("Dropping all application tables")
	if err := db.Migrator().DropTable(
		&model.User{},
		&model.Resume{},
		&model.UserAction{},
		&model.JobApplication{},
		&model.CoverLetter{},
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
