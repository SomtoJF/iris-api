package model

import (
	"database/sql/driver"
	"encoding/json"
	"time"
)

type UserActionType string

const (
	UserActionTypeCaptcha        = "captcha"
	UserActionTypeAuthentication = "authentication"
	UserActionTypeOTP            = "otp"
)

type UserActionLayoutItem struct {
	Type      *string   `json:"type"`       //e.g password, text, number, phone, email etc.
	FieldName string    `json:"field_name"` //e.g Username, OTP, Password, etc.
	Component *string   `json:"component"`  //e.g input, textarea, select, radio, checkbox, etc.
	Options   *[]string `json:"options"`    //e.g ["Option 1", "Option 2", "Option 3"]
}

type UserActionResultItem struct {
	FieldName string `json:"field_name"` //e.g Username, OTP, Password, etc.
	Value     string `json:"value"`      //e.g "John Doe", "123456", "password", etc.
}

type UserActionLayout []UserActionLayoutItem

// Scan implements sql.Scanner for JSON stored as TEXT/BLOB in SQLite.
func (u *UserActionLayout) Scan(value interface{}) error {
	if value == nil {
		*u = UserActionLayout{}
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		*u = UserActionLayout{}
		return nil
	}
	if len(b) == 0 {
		*u = UserActionLayout{}
		return nil
	}
	if len(b) == 4 && (string(b) == "NULL" || string(b) == "null") {
		*u = UserActionLayout{}
		return nil
	}
	if err := json.Unmarshal(b, (*[]UserActionLayoutItem)(u)); err != nil {
		*u = UserActionLayout{}
		return nil
	}
	return nil
}

// Value implements driver.Valuer for persisting layout as JSON.
func (u UserActionLayout) Value() (driver.Value, error) {
	if len(u) == 0 {
		return "[]", nil
	}
	return json.Marshal(u)
}

type UserAction struct {
	IdUserAction     uint             `gorm:"primaryKey;autoIncrement;column:id_user_action" json:"_"`
	IdUser           uint             `gorm:"column:id_user;not null"`
	User             User             `gorm:"foreignKey:IdUser;references:IdUser"`
	IdJobApplication uint             `gorm:"column:id_job_application;not null"`
	JobApplication   JobApplication   `gorm:"foreignKey:IdJobApplication;references:IdJobApplication"`
	UserActionType   UserActionType   `gorm:"type:text;not null"`
	ActionDetails    string           `gorm:"type:text;not null"`
	UserActionLayout UserActionLayout `gorm:"type:jsonb;not null"`
	IsPending        bool             `gorm:"default:true"`
	CreatedAt        time.Time        `gorm:"default:CURRENT_TIMESTAMP"`
	UpdatedAt        time.Time        `gorm:"default:CURRENT_TIMESTAMP;autoUpdateTime"`
}
