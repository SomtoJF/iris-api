package model

import (
	"time"

	"github.com/google/uuid"
)

type JobApplicationQuestions struct {
	Question   string `gorm:"type:text;not null"`
	Answer     string `gorm:"type:text;not null"`
	isOptional bool   `gorm:"default:false"`
}

type JobApplicationData struct {
	IdJobApplicationData uint                      `gorm:"primaryKey;autoIncrement;column:id_job_application_data" json:"_"`
	IdExternal           uuid.UUID                 `gorm:"unique;type:uuid;default:gen_random_uuid()" json:"id"`
	UserId               uint                      `gorm:"column:id_user;not null"`
	User                 User                      `gorm:"foreignKey:UserId;references:IdUser"`
	ResumeId             uint                      `gorm:"column:id_resume;not null"`
	Resume               Resume                    `gorm:"foreignKey:ResumeId;references:IdResume"`
	JobApplicationId     uint                      `gorm:"column:id_job_application;not null;uniqueIndex"`
	JobApplication       JobApplication            `gorm:"foreignKey:JobApplicationId;references:IdJobApplication"`
	CoverLetter          *string                   `gorm:"type:text"`
	Questions            []JobApplicationQuestions `gorm:"type:jsonb;not null"`
	CreatedAt            time.Time                 `gorm:"default:CURRENT_TIMESTAMP"`
	UpdatedAt            time.Time                 `gorm:"default:CURRENT_TIMESTAMP;autoUpdateTime"`
}

func (JobApplicationData) TableName() string {
	return "job_application_data"
}
