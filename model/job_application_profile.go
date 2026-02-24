package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type JobApplicationProfile struct {
	IdJobApplicationProfile uint       `gorm:"primaryKey;autoIncrement;column:id_job_application_profile" json:"_"`
	IdExternal              uuid.UUID  `gorm:"type:text;not null;unique" json:"id"`
	UserId                  uint       `gorm:"column:id_user;not null;uniqueIndex"`
	FirstName               string     `json:"first_name"`
	LastName                string     `json:"last_name"`
	Email                   string     `json:"email"`
	Phone                   string     `json:"phone"`
	Address                 string     `json:"address"`
	City                    string     `json:"city"`
	State                   string     `json:"state"`
	Zip                     string     `json:"zip"`
	CountryOfResidence      string     `json:"country_of_residence"`
	IsVeteran               bool       `json:"is_veteran"`
	CountriesOfCitizenship  []string   `json:"countries_of_citizenship" gorm:"type:text;serializer:json"`
	Gender                  string     `json:"gender"`
	DateOfBirth             time.Time  `json:"date_of_birth"`
	CreatedAt               time.Time  `gorm:"default:CURRENT_TIMESTAMP"`
	UpdatedAt               time.Time  `gorm:"default:CURRENT_TIMESTAMP;autoUpdateTime"`
	DeletedAt               *time.Time `gorm:"index;default:NULL"`
}

func (JobApplicationProfile) TableName() string {
	return "job_application_profile"
}

func (u *JobApplicationProfile) BeforeCreate(tx *gorm.DB) error {
	if u.IdExternal == uuid.Nil {
		u.IdExternal = uuid.New()
	}
	return nil
}
