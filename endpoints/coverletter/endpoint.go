package coverletter

import (
	"context"
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

const coverLetterTimeout = 10 * time.Minute

type Endpoint struct {
	db             *gorm.DB
	temporalClient client.Client
	logger         *slog.Logger
	taskQueueName  temporal.TaskQueueName
}

func NewEndpoint(db *gorm.DB, temporalClient client.Client, logger *slog.Logger, taskQueueName temporal.TaskQueueName) *Endpoint {
	return &Endpoint{db: db, temporalClient: temporalClient, logger: logger, taskQueueName: taskQueueName}
}

// coverLetterWorkflowInput mirrors the worker's coverletter.CoverLetterWorkflowInput.
// Only IdJobApplication + IdUser are needed for the standalone (non-typing) path.
type coverLetterWorkflowInput struct {
	IdJobApplication uint              `json:"id_job_application"`
	IdUser           uint              `json:"id_user"`
	WorkflowID       *string           `json:"workflow_id,omitempty"`
	ElementIndex     *int              `json:"element_index,omitempty"`
	EditInstructions *editInstructions `json:"edit_instructions,omitempty"`
	// UltraWrite only applies in edit mode (EditInstructions set): true runs the
	// full analysis write instead of the lightweight edit.
	UltraWrite bool `json:"ultra_write,omitempty"`
}

type editInstructions struct {
	Instructions string `json:"instructions"`
}

type CreateCoverLetterInput struct {
	ResumeId       *string `json:"resumeId"`
	CompanyName    string  `json:"companyName" binding:"required"`
	JobTitle       string  `json:"jobTitle" binding:"required"`
	JobDescription string  `json:"jobDescription" binding:"required"`
	Url            string  `json:"url" binding:"required"`
}

type CreateCoverLetterResponse struct {
	JobApplicationId string `json:"jobApplicationId"`
	CoverLetter      string `json:"coverLetter"`
}

// POST /coverletter
func (e *Endpoint) CreateCoverLetter(c *gin.Context) {
	ctx := c.Request.Context()

	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(ctx, "unauthorized", "handler", "CreateCoverLetter")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var input CreateCoverLetterInput
	if err := c.ShouldBindJSON(&input); err != nil {
		e.logger.WarnContext(ctx, "failed to bind JSON", "handler", "CreateCoverLetter", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// The workflow always generates from the active resume; resolve it here so we can
	// persist the correct ResumeId on the resulting JobApplicationData.
	resume, err := e.resolveActiveResume(userId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "No active resume found"})
			return
		}
		e.logger.ErrorContext(ctx, "failed to resolve active resume", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to resolve active resume"})
		return
	}

	jobApplication, err := e.createCoverLetterApplication(userId, input)
	if err != nil {
		if utils.IsUniqueConstraintViolation(err) {
			e.logger.WarnContext(ctx, "cover letter application duplicate key", "error", err)
			c.JSON(http.StatusConflict, gin.H{"error": "Cover letter application already exists"})
			return
		}
		e.logger.ErrorContext(ctx, "failed to create cover letter application", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create cover letter application"})
		return
	}

	coverLetter, err := e.runCoverLetterWorkflow(ctx, *jobApplication.WorkflowID, userId, jobApplication.IdJobApplication, nil, false)
	if err != nil {
		e.markApplicationFailed(&jobApplication, err)
		e.logger.ErrorContext(ctx, "cover letter workflow failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate cover letter"})
		return
	}

	if err := e.persistCoverLetterData(&jobApplication, resume.IdResume, coverLetter); err != nil {
		e.logger.ErrorContext(ctx, "failed to persist cover letter data", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save cover letter"})
		return
	}

	c.JSON(http.StatusCreated, CreateCoverLetterResponse{
		JobApplicationId: jobApplication.IdExternal.String(),
		CoverLetter:      coverLetter,
	})
}

type RegenerateCoverLetterInput struct {
	JobApplicationId string `json:"jobApplicationId" binding:"required"`
	EditInstructions string `json:"editInstructions"`
	// UltraWrite runs the full-analysis write instead of the lightweight edit.
	// Only applies when EditInstructions is provided.
	UltraWrite bool `json:"ultraWrite"`
}

// POST /coverletter/regenerate
func (e *Endpoint) RegenerateCoverLetter(c *gin.Context) {
	ctx := c.Request.Context()

	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(ctx, "unauthorized", "handler", "RegenerateCoverLetter")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var input RegenerateCoverLetterInput
	if err := c.ShouldBindJSON(&input); err != nil {
		e.logger.WarnContext(ctx, "failed to bind JSON", "handler", "RegenerateCoverLetter", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	id, err := uuid.Parse(input.JobApplicationId)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid job application ID"})
		return
	}

	jobApplication, err := e.getCoverLetterApplication(id, userId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Cover letter not found"})
			return
		}
		e.logger.ErrorContext(ctx, "failed to load cover letter application", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load cover letter"})
		return
	}

	resume, err := e.resolveActiveResume(userId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "No active resume found"})
			return
		}
		e.logger.ErrorContext(ctx, "failed to resolve active resume", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to resolve active resume"})
		return
	}

	workflowId := newCoverLetterWorkflowID()
	if err := e.db.Model(&jobApplication).Updates(map[string]any{
		"status":      model.JobApplicationStatusPending,
		"workflow_id": &workflowId,
	}).Error; err != nil {
		e.logger.ErrorContext(ctx, "failed to update cover letter application on regenerate", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to regenerate cover letter"})
		return
	}

	var edit *editInstructions
	if input.EditInstructions != "" {
		edit = &editInstructions{Instructions: input.EditInstructions}
	}

	coverLetter, err := e.runCoverLetterWorkflow(ctx, workflowId, userId, jobApplication.IdJobApplication, edit, input.UltraWrite)
	if err != nil {
		e.markApplicationFailed(&jobApplication, err)
		e.logger.ErrorContext(ctx, "cover letter workflow failed on regenerate", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to regenerate cover letter"})
		return
	}

	if err := e.persistCoverLetterData(&jobApplication, resume.IdResume, coverLetter); err != nil {
		e.logger.ErrorContext(ctx, "failed to persist cover letter data on regenerate", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save cover letter"})
		return
	}

	c.JSON(http.StatusOK, CreateCoverLetterResponse{
		JobApplicationId: jobApplication.IdExternal.String(),
		CoverLetter:      coverLetter,
	})
}

type GetCoverLetterResponse struct {
	JobApplicationId string                     `json:"jobApplicationId"`
	CoverLetter      string                     `json:"coverLetter"`
	CompanyName      string                     `json:"companyName"`
	JobTitle         string                     `json:"jobTitle"`
	JobDescription   string                     `json:"jobDescription"`
	Url              string                     `json:"url"`
	ResumeId         string                     `json:"resumeId"`
	Status           model.JobApplicationStatus `json:"status"`
	CreatedAt        time.Time                  `json:"createdAt"`
}

// GET /coverletter/job-application/:jobApplicationId
func (e *Endpoint) GetCoverLetter(c *gin.Context) {
	ctx := c.Request.Context()

	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(ctx, "unauthorized", "handler", "GetCoverLetter")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	id, err := uuid.Parse(c.Param("jobApplicationId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid job application ID"})
		return
	}

	var jobApplication model.JobApplication
	if err := e.db.
		Where("id_external = ? AND id_user = ? AND cover_letter_only = true AND deleted_at IS NULL", id, userId).
		Preload("JobApplicationData").
		Preload("JobApplicationData.Resume").
		First(&jobApplication).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Cover letter not found"})
			return
		}
		e.logger.ErrorContext(ctx, "failed to fetch cover letter", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch cover letter"})
		return
	}

	c.JSON(http.StatusOK, buildCoverLetterResponse(&jobApplication))
}

type FetchCoverLettersRequest struct {
	Page   int    `form:"page"`
	Limit  int    `form:"limit"`
	Search string `form:"search"`
}

// CoverLetterListItem is GetCoverLetterResponse without the cover letter body.
type CoverLetterListItem struct {
	JobApplicationId string                     `json:"jobApplicationId"`
	CompanyName      string                     `json:"companyName"`
	JobTitle         string                     `json:"jobTitle"`
	JobDescription   string                     `json:"jobDescription"`
	Url              string                     `json:"url"`
	ResumeId         string                     `json:"resumeId"`
	Status           model.JobApplicationStatus `json:"status"`
	CreatedAt        time.Time                  `json:"createdAt"`
}

type FetchCoverLettersResponse struct {
	Data  []CoverLetterListItem `json:"data"`
	Total int                   `json:"total"`
	Page  int                   `json:"page"`
	Limit int                   `json:"limit"`
}

// GET /coverletter
func (e *Endpoint) GetCoverLetters(c *gin.Context) {
	ctx := c.Request.Context()

	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(ctx, "unauthorized", "handler", "GetCoverLetters")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var request FetchCoverLettersRequest
	if err := c.ShouldBindQuery(&request); err != nil {
		e.logger.WarnContext(ctx, "failed to bind query", "handler", "GetCoverLetters", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if request.Page < 1 {
		request.Page = 1
	}
	if request.Limit < 1 {
		request.Limit = 20
	}
	if request.Limit > 100 {
		request.Limit = 100
	}

	baseQuery := e.db.Model(&model.JobApplication{}).
		Where("id_user = ? AND cover_letter_only = true AND deleted_at IS NULL", userId).
		Preload("JobApplicationData").
		Preload("JobApplicationData.Resume")
	if request.Search != "" {
		like := "%" + request.Search + "%"
		baseQuery = baseQuery.Where("job_title ILIKE ? OR company_name ILIKE ?", like, like)
	}

	var total int64
	if err := baseQuery.Count(&total).Error; err != nil {
		e.logger.ErrorContext(ctx, "failed to count cover letters", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch cover letters"})
		return
	}

	var jobApplications []model.JobApplication
	if err := baseQuery.
		Order("created_at DESC").
		Limit(request.Limit).
		Offset((request.Page - 1) * request.Limit).
		Find(&jobApplications).Error; err != nil {
		e.logger.ErrorContext(ctx, "failed to fetch cover letters", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch cover letters"})
		return
	}

	items := make([]CoverLetterListItem, 0, len(jobApplications))
	for i := range jobApplications {
		items = append(items, buildCoverLetterListItem(&jobApplications[i]))
	}

	c.JSON(http.StatusOK, gin.H{"data": FetchCoverLettersResponse{
		Data:  items,
		Total: int(total),
		Page:  request.Page,
		Limit: request.Limit,
	}})
}

// ── helpers ──

func newCoverLetterWorkflowID() string {
	return fmt.Sprintf("cover-letter-%s", uuid.New().String())
}

func (e *Endpoint) resolveActiveResume(userId uint) (model.Resume, error) {
	var resume model.Resume
	err := e.db.Where("id_user = ? AND is_active = true AND deleted_at IS NULL", userId).First(&resume).Error
	return resume, err
}

func (e *Endpoint) createCoverLetterApplication(userId uint, input CreateCoverLetterInput) (model.JobApplication, error) {
	workflowId := newCoverLetterWorkflowID()
	jobApplication := model.JobApplication{
		UserId:          userId,
		JobTitle:        input.JobTitle,
		CompanyName:     input.CompanyName,
		JobDescription:  input.JobDescription,
		Url:             input.Url,
		Status:          model.JobApplicationStatusPending,
		CoverLetterOnly: true,
		WorkflowID:      &workflowId,
	}
	if err := e.db.Create(&jobApplication).Error; err != nil {
		return model.JobApplication{}, err
	}
	return jobApplication, nil
}

func (e *Endpoint) getCoverLetterApplication(id uuid.UUID, userId uint) (model.JobApplication, error) {
	var jobApplication model.JobApplication
	err := e.db.
		Where("id_external = ? AND id_user = ? AND cover_letter_only = true AND deleted_at IS NULL", id, userId).
		First(&jobApplication).Error
	return jobApplication, err
}

func (e *Endpoint) runCoverLetterWorkflow(ctx context.Context, workflowId string, userId, idJobApplication uint, edit *editInstructions, ultraWrite bool) (string, error) {
	options := client.StartWorkflowOptions{
		ID:                       workflowId,
		TaskQueue:                string(e.taskQueueName),
		WorkflowExecutionTimeout: coverLetterTimeout,
		WorkflowTaskTimeout:      1 * time.Minute,
	}

	workflowInput := coverLetterWorkflowInput{
		IdJobApplication: idJobApplication,
		IdUser:           userId,
		EditInstructions: edit,
		UltraWrite:       ultraWrite,
	}

	run, err := e.temporalClient.ExecuteWorkflow(ctx, options, "CoverLetterWorkflow", workflowInput)
	if err != nil {
		return "", fmt.Errorf("start cover letter workflow: %w", err)
	}

	var result map[string]any
	if err := run.Get(ctx, &result); err != nil {
		return "", fmt.Errorf("cover letter workflow execution: %w", err)
	}

	coverLetter, _ := result["cover_letter"].(string)
	if coverLetter == "" {
		return "", errors.New("cover letter workflow returned empty result")
	}
	return coverLetter, nil
}

// persistCoverLetterData upserts the JobApplicationData row with the generated cover letter
// and marks the application as applied.
func (e *Endpoint) persistCoverLetterData(jobApplication *model.JobApplication, resumeId uint, coverLetter string) error {
	return e.db.Transaction(func(tx *gorm.DB) error {
		var data model.JobApplicationData
		err := tx.Where("id_job_application = ?", jobApplication.IdJobApplication).First(&data).Error
		switch {
		case err == nil:
			if err := tx.Model(&data).Update("cover_letter", &coverLetter).Error; err != nil {
				return err
			}
		case errors.Is(err, gorm.ErrRecordNotFound):
			data = model.JobApplicationData{
				UserId:           jobApplication.UserId,
				ResumeId:         resumeId,
				JobApplicationId: jobApplication.IdJobApplication,
				CoverLetter:      &coverLetter,
				Questions:        model.JobApplicationQuestionsList{},
			}
			if err := tx.Create(&data).Error; err != nil {
				return err
			}
		default:
			return err
		}

		return tx.Model(jobApplication).Update("status", model.JobApplicationStatusApplied).Error
	})
}

func (e *Endpoint) markApplicationFailed(jobApplication *model.JobApplication, cause error) {
	reason := cause.Error()
	if err := e.db.Model(jobApplication).Updates(map[string]any{
		"status":         model.JobApplicationStatusFailed,
		"failure_reason": &reason,
	}).Error; err != nil {
		e.logger.Error("failed to mark cover letter application as failed", "error", err)
	}
}

func buildCoverLetterResponse(jobApplication *model.JobApplication) GetCoverLetterResponse {
	res := GetCoverLetterResponse{
		JobApplicationId: jobApplication.IdExternal.String(),
		CompanyName:      jobApplication.CompanyName,
		JobTitle:         jobApplication.JobTitle,
		JobDescription:   jobApplication.JobDescription,
		Url:              jobApplication.Url,
		Status:           jobApplication.Status,
		CreatedAt:        jobApplication.CreatedAt,
	}
	if data := jobApplication.JobApplicationData; data != nil {
		if data.CoverLetter != nil {
			res.CoverLetter = *data.CoverLetter
		}
		res.ResumeId = data.Resume.IdExternal.String()
	}
	return res
}

func buildCoverLetterListItem(jobApplication *model.JobApplication) CoverLetterListItem {
	item := CoverLetterListItem{
		JobApplicationId: jobApplication.IdExternal.String(),
		CompanyName:      jobApplication.CompanyName,
		JobTitle:         jobApplication.JobTitle,
		JobDescription:   jobApplication.JobDescription,
		Url:              jobApplication.Url,
		Status:           jobApplication.Status,
		CreatedAt:        jobApplication.CreatedAt,
	}
	if data := jobApplication.JobApplicationData; data != nil {
		item.ResumeId = data.Resume.IdExternal.String()
	}
	return item
}
