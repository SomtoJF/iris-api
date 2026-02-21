package resume

import (
	"net/http"
	"time"

	"github.com/SomtoJF/iris-api/model"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Endpoint struct {
	db *gorm.DB
}

func NewEndpoint(db *gorm.DB) *Endpoint {
	return &Endpoint{db: db}
}

type ResumeDTO struct {
	Id        string    `json:"id"`
	FileName  string    `json:"fileName"`
	FileSize  int64     `json:"fileSize"`
	Url       string    `json:"url"`
	IsActive  bool      `json:"isActive"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (e *Endpoint) FetchResumes(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var resumes []model.Resume
	if err := e.db.Where("deleted_at IS NULL AND user_id = ?", userId).Order("created_at DESC").Find(&resumes).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch resumes"})
		return
	}

	resumeDTOs := make([]ResumeDTO, 0, len(resumes))
	for _, resume := range resumes {
		resumeDTOs = append(resumeDTOs, ResumeDTO{
			Id:        resume.IdExternal.String(),
			FileName:  resume.FileName,
			FileSize:  resume.FileSize,
			Url:       resume.Url,
			IsActive:  resume.IsActive,
			CreatedAt: resume.CreatedAt,
			UpdatedAt: resume.UpdatedAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{"data": resumeDTOs})
}

func (e *Endpoint) SetResumeAsActive(c *gin.Context) {
	id := c.Param("id")
	userId := c.GetUint("userId")
	if userId == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Start transaction
	tx := e.db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// Deactivate all resumes for the user
	if err := tx.Model(&model.Resume{}).Where("deleted_at IS NULL AND user_id = ?", userId).Update("is_active", false).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to deactivate resumes"})
		return
	}

	// Find resume by IdExternal
	var resume model.Resume
	if err := tx.Where("id_external = ? AND deleted_at IS NULL AND user_id = ?", id, userId).First(&resume).Error; err != nil {
		tx.Rollback()
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Resume not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to find resume"})
		return
	}

	// Set resume as active
	if err := tx.Model(&resume).Update("is_active", true).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to activate resume"})
		return
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Resume set as active"})
}
