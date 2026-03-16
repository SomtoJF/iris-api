package model

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// StringSlice is a []string that safely scans NULL or invalid JSON as an empty slice.
type StringSlice []string

// Scan implements sql.Scanner. Handles nil, "NULL", and invalid JSON as empty slice.
func (s *StringSlice) Scan(value interface{}) error {
	if value == nil {
		*s = []string{}
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		*s = []string{}
		return nil
	}
	if len(b) == 0 {
		*s = []string{}
		return nil
	}
	// SQL NULL often returned as string "NULL" or "null"
	if len(b) == 4 && (string(b) == "NULL" || string(b) == "null") {
		*s = []string{}
		return nil
	}
	if err := json.Unmarshal(b, (*[]string)(s)); err != nil {
		*s = []string{}
		return nil
	}
	return nil
}

// Value implements driver.Valuer for saving to DB.
func (s StringSlice) Value() (driver.Value, error) {
	if s == nil || len(s) == 0 {
		return "[]", nil
	}
	return json.Marshal(s)
}

type JobApplicationProfile struct {
	IdJobApplicationProfile uint       `gorm:"primaryKey;autoIncrement;column:id_job_application_profile" json:"_"`
	IdExternal              uuid.UUID  `gorm:"type:text;not null;unique" json:"id"`
	UserId                  uint       `gorm:"column:id_user;not null;uniqueIndex"`
	FirstName               string     `json:"first_name" gorm:"not null"`
	LastName                string     `json:"last_name" gorm:"not null"`
	Email                   string     `json:"email" gorm:"not null"`
	Phone                   string     `json:"phone"`
	Address                 string     `json:"address"`
	City                    string     `json:"city"`
	State                   string     `json:"state"`
	Zip                     string     `json:"zip"`
	CountryOfResidence      string     `json:"country_of_residence"`
	IsVeteran               bool       `json:"is_veteran"`
	CountriesOfCitizenship  StringSlice `json:"countries_of_citizenship" gorm:"type:text"`
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
