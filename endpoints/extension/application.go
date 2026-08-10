package extension

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

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
	Url              string `json:"url"`
	IdUser           uint   `json:"id_user"`
	IdJobApplication uint   `json:"id_job_application"`
}

type InitiateApplicationWorkflowResponse struct {
	JobTitle       string `json:"jobTitle"`
	CompanyName    string `json:"companyName"`
	JobDescription string `json:"jobDescription"`
}

type InitiateApplicationResponse struct {
	Id          string                     `json:"id"`
	Url         string                     `json:"url"`
	JobTitle    string                     `json:"jobTitle"`
	CompanyName string                     `json:"companyName"`
	Status      model.JobApplicationStatus `json:"status"`
	UpdatedAt   time.Time                  `json:"updatedAt"`
}

func NewEndpoint(db *gorm.DB, temporalClient client.Client, logger *slog.Logger, taskQueueName temporal.TaskQueueName) *Endpoint {
	return &Endpoint{db: db, temporalClient: temporalClient, logger: logger, taskQueueName: taskQueueName}
}

func (e *Endpoint) InitiateApplication(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(c.Request.Context(), "unauthorized", "handler", "InitiateApplication")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var request ApplyForJobRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		e.logger.WarnContext(c.Request.Context(), "failed to bind JSON", "handler", "InitiateApplication", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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

	workflowID := fmt.Sprintf("initiate-application-%s-%s", request.Url, uuid.New().String())
	jobApplication := model.JobApplication{
		Url:            request.Url,
		JobTitle:       "Pending-Job-Title",
		CompanyName:    "Pending-Company-Name",
		JobDescription: "Pending-Job-Description",
		Status:         model.JobApplicationStatusPending,
		UserId:         userId,
		ResumeId:       resume.IdResume,
		WorkflowID:     &workflowID,
	}
	if err := e.db.Create(&jobApplication).Error; err != nil {
		if utils.IsUniqueConstraintViolation(err) {
			e.logger.WarnContext(c.Request.Context(), "job application duplicate key", "error", err)
			c.JSON(http.StatusConflict, gin.H{"error": "Job application already exists"})
			return
		}
		e.logger.ErrorContext(c.Request.Context(), "failed to create job application", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create job application"})
		return
	}

	run, err := e.temporalClient.ExecuteWorkflow(c.Request.Context(), client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: string(e.taskQueueName),
	}, "InitiateApplicationWorkflow", InitiateApplicationWorkflowInput{
		Url:              request.Url,
		IdUser:           userId,
		IdJobApplication: jobApplication.IdJobApplication,
	})
	if err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to initiate application workflow", "error", err)
		e.softDeleteApplication(c, &jobApplication)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to initiate application workflow"})
		return
	}

	var workflowResponse InitiateApplicationWorkflowResponse
	if err := run.Get(c.Request.Context(), &workflowResponse); err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to get workflow result", "error", err)
		e.softDeleteApplication(c, &jobApplication)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get workflow result"})
		return
	}

	if err := e.db.Model(&jobApplication).Updates(map[string]any{
		"job_title":       workflowResponse.JobTitle,
		"company_name":    workflowResponse.CompanyName,
		"job_description": workflowResponse.JobDescription,
	}).Error; err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to update job application after initiate", "error", err)
		e.softDeleteApplication(c, &jobApplication)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update job application"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": InitiateApplicationResponse{
		Id:          jobApplication.IdExternal.String(),
		Url:         jobApplication.Url,
		JobTitle:    workflowResponse.JobTitle,
		CompanyName: workflowResponse.CompanyName,
		Status:      jobApplication.Status,
		UpdatedAt:   time.Now(),
	}})
}

func (e *Endpoint) softDeleteApplication(c *gin.Context, jobApplication *model.JobApplication) {
	now := time.Now()
	if err := e.db.Model(jobApplication).Update("deleted_at", &now).Error; err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to soft-delete job application after initiate failure", "error", err, "id_job_application", jobApplication.IdJobApplication)
	}
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
