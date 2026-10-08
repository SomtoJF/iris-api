package jobapplication

import (
	"context"
	"encoding/json"
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
	"gorm.io/gorm/clause"
)

type Endpoint struct {
	db                    *gorm.DB
	temporalClient        client.Client
	logger                *slog.Logger
	taskQueueName         temporal.TaskQueueName
	browserPoolWorkflowId string
}

func NewEndpoint(db *gorm.DB, temporalClient client.Client, logger *slog.Logger, browserPoolWorkflowId string, taskQueueName temporal.TaskQueueName) *Endpoint {
	return &Endpoint{db: db, temporalClient: temporalClient, logger: logger, taskQueueName: taskQueueName, browserPoolWorkflowId: browserPoolWorkflowId}
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

type ApplyForJobRequest struct {
	Url string `json:"url" binding:"required"`
	// ResumeId is the external UUID of the resume to apply with. When omitted,
	// the user's active resume is used.
	ResumeId *string `json:"resumeId"`
}

type JobApplicationWorkflowInput struct {
	Url                   string `json:"url"`
	IdUser                uint   `json:"id_user"`
	IdResume              uint   `json:"id_resume"`
	IdJobApplication      uint   `json:"id_job_application"`
	ApplicationExternalId string `json:"application_external_id"`
}

type ApplicationQueueItem struct {
	IdJobApplication      uint   `json:"id_job_application"`
	Url                   string `json:"url"`
	IdUser                uint   `json:"id_user"`
	IdResume              uint   `json:"id_resume"`
	ApplicationWorkflowId string `json:"application_workflow_id"`
	NewReplayGeneration   bool   `json:"new_replay_generation,omitempty"`
	ResumeUserActionID    uint   `json:"resume_user_action_id,omitempty"`
}

type InitiateApplicationWorkflowInput struct {
	IdJobApplication      uint    `json:"id_job_application"`
	ApplyAutonomously     bool    `json:"apply_autonomously"`
	BrowserPoolWorkflowId *string `json:"browser_pool_workflow_id"`
}

const JOB_APPLICATION_INIT_TIMEOUT = 10 * time.Minute

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

	resume, err := e.resolveResume(userId, request.ResumeId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "No resume found"})
			return
		}
		e.logger.ErrorContext(c.Request.Context(), "failed to resolve resume", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to resolve resume"})
		return
	}

	applicationWorkflowId := fmt.Sprintf("job-application-%s-%s", request.Url, uuid.New().String())

	jobApplication := model.JobApplication{
		Url:                   request.Url,
		JobTitle:              "Pending-Job-Title",
		CompanyName:           "Pending-Company-Name",
		JobDescription:        "Pending-Job-Description",
		Status:                model.JobApplicationStatusPending,
		UserId:                userId,
		ResumeId:              resume.IdResume,
		WorkflowID:            &applicationWorkflowId,
		AppliedUsingExtension: false,
	}
	if err := e.db.Create(&jobApplication).Error; err != nil {
		if utils.IsUniqueConstraintViolation(err) {
			e.logger.WarnContext(c.Request.Context(), "job application duplicate key", "error", err)
			c.JSON(http.StatusConflict, gin.H{"error": "Job application already exists"})
			return
		}
		e.logger.ErrorContext(c.Request.Context(), "failed to create job application", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create job application: " + err.Error()})
		return
	}

	workflowOptions := client.StartWorkflowOptions{
		ID:                       fmt.Sprintf("initiate-application-%s-%s", request.Url, uuid.New().String()),
		TaskQueue:                string(e.taskQueueName),
		WorkflowExecutionTimeout: JOB_APPLICATION_INIT_TIMEOUT,
		WorkflowTaskTimeout:      1 * time.Minute,
	}

	workflowInput := InitiateApplicationWorkflowInput{
		IdJobApplication:      jobApplication.IdJobApplication,
		ApplyAutonomously:     true,
		BrowserPoolWorkflowId: &e.browserPoolWorkflowId,
	}
	_, err = e.temporalClient.ExecuteWorkflow(context.Background(), workflowOptions, "InitiateApplicationWorkflow", workflowInput)
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

	if jobApplication.Status != model.JobApplicationStatusFailed &&
		jobApplication.Status != model.JobApplicationStatusCancelled &&
		jobApplication.Status != model.JobApplicationStatusHalted {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only failed, cancelled, or halted applications can be retried"})
		return
	}

	workflowId := fmt.Sprintf("job-application-%s-%s", jobApplication.Url, uuid.New().String())

	tx := e.db.Begin()

	result := tx.Model(&jobApplication).
		Where("status IN ?", []model.JobApplicationStatus{
			model.JobApplicationStatusFailed,
			model.JobApplicationStatusCancelled,
			model.JobApplicationStatusHalted,
		}).
		Updates(map[string]any{
			"status":              model.JobApplicationStatusQueued,
			"workflow_id":         &workflowId,
			"created_at":          time.Now(),
			"failure_reason":      nil,
			"halt_reason":         nil,
			"cancellation_reason": nil,
		})
	if result.Error != nil {
		// no need to rollback here as nothing was updated
		e.logger.ErrorContext(c.Request.Context(), "failed to update job application on retry", "error", result.Error)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update job application"})
		return
	}
	if result.RowsAffected == 0 {
		// no need to rollback here nothing was updated
		c.JSON(http.StatusConflict, gin.H{"error": "Application state changed; please try again"})
		return
	}

	err = e.temporalClient.SignalWorkflow(
		context.Background(),
		e.browserPoolWorkflowId,
		"",
		"queue_application",
		ApplicationQueueItem{
			IdJobApplication:      jobApplication.IdJobApplication,
			Url:                   jobApplication.Url,
			IdUser:                userId,
			IdResume:              jobApplication.ResumeId,
			ApplicationWorkflowId: workflowId,
			NewReplayGeneration:   true,
		},
	)
	if err != nil {
		tx.Rollback()
		e.logger.ErrorContext(c.Request.Context(), "failed to queue job application retry", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to queue job application process"})
		return
	}

	if err := tx.Commit().Error; err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to commit transaction", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit transaction"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"message": "Job application initiated"})
}

type FetchAllJobApplicationsRequest struct {
	Page              int    `form:"page" binding:"required"`
	Limit             int    `form:"limit" binding:"required"`
	Search            string `form:"search"`
	Status            string `form:"status" binding:"omitempty,oneof=processing applied failed blocked cancelled halted"`
	StatusNot         string `form:"status_not" binding:"omitempty,oneof=processing applied failed blocked cancelled halted"`
	ResponseStatus    string `form:"response_status" binding:"omitempty,oneof=none rejected interviewing ghosted"`
	ResponseStatusNot string `form:"response_status_not" binding:"omitempty,oneof=none rejected interviewing ghosted"`
}

type JobApplication struct {
	Id                    string                     `json:"id"`
	Url                   string                     `json:"url"`
	JobTitle              string                     `json:"jobTitle"`
	CompanyName           string                     `json:"companyName"`
	Status                model.JobApplicationStatus `json:"status"`
	ResponseStatus        model.ResponseStatus       `json:"responseStatus"`
	HasApplicationData    bool                       `json:"hasApplicationData"`
	AppliedUsingExtension bool                       `json:"appliedUsingExtension"`
	AppliedAt             *time.Time                 `json:"appliedAt,omitempty"`
	FailureReason         *string                    `json:"failureReason,omitempty"`
	CancellationReason    *string                    `json:"cancellationReason,omitempty"`
	HaltReason            *string                    `json:"haltReason,omitempty"`
	CreatedAt             time.Time                  `json:"createdAt"`
	UpdatedAt             time.Time                  `json:"updatedAt"`
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
		Where("id_user = ? AND deleted_at IS NULL", userId).
		Preload("JobApplicationData")
	if request.Search != "" {
		baseQuery = baseQuery.Where("job_title LIKE ? OR company_name LIKE ?", "%"+request.Search+"%", "%"+request.Search+"%")
	}
	if request.Status != "" {
		baseQuery = baseQuery.Where("status = ?", request.Status)
	}
	if request.StatusNot != "" {
		baseQuery = baseQuery.Where("status != ?", request.StatusNot)
	}
	if request.ResponseStatus != "" {
		baseQuery = baseQuery.Where("response_status = ?", request.ResponseStatus)
	}
	if request.ResponseStatusNot != "" {
		baseQuery = baseQuery.Where("response_status != ?", request.ResponseStatusNot)
	}

	var total int64
	if err := baseQuery.Count(&total).Error; err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to count job applications", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch total job applications"})
		return
	}

	var jobApplications []model.JobApplication
	if err := baseQuery.Order("created_at DESC").Limit(request.Limit).Offset((request.Page - 1) * request.Limit).Find(&jobApplications).Error; err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to fetch job applications", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch job applications"})
		return
	}

	applications := make([]JobApplication, 0, len(jobApplications))
	for _, jobApplication := range jobApplications {
		applications = append(applications, JobApplication{
			Id:                    jobApplication.IdExternal.String(),
			Url:                   jobApplication.Url,
			JobTitle:              jobApplication.JobTitle,
			CompanyName:           jobApplication.CompanyName,
			Status:                jobApplication.Status,
			ResponseStatus:        jobApplication.ResponseStatus,
			HasApplicationData:    jobApplication.JobApplicationData != nil,
			AppliedUsingExtension: jobApplication.AppliedUsingExtension,
			FailureReason:         jobApplication.FailureReason,
			CancellationReason:    jobApplication.CancellationReason,
			HaltReason:            jobApplication.HaltReason,
			AppliedAt:             jobApplication.AppliedAt,
			CreatedAt:             jobApplication.CreatedAt,
			UpdatedAt:             jobApplication.UpdatedAt,
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
	DurablePause   bool                   `json:"durable_pause"`
}

type CancelApplicationRequest struct {
	Reason *string `json:"reason"`
}

type CancelSignalPayload struct {
	IdJobApplication uint   `json:"id_job_application"`
	Reason           string `json:"reason"`
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

	if !isCancellableApplicationStatus(jobApplication.Status) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Application cannot be cancelled in its current state"})
		return
	}

	cancelled, err := e.cancelApplication(&jobApplication, req.Reason)
	if err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to cancel application", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to cancel application"})
		return
	}
	if !cancelled {
		c.JSON(http.StatusConflict, gin.H{"error": "Application state changed; please try again"})
		return
	}

	reason := ""
	if req.Reason != nil {
		reason = *req.Reason
	}
	go func() {
		err := e.temporalClient.SignalWorkflow(
			context.Background(),
			e.browserPoolWorkflowId,
			"",
			"cancel_application",
			CancelSignalPayload{IdJobApplication: jobApplication.IdJobApplication, Reason: reason},
		)
		if err != nil {
			e.logger.Error("failed to signal browser pool for cancellation", "error", err, "idJobApplication", jobApplication.IdJobApplication)
		}
	}()

	c.JSON(http.StatusAccepted, gin.H{"message": "Application cancellation initiated"})
}

func isCancellableApplicationStatus(status model.JobApplicationStatus) bool {
	switch status {
	case model.JobApplicationStatusProcessing,
		model.JobApplicationStatusStarted,
		model.JobApplicationStatusPending,
		model.JobApplicationStatusQueued:
		return true
	default:
		return false
	}
}

func (e *Endpoint) cancelApplication(jobApplication *model.JobApplication, reason *string) (bool, error) {
	result := e.db.Model(jobApplication).
		Where("status IN ?", []model.JobApplicationStatus{
			model.JobApplicationStatusProcessing,
			model.JobApplicationStatusStarted,
			model.JobApplicationStatusPending,
			model.JobApplicationStatusQueued,
		}).
		Updates(map[string]any{
			"status":              model.JobApplicationStatusCancelled,
			"cancellation_reason": reason,
		})
	if result.Error != nil {
		return false, result.Error
	}

	return result.RowsAffected > 0, nil
}

func (e *Endpoint) DeleteApplication(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(c.Request.Context(), "unauthorized", "handler", "DeleteApplication")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
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
		e.logger.ErrorContext(c.Request.Context(), "failed to load job application for deletion", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load job application"})
		return
	}

	if jobApplication.Status != model.JobApplicationStatusFailed &&
		jobApplication.Status != model.JobApplicationStatusCancelled &&
		jobApplication.Status != model.JobApplicationStatusHalted {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only failed, cancelled, or halted applications can be deleted"})
		return
	}

	now := time.Now()
	if err := e.db.Model(&jobApplication).Update("deleted_at", &now).Error; err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to delete job application", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete job application"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Job application deleted"})
}

type PatchJobApplicationRequest struct {
	ResumeId       *string               `json:"resumeId"`
	ResponseStatus *model.ResponseStatus `json:"responseStatus" binding:"omitempty,oneof=none rejected interviewing ghosted"`
}

func (e *Endpoint) PatchJobApplication(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(c.Request.Context(), "unauthorized", "handler", "PatchJobApplication")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid job application ID"})
		return
	}

	var req PatchJobApplicationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		e.logger.WarnContext(c.Request.Context(), "failed to bind JSON", "handler", "PatchJobApplication", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.ResumeId == nil && req.ResponseStatus == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No updatable fields provided"})
		return
	}

	var jobApplication model.JobApplication
	if err := e.db.Where("id_external = ? AND id_user = ? AND deleted_at IS NULL", id, userId).First(&jobApplication).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Job application not found"})
			return
		}
		e.logger.ErrorContext(c.Request.Context(), "failed to load job application for patch", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load job application"})
		return
	}

	updates := make(map[string]any)
	if req.ResumeId != nil {
		resume, err := e.resolveResume(userId, req.ResumeId)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "No resume found"})
				return
			}
			e.logger.ErrorContext(c.Request.Context(), "failed to resolve resume for patch", "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to resolve resume"})
			return
		}
		updates["id_resume"] = resume.IdResume
	}
	if req.ResponseStatus != nil {
		updates["response_status"] = *req.ResponseStatus
	}

	if err := e.db.Model(&jobApplication).Updates(updates).Error; err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to update job application resume", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update job application"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Job application updated"})
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
	if err := e.db.Where("id_job_application = ? AND (is_pending = ? OR (durable_pause = ? AND submitted_at IS NOT NULL AND resume_enqueued_at IS NULL))",
		jobApp.IdJobApplication, true, true).
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
		DurablePause:   userAction.DurablePause,
	})
}

type SubmitUserActionRequest struct {
	UserActionID uint                         `json:"user_action_id" binding:"required"`
	Values       []model.UserActionResultItem `json:"values" binding:"required"`
}

type invalidUserActionSubmission string

func (e invalidUserActionSubmission) Error() string { return string(e) }

func isInvalidUserActionSubmission(err error) bool {
	var invalid invalidUserActionSubmission
	return errors.As(err, &invalid)
}

func (e *Endpoint) SubmitUserAction(c *gin.Context) {
	userID := c.GetUint("userId")
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	externalID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid job application ID"})
		return
	}
	var request SubmitUserActionRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var application model.JobApplication
	if err := e.db.Where("id_external = ? AND id_user = ? AND deleted_at IS NULL", externalID, userID).First(&application).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job application not found"})
		return
	}
	var action model.UserAction
	if err := e.db.Where("id_user_action = ? AND id_job_application = ? AND id_user = ?", request.UserActionID, application.IdJobApplication, userID).First(&action).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User action not found"})
		return
	}
	if !action.DurablePause {
		if err := validateUserActionValues(action.UserActionLayout, request.Values); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := e.temporalClient.SignalWorkflow(c.Request.Context(), action.WorkflowID, "", "USER_ACTION_RESULT", request.Values); err != nil {
			e.logger.ErrorContext(c.Request.Context(), "failed to deliver legacy user action signal", "error", err)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Failed to submit user action"})
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"message": "Action submitted"})
		return
	}

	err = e.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id_user_action = ? AND id_job_application = ? AND id_user = ?", request.UserActionID, application.IdJobApplication, userID).
			First(&action).Error; err != nil {
			return err
		}
		if action.SubmittedAt == nil {
			if !action.IsPending {
				return gorm.ErrRecordNotFound
			}
			if err := validateUserActionValues(action.UserActionLayout, request.Values); err != nil {
				return err
			}
			plain, err := json.Marshal(request.Values)
			if err != nil {
				return fmt.Errorf("encode user action values: %w", err)
			}
			ciphertext, err := utils.EncryptUserActionResult(string(plain), action.IdExternal)
			if err != nil {
				return err
			}
			resumeWorkflowID := "job-application-resume-" + uuid.NewString()
			now := time.Now().UTC()
			if err := tx.Model(&action).Updates(map[string]any{
				"result_ciphertext":  ciphertext,
				"submitted_at":       now,
				"is_pending":         false,
				"resume_workflow_id": resumeWorkflowID,
			}).Error; err != nil {
				return err
			}
			action.ResultCiphertext = ciphertext
			action.SubmittedAt = &now
			action.IsPending = false
			action.ResumeWorkflowID = resumeWorkflowID

			updated := tx.Model(&model.JobApplication{}).
				Where("id_job_application = ? AND status = ?", application.IdJobApplication, model.JobApplicationStatusBlocked).
				Update("status", model.JobApplicationStatusProcessing)
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected == 0 {
				return fmt.Errorf("application is no longer blocked")
			}
		}
		if len(action.ResultCiphertext) == 0 || action.ResumeWorkflowID == "" {
			return fmt.Errorf("submitted user action is missing resume data")
		}
		return nil
	})
	if err != nil {
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			c.JSON(http.StatusConflict, gin.H{"error": "User action is no longer pending"})
		case isInvalidUserActionSubmission(err):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			e.logger.ErrorContext(c.Request.Context(), "failed to persist user action submission", "error", err)
			if err.Error() == "application is no longer blocked" {
				c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to submit user action"})
		}
		return
	}

	queueItem := buildUserActionResumeQueueItem(application, action, userID)
	if err := e.temporalClient.SignalWorkflow(c.Request.Context(), e.browserPoolWorkflowId, "", "queue_application", queueItem); err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to requeue application after user action", "error", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Answers saved; application requeue will retry"})
		return
	}
	now := time.Now().UTC()
	if err := e.db.Model(&model.UserAction{}).
		Where("id_user_action = ? AND id_user = ? AND submitted_at IS NOT NULL AND resume_enqueued_at IS NULL", action.IdUserAction, userID).
		Update("resume_enqueued_at", now).Error; err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to mark user-action resume as queued", "error", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Answers saved; application resume will retry"})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"message": "Application resumed"})
}

func buildUserActionResumeQueueItem(application model.JobApplication, action model.UserAction, userID uint) ApplicationQueueItem {
	return ApplicationQueueItem{
		IdJobApplication:      application.IdJobApplication,
		Url:                   application.Url,
		IdUser:                userID,
		IdResume:              application.ResumeId,
		ApplicationWorkflowId: action.ResumeWorkflowID,
		ResumeUserActionID:    action.IdUserAction,
	}
}

func validateUserActionValues(layout model.UserActionLayout, values []model.UserActionResultItem) error {
	if len(layout) == 0 || len(values) != len(layout) {
		return invalidUserActionSubmission("submitted answers do not match the requested fields")
	}
	expected := make(map[string]struct{}, len(layout))
	for _, item := range layout {
		if item.FieldName == "" {
			return invalidUserActionSubmission("user action contains an unnamed field")
		}
		if _, exists := expected[item.FieldName]; exists {
			return invalidUserActionSubmission("user action contains duplicate fields")
		}
		expected[item.FieldName] = struct{}{}
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, ok := expected[value.FieldName]; !ok {
			return invalidUserActionSubmission("submitted answer contains an unknown field")
		}
		if _, exists := seen[value.FieldName]; exists {
			return invalidUserActionSubmission("submitted answer contains duplicate fields")
		}
		seen[value.FieldName] = struct{}{}
		if len(value.Value) > 10000 {
			return invalidUserActionSubmission("submitted answer exceeds the allowed size")
		}
	}
	if len(seen) != len(expected) {
		return invalidUserActionSubmission("submitted answers do not match the requested fields")
	}
	return nil
}

type ResumeSummary struct {
	Id          string  `json:"id"`
	DisplayName *string `json:"displayName,omitempty"`
	FileName    string  `json:"fileName"`
}

type JobApplicationComprehensiveResponse struct {
	Id                    string                          `json:"id"`
	AppliedAt             *time.Time                      `json:"appliedAt,omitempty"`
	AppliedUsingExtension bool                            `json:"appliedUsingExtension"`
	Url                   string                          `json:"url"`
	JobTitle              string                          `json:"jobTitle"`
	CompanyName           string                          `json:"companyName"`
	Status                string                          `json:"status"`
	Questions             []model.JobApplicationQuestions `json:"questions"`
	JobDescription        string                          `json:"jobDescription"`
	CoverLetter           *string                         `json:"coverLetter"`
	CoverLetterStatus     model.CoverLetterStatus         `json:"coverLetterStatus,omitempty"`
	Resume                ResumeSummary                   `json:"resume"`
}

// get /jobs/:id/comprehensive
func (e *Endpoint) FetchJobApplicationComprehensive(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(c.Request.Context(), "unauthorized", "handler", "FetchJobApplicationComprehensive")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid job application ID"})
		return
	}

	var jobApplication model.JobApplication
	if err := e.db.Preload("JobApplicationData").Preload("CoverLetter").Preload("Resume").Where("id_external = ? AND id_user = ?", id, userId).First(&jobApplication).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Job application not found"})
		return
	}

	var questions []model.JobApplicationQuestions
	var coverLetter *string
	var coverLetterStatus model.CoverLetterStatus
	if jobApplication.JobApplicationData != nil {
		questions = jobApplication.JobApplicationData.Questions
	}
	if jobApplication.CoverLetter != nil {
		coverLetterStatus = jobApplication.CoverLetter.Status
		if jobApplication.CoverLetter.Body != nil {
			coverLetter = jobApplication.CoverLetter.Body
		}
	}

	c.JSON(http.StatusOK, gin.H{"data": JobApplicationComprehensiveResponse{
		Id:                    jobApplication.IdExternal.String(),
		AppliedUsingExtension: jobApplication.AppliedUsingExtension,
		Url:                   jobApplication.Url,
		JobTitle:              jobApplication.JobTitle,
		CompanyName:           jobApplication.CompanyName,
		Status:                string(jobApplication.Status),
		Questions:             questions,
		JobDescription:        jobApplication.JobDescription,
		CoverLetter:           coverLetter,
		CoverLetterStatus:     coverLetterStatus,
		AppliedAt:             jobApplication.AppliedAt,
		Resume: ResumeSummary{
			Id:          jobApplication.Resume.IdExternal.String(),
			DisplayName: jobApplication.Resume.DisplayName,
			FileName:    jobApplication.Resume.FileName,
		},
	}})
}
