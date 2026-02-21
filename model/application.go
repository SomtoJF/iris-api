package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type JobApplicationStatus string

const (
	JobApplicationStatusPending JobApplicationStatus = "processing"
	JobApplicationStatusApplied JobApplicationStatus = "applied"
	JobApplicationStatusFailed  JobApplicationStatus = "failed"
)

type JobApplication struct {
	IdJobApplication uint                 `gorm:"primaryKey;autoIncrement;column:id_job_application" json:"_"`
	IdExternal       uuid.UUID            `gorm:"type:text;not null;unique" json:"id"`
	UserId           uint                 `gorm:"column:id_user;not null"`
	User             User                 `gorm:"foreignKey:UserId;references:IdUser"`
	Status           JobApplicationStatus `gorm:"type:varchar(50);not null"`
	JobTitle         string               `gorm:"type:varchar(255);not null"`
	CompanyName      string               `gorm:"type:varchar(255);not null"`
	JobDescription   string               `gorm:"type:text;not null"`
	Url              string               `gorm:"not null;unique"`
	CreatedAt        time.Time            `gorm:"default:CURRENT_TIMESTAMP"`
	UpdatedAt        time.Time            `gorm:"default:CURRENT_TIMESTAMP;autoUpdateTime"`
	DeletedAt        *time.Time           `gorm:"index;default:NULL"`
}

func (JobApplication) TableName() string {
	return "job_application"
}

// BeforeCreate hook to auto-generate UUID
func (j *JobApplication) BeforeCreate(tx *gorm.DB) error {
	if j.IdExternal == uuid.Nil {
		j.IdExternal = uuid.New()
	}
	return nil
}
