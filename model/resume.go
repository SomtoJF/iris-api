package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Resume struct {
	IdResume   uint      `gorm:"primaryKey;autoIncrement;column:id_resume" json:"_"`
	IdExternal uuid.UUID `gorm:"type:text;not null;unique" json:"id"`
	// Either url of filepath
	UserId       uint       `gorm:"column:id_user;not null"`
	User         User       `gorm:"foreignKey:UserId;references:IdUser"`
	Url          string     `gorm:"not null"`
	FileName     string     `gorm:"not null"`
	FileSize     int64      `gorm:"not null"`
	Content      string     `gorm:"not null"`
	Summary      string     `gorm:"not null"`
	IsProcessing bool       `gorm:"default:true"`
	IsActive     bool       `gorm:"default:true"`
	CreatedAt    time.Time  `gorm:"default:CURRENT_TIMESTAMP"`
	UpdatedAt    time.Time  `gorm:"default:CURRENT_TIMESTAMP;autoUpdateTime"`
	DeletedAt    *time.Time `gorm:"index;default:NULL"`
}

func (Resume) TableName() string {
	return "resume"
}

// BeforeCreate hook to auto-generate UUID
func (r *Resume) BeforeCreate(tx *gorm.DB) error {
	if r.IdExternal == uuid.Nil {
		r.IdExternal = uuid.New()
	}
	return nil
}
