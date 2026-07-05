package jobapplicationdata

import (
	"log/slog"
	"net/http"

	"github.com/SomtoJF/iris-api/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Endpoint struct {
	db     *gorm.DB
	logger *slog.Logger
}

func NewEndpoint(db *gorm.DB, logger *slog.Logger) *Endpoint {
	return &Endpoint{db: db, logger: logger}
}

type ResumeDTO struct {
	Id       string `json:"id"`
	FileName string `json:"fileName"`
	FileSize int64  `json:"fileSize"`
}

type JobApplicationDataResponse struct {
	Questions   model.JobApplicationQuestionsList `json:"questions"`
	CoverLetter *string                          `json:"cover_letter"`
	Resume      ResumeDTO                        `json:"resume"`
}

func (e *Endpoint) GetJobApplicationData(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(c.Request.Context(), "unauthorized", "handler", "GetJobApplicationData")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	jobAppId, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid job application ID"})
		return
	}

	var jobApp model.JobApplication
	if err := e.db.Where("id_external = ? AND id_user = ?", jobAppId, userId).Preload("Resume").First(&jobApp).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Job application not found"})
			return
		}
		e.logger.ErrorContext(c.Request.Context(), "failed to fetch job application", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch job application"})
		return
	}

	var data model.JobApplicationData
	if err := e.db.Where("id_job_application = ? AND id_user = ?", jobApp.IdJobApplication, userId).
		First(&data).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Application data not found"})
			return
		}
		e.logger.ErrorContext(c.Request.Context(), "failed to fetch application data", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch application data"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": JobApplicationDataResponse{
		Questions:   data.Questions,
		CoverLetter: data.CoverLetter,
		Resume: ResumeDTO{
			Id:       jobApp.Resume.IdExternal.String(),
			FileName: jobApp.Resume.FileName,
			FileSize: jobApp.Resume.FileSize,
		},
	}})
}
