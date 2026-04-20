package model

import (
	"time"

	"github.com/google/uuid"
)

type IssueType string

const (
	IssueTypeBug            IssueType = "bug"
	IssueTypeFeatureRequest IssueType = "feature_request"
)

type Issue struct {
	IdIssue          uint            `gorm:"primaryKey;autoIncrement;column:id_issue" json:"_"`
	IdExternal       uuid.UUID       `gorm:"unique;type:uuid;default:gen_random_uuid()" json:"id"`
	Title            string          `gorm:"not null"`
	Type             IssueType       `gorm:"type:text;not null"`
	UserId           uint            `gorm:"column:id_user;not null"`
	User             User            `gorm:"foreignKey:UserId;references:IdUser"`
	JobApplicationId *uint           `gorm:"column:id_job_application;default:NULL"`
	JobApplication   *JobApplication `gorm:"foreignKey:JobApplicationId;references:IdJobApplication;default:NULL"`
	Description      string          `gorm:"not null"`
	Summary          string          `gorm:"not null"`
	Comments         []IssueComment  `gorm:"foreignKey:IssueId;references:IdIssue"`
	Upvotes          []IssueUpvote   `gorm:"foreignKey:IssueId;references:IdIssue"`
	IsResolved       bool            `gorm:"default:false"`
	CreatedAt        time.Time       `gorm:"default:CURRENT_TIMESTAMP"`
	UpdatedAt        time.Time       `gorm:"default:CURRENT_TIMESTAMP;autoUpdateTime"`
	DeletedAt        *time.Time      `gorm:"index;default:NULL"`
}

func (Issue) TableName() string {
	return "issue"
}
