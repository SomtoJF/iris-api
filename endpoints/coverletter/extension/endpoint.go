package extensioncoverletter

import (
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/SomtoJF/iris-api/endpoints/coverletter"
	"github.com/SomtoJF/iris-api/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Endpoint struct {
	db           *gorm.DB
	logger       *slog.Logger
	coverLetters *coverletter.Endpoint
}

func NewEndpoint(db *gorm.DB, logger *slog.Logger, coverLetters *coverletter.Endpoint) *Endpoint {
	return &Endpoint{
		db:           db,
		logger:       logger,
		coverLetters: coverLetters,
	}
}

type GenerateCoverLetterRequest struct {
	ResumeId string `json:"resumeId"`
}

// POST /extension/application/:id/generate-cover-letter
func (e *Endpoint) GenerateCoverLetter(c *gin.Context) {
	ctx := c.Request.Context()

	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(ctx, "unauthorized", "handler", "GenerateCoverLetter")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var input GenerateCoverLetterRequest
	if err := c.ShouldBindJSON(&input); err != nil && !errors.Is(err, io.EOF) {
		e.logger.WarnContext(ctx, "failed to bind JSON", "handler", "GenerateCoverLetter", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid job application ID"})
		return
	}

	var jobApplication model.JobApplication
	if err := e.db.Where("id_external = ? AND id_user = ? AND deleted_at IS NULL", id, userId).First(&jobApplication).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Job application not found"})
			return
		}
		e.logger.ErrorContext(ctx, "failed to load job application", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load job application"})
		return
	}

	resumeId := jobApplication.ResumeId
	if input.ResumeId != "" {
		resume, err := e.resolveResume(userId, input.ResumeId)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "No resume found"})
				return
			}
			e.logger.ErrorContext(ctx, "failed to resolve resume", "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to resolve resume"})
			return
		}
		if err := e.db.Model(&jobApplication).Update("id_resume", resume.IdResume).Error; err != nil {
			e.logger.ErrorContext(ctx, "failed to update job application resume", "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update resume"})
			return
		}
		resumeId = resume.IdResume
		jobApplication.ResumeId = resume.IdResume
	}

	workflowId := coverletter.NewCoverLetterWorkflowID()
	coverLetter, err := coverletter.UpsertAttachedCoverLetter(
		e.db,
		jobApplication,
		resumeId,
		model.CoverLetterStatusProcessing,
		&workflowId,
		nil,
	)
	if err != nil {
		e.logger.ErrorContext(ctx, "failed to upsert attached cover letter", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate cover letter"})
		return
	}

	e.coverLetters.GenerateCoverLetterAsync(userId, coverLetter, workflowId, nil, false)

	c.JSON(http.StatusAccepted, coverletter.CreateCoverLetterResponse{
		Id:     coverLetter.IdExternal.String(),
		Status: model.CoverLetterStatusProcessing,
	})
}

func (e *Endpoint) resolveResume(userId uint, externalId string) (model.Resume, error) {
	var resume model.Resume
	err := e.db.Where("id_external = ? AND id_user = ? AND deleted_at IS NULL", externalId, userId).First(&resume).Error
	return resume, err
}
