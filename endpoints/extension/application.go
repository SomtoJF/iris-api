package extension

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/SomtoJF/iris-api/model"
	"github.com/SomtoJF/iris-api/temporal"
	"github.com/SomtoJF/iris-api/utils"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
)

type Endpoint struct {
	db             *gorm.DB
	temporalClient client.Client
	logger         *slog.Logger
	taskQueueName  temporal.TaskQueueName
}

type ApplyForJobRequest struct {
	Url string `json:"url" binding:"required"`
	// ResumeId is the external UUID of the resume to apply with. When omitted,
	// the user's active resume is used.
	ResumeId string `json:"resumeId"`
}

type InitiateApplicationWorkflowInput struct {
	Url      string `json:"url" binding:"required"`
	ResumeId string `json:"resumeId"`
}

type InitiateApplicationWorkflowResponse struct {
	JobTitle       string `json:"jobTitle"`
	CompanyName    string `json:"companyName"`
	JobDescription string `json:"jobDescription"`
}

func NewEndpoint(db *gorm.DB, temporalClient client.Client, logger *slog.Logger, taskQueueName temporal.TaskQueueName) *Endpoint {
	return &Endpoint{db: db, temporalClient: temporalClient, logger: logger, taskQueueName: taskQueueName}
}

func (e *Endpoint) InitiateApplication(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(c.Request.Context(), "unauthorized", "handler", "ApplyForJob")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var request ApplyForJobRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		e.logger.WarnContext(c.Request.Context(), "failed to bind JSON", "handler", "ApplyForJob", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// initiate application workflow
	workflowID := uuid.New().String()
	workflowInput := InitiateApplicationWorkflowInput{
		Url:      request.Url,
		ResumeId: request.ResumeId,
	}

	run, err := e.temporalClient.ExecuteWorkflow(c.Request.Context(), client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: string(e.taskQueueName),
	}, "InitiateApplicationWorkflow", workflowInput)
	if err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to initiate application workflow", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to initiate application workflow"})
		return
	}

	var workflowResponse InitiateApplicationWorkflowResponse
	if err := run.Get(c.Request.Context(), &workflowResponse); err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to get workflow result", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get workflow result"})
		return
	}

	resume, err := e.resolveResume(userId, &request.ResumeId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "No resume found"})
			return
		}
		e.logger.ErrorContext(c.Request.Context(), "failed to resolve resume", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to resolve resume"})
		return
	}

	if err := e.db.Create(&model.JobApplication{
		Url:            request.Url,
		ResumeId:       resume.IdResume,
		JobTitle:       workflowResponse.JobTitle,
		CompanyName:    workflowResponse.CompanyName,
		JobDescription: workflowResponse.JobDescription,
		Status:         model.JobApplicationStatusPending,
		UserId:         userId,
		WorkflowID:     &workflowID,
	}).Error; err != nil {
		if utils.IsUniqueConstraintViolation(err) {
			e.logger.WarnContext(c.Request.Context(), "job application duplicate key", "error", err)
			c.JSON(http.StatusConflict, gin.H{"error": "Job application already exists"})
			return
		}
		e.logger.ErrorContext(c.Request.Context(), "failed to create job application", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create job application"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Application initiated"})
}

// resolveResume returns the resume identified by externalId (scoped to the user)
// when provided, otherwise the user's active resume.
func (e *Endpoint) resolveResume(userId uint, externalId *string) (model.Resume, error) {
	var resume model.Resume
	if externalId != nil && *externalId != "" {
		err := e.db.Where("id_external = ? AND id_user = ? AND deleted_at IS NULL", *externalId, userId).First(&resume).Error
		return resume, err
	}
	err := e.db.Where("id_user = ? AND is_active = true AND deleted_at IS NULL", userId).First(&resume).Error
	return resume, err
}
