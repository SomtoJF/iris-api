package jobapplication

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/SomtoJF/iris-api/model"
	"github.com/SomtoJF/iris-api/temporal"
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

func NewEndpoint(db *gorm.DB, temporalClient client.Client, logger *slog.Logger, taskQueueName temporal.TaskQueueName) *Endpoint {
	return &Endpoint{db: db, temporalClient: temporalClient, logger: logger, taskQueueName: taskQueueName}
}

type ApplyForJobRequest struct {
	Url string `json:"url" binding:"required"`
}

type JobApplicationWorkflowInput struct {
	Url                   string `json:"url"`
	IdUser                uint   `json:"id_user"`
	IdJobApplication      uint   `json:"id_job_application"`
	ApplicationExternalId string `json:"application_external_id"`
}

const JOB_APPLICATION_TIMEOUT = 24 * time.Hour

func (e *Endpoint) ApplyForJob(c *gin.Context) {
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

	workflowId := fmt.Sprintf("job-application-%s-%s", request.Url, uuid.New().String())

	jobApplication := model.JobApplication{
		Url:            request.Url,
		JobTitle:       "Pending-Job-Title",
		CompanyName:    "Pending-Company-Name",
		JobDescription: "Pending-Job-Description",
		Status:         model.JobApplicationStatusPending,
		UserId:         userId,
		WorkflowID:     &workflowId,
	}
	if err := e.db.Create(&jobApplication).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			e.logger.WarnContext(c.Request.Context(), "job application duplicate key", "error", err)
			c.JSON(http.StatusConflict, gin.H{"error": "Job application already exists"})
			return
		}
		e.logger.ErrorContext(c.Request.Context(), "failed to create job application", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create job application: " + err.Error()})
		return
	}

	workflowOptions := client.StartWorkflowOptions{
		ID:                       workflowId,
		TaskQueue:                string(e.taskQueueName),
		WorkflowExecutionTimeout: JOB_APPLICATION_TIMEOUT,
		WorkflowTaskTimeout:      1 * time.Minute,
	}

	workflowInput := JobApplicationWorkflowInput{
		Url:                   request.Url,
		IdJobApplication:      jobApplication.IdJobApplication,
		IdUser:                userId,
		ApplicationExternalId: jobApplication.IdExternal.String(),
	}
	_, err := e.temporalClient.ExecuteWorkflow(context.Background(), workflowOptions, "JobApplicationWorkflow", workflowInput)
	if err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to start job application workflow", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start job application process"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"message": "Job application initiated"})
}

// post /job/:id/retry-application
func (e *Endpoint) RetryApplication(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(c.Request.Context(), "unauthorized", "handler", "RetryApplication")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid job application ID"})
		return
	}

	var jobApplication model.JobApplication
	if err := e.db.Where("id_external = ? AND id_user = ?", id, userId).First(&jobApplication).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job application not found"})
		return
	}

	workflowId := fmt.Sprintf("job-application-%s-%s", jobApplication.Url, uuid.New().String())

	if err := e.db.Model(&jobApplication).Updates(map[string]any{
		"status":      model.JobApplicationStatusPending,
		"workflow_id": &workflowId,
		"created_at":  time.Now(),
	}).Error; err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to update job application on retry", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update job application"})
		return
	}

	workflowOptions := client.StartWorkflowOptions{
		ID:                       workflowId,
		TaskQueue:                string(e.taskQueueName),
		WorkflowExecutionTimeout: JOB_APPLICATION_TIMEOUT,
		WorkflowTaskTimeout:      1 * time.Minute,
	}

	workflowInput := JobApplicationWorkflowInput{
		Url:                   jobApplication.Url,
		IdJobApplication:      jobApplication.IdJobApplication,
		IdUser:                userId,
		ApplicationExternalId: jobApplication.IdExternal.String(),
	}
	_, err = e.temporalClient.ExecuteWorkflow(context.Background(), workflowOptions, "JobApplicationWorkflow", workflowInput)
	if err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to start job application workflow on retry", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start job application process"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"message": "Job application initiated"})
}

type FetchAllJobApplicationsRequest struct {
	Page   int    `form:"page" binding:"required"`
	Limit  int    `form:"limit" binding:"required"`
	Search string `form:"search"`
}

type JobApplication struct {
	Id                 string                     `json:"id"`
	Url                string                     `json:"url"`
	JobTitle           string                     `json:"jobTitle"`
	CompanyName        string                     `json:"companyName"`
	Status             model.JobApplicationStatus `json:"status"`
	HasApplicationData bool                       `json:"hasApplicationData"`
	FailureReason      *string                    `json:"failureReason,omitempty"`
	CreatedAt          time.Time                  `json:"createdAt"`
	UpdatedAt          time.Time                  `json:"updatedAt"`
}

type FetchAllJobApplicationsResponse struct {
	Data  []JobApplication `json:"data"`
	Total int              `json:"total"`
	Page  int              `json:"page"`
	Limit int              `json:"limit"`
}

func (e *Endpoint) FetchAllJobApplications(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(c.Request.Context(), "unauthorized", "handler", "FetchAllJobApplications")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var request FetchAllJobApplicationsRequest
	if err := c.ShouldBindQuery(&request); err != nil {
		e.logger.WarnContext(c.Request.Context(), "failed to bind query", "handler", "FetchAllJobApplications", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	baseQuery := e.db.Model(&model.JobApplication{}).
		Where("id_user = ?", userId).
		Preload("JobApplicationData")
	if request.Search != "" {
		baseQuery = baseQuery.Where("job_title LIKE ? OR company_name LIKE ?", "%"+request.Search+"%", "%"+request.Search+"%")
	}

	var jobApplications []model.JobApplication
	if err := baseQuery.Order("created_at DESC").Limit(request.Limit).Offset((request.Page - 1) * request.Limit).Find(&jobApplications).Error; err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to fetch job applications", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch job applications"})
		return
	}

	var total int64
	if err := baseQuery.Count(&total).Error; err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to count job applications", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch total job applications"})
		return
	}
	applications := make([]JobApplication, 0, len(jobApplications))
	for _, jobApplication := range jobApplications {
		applications = append(applications, JobApplication{
			Id:                 jobApplication.IdExternal.String(),
			Url:                jobApplication.Url,
			JobTitle:           jobApplication.JobTitle,
			CompanyName:        jobApplication.CompanyName,
			Status:             jobApplication.Status,
			HasApplicationData: jobApplication.JobApplicationData != nil,
			FailureReason:      jobApplication.FailureReason,
			CreatedAt:          jobApplication.CreatedAt,
			UpdatedAt:          jobApplication.UpdatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": FetchAllJobApplicationsResponse{
		Data:  applications,
		Total: int(total),
		Page:  request.Page,
		Limit: request.Limit,
	}})
}

type UserActionResponse struct {
	ID             uint                   `json:"id"`
	UserActionType model.UserActionType   `json:"user_action_type"`
	ActionDetails  string                 `json:"action_details"`
	Layout         model.UserActionLayout `json:"layout"`
	WorkflowID     string                 `json:"workflow_id"`
	SignalName     string                 `json:"signal_name"`
}

type CancelApplicationRequest struct {
	Reason *string `json:"reason"`
}

type CancelSignalPayload struct {
	Reason string `json:"reason"`
}

func (e *Endpoint) CancelApplication(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid job application ID"})
		return
	}

	var req CancelApplicationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var jobApplication model.JobApplication
	if err := e.db.Where("id_external = ? AND id_user = ?", id, userId).First(&jobApplication).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job application not found"})
		return
	}

	if jobApplication.Status != model.JobApplicationStatusPending {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Application is not currently processing"})
		return
	}

	if jobApplication.WorkflowID == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No workflow associated with this application"})
		return
	}

	reason := ""
	if req.Reason != nil {
		reason = *req.Reason
	}

	err = e.temporalClient.SignalWorkflow(
		context.Background(),
		*jobApplication.WorkflowID,
		"",
		"CANCEL_APPLICATION",
		CancelSignalPayload{Reason: reason},
	)
	if err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to signal workflow for cancellation", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to cancel application"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Application cancellation initiated"})
}

func (e *Endpoint) GetUserAction(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(c.Request.Context(), "unauthorized", "handler", "GetUserAction")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid job application ID"})
		return
	}

	var jobApp model.JobApplication
	if err := e.db.Where("id_external = ? AND id_user = ?", id, userId).First(&jobApp).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job application not found"})
		return
	}

	var userAction model.UserAction
	if err := e.db.Where("id_job_application = ? AND is_pending = ?", jobApp.IdJobApplication, true).
		Order("created_at ASC").
		First(&userAction).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "No pending user action found"})
		return
	}

	c.JSON(http.StatusOK, UserActionResponse{
		ID:             userAction.IdUserAction,
		UserActionType: userAction.UserActionType,
		ActionDetails:  userAction.ActionDetails,
		Layout:         userAction.UserActionLayout,
		WorkflowID:     userAction.WorkflowID,
		SignalName:     "USER_ACTION_RESULT",
	})
}
