package extension

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
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

// POST /extension/application/:id/autofill

type AutofillApplicationQuestion struct {
	Question string `json:"question"`
	Id       string `json:"id"`
}

type AutofillApplicationRequest struct {
	Questions    []AutofillApplicationQuestion `json:"questions" binding:"required"`
	ContextUrls  []string                      `json:"contextUrls"`
}

type AutofillApplicationWorkflowQuestion struct {
	Id       string `json:"id"`
	Question string `json:"question"`
}

type AutofillApplicationWorkflowAnsweredQuestion struct {
	Id       string `json:"id"`
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

type AutofillApplicationWorkflowInput struct {
	Url              string                                `json:"url"`
	IdUser           uint                                  `json:"id_user"`
	IdJobApplication uint                                  `json:"id_job_application"`
	Questions        []AutofillApplicationWorkflowQuestion `json:"questions"`
	ContextUrls      []string                              `json:"context_urls"`
}

type AutofillApplicationWorkflowResponse struct {
	Questions []AutofillApplicationWorkflowAnsweredQuestion `json:"questions"`
}

type AutofillApplicationResponse struct {
	Questions []AutofillApplicationWorkflowAnsweredQuestion `json:"questions"`
}

func (e *Endpoint) AutofillApplication(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(c.Request.Context(), "unauthorized", "handler", "AutofillApplication")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var request AutofillApplicationRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		e.logger.WarnContext(c.Request.Context(), "failed to bind JSON", "handler", "AutofillApplication", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var jobApplication model.JobApplication
	if err := e.db.Preload("JobApplicationData").Where("id_user = ? AND id_external = ? AND deleted_at IS NULL", userId, c.Param("id")).First(&jobApplication).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Job application not found"})
			return
		}
		e.logger.ErrorContext(c.Request.Context(), "failed to get job application", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get job application"})
		return
	}

	workflowQuestions := make([]AutofillApplicationWorkflowQuestion, 0, len(request.Questions))
	for _, q := range request.Questions {
		workflowQuestions = append(workflowQuestions, AutofillApplicationWorkflowQuestion{
			Id:       q.Id,
			Question: q.Question,
		})
	}

	workflowID := fmt.Sprintf("autofill-application-%s-%s", jobApplication.IdExternal.String(), uuid.New().String())
	run, err := e.temporalClient.ExecuteWorkflow(c.Request.Context(), client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: string(e.taskQueueName),
	}, "AutofillApplicationWorkflow", AutofillApplicationWorkflowInput{
		Url:              jobApplication.Url,
		IdUser:           userId,
		IdJobApplication: jobApplication.IdJobApplication,
		Questions:        workflowQuestions,
		ContextUrls:      request.ContextUrls,
	})
	if err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to start autofill workflow", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start autofill workflow"})
		return
	}

	var workflowResponse AutofillApplicationWorkflowResponse
	if err := run.Get(c.Request.Context(), &workflowResponse); err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to get autofill workflow result", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get autofill workflow result"})
		return
	}

	incoming := make([]model.JobApplicationQuestions, 0, len(workflowResponse.Questions))
	for _, q := range workflowResponse.Questions {
		if strings.TrimSpace(q.Answer) == "" {
			continue
		}
		incoming = append(incoming, model.JobApplicationQuestions{
			Question:   q.Question,
			Answer:     q.Answer,
			IsOptional: false,
		})
	}

	if len(incoming) > 0 {
		mergedQuestions := mergeJobApplicationQuestions(jobApplication.JobApplicationData, incoming)

		tx := e.db.Begin()
		if tx.Error != nil {
			e.logger.ErrorContext(c.Request.Context(), "failed to begin autofill transaction", "error", tx.Error)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save application data"})
			return
		}

		var data model.JobApplicationData
		err := tx.Where("id_job_application = ?", jobApplication.IdJobApplication).First(&data).Error
		switch {
		case err == nil:
			if err := tx.Model(&data).Updates(map[string]any{
				"questions": mergedQuestions,
			}).Error; err != nil {
				_ = tx.Rollback()
				e.logger.ErrorContext(c.Request.Context(), "failed to update application data", "error", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save application data"})
				return
			}
		case errors.Is(err, gorm.ErrRecordNotFound):
			data = model.JobApplicationData{
				UserId:           jobApplication.UserId,
				JobApplicationId: jobApplication.IdJobApplication,
				Questions:        mergedQuestions,
			}
			if err := tx.Create(&data).Error; err != nil {
				_ = tx.Rollback()
				e.logger.ErrorContext(c.Request.Context(), "failed to create application data", "error", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save application data"})
				return
			}
		default:
			_ = tx.Rollback()
			e.logger.ErrorContext(c.Request.Context(), "failed to get application data", "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save application data"})
			return
		}

		if err := tx.Model(&jobApplication).Update("updated_at", time.Now()).Error; err != nil {
			_ = tx.Rollback()
			e.logger.ErrorContext(c.Request.Context(), "failed to update job application", "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save application data"})
			return
		}

		if err := tx.Commit().Error; err != nil {
			e.logger.ErrorContext(c.Request.Context(), "failed to commit autofill transaction", "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save application data"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"data": AutofillApplicationResponse{
		Questions: workflowResponse.Questions,
	}})
}

// POST /extension/application/:id/sync-data

type SyncApplicationDataRequest struct {
	Questions   []model.JobApplicationQuestions `json:"questions" binding:"required"`
	CoverLetter *string                         `json:"coverLetter"`
}

func (e *Endpoint) SyncApplicationData(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(c.Request.Context(), "unauthorized", "handler", "SyncApplicationData")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var request SyncApplicationDataRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		e.logger.WarnContext(c.Request.Context(), "failed to bind JSON", "handler", "SyncApplicationData", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var jobApplication model.JobApplication
	if err := e.db.Preload("JobApplicationData").Where("id_user = ? AND id_external = ? AND deleted_at IS NULL", userId, c.Param("id")).First(&jobApplication).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Job application not found"})
			return
		}
		e.logger.ErrorContext(c.Request.Context(), "failed to get job application", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get job application"})
		return
	}

	mergedQuestions := mergeJobApplicationQuestions(jobApplication.JobApplicationData, request.Questions)

	tx := e.db.Begin()
	if tx.Error != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to begin sync transaction", "error", tx.Error)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to sync application data"})
		return
	}

	var data model.JobApplicationData
	err := tx.Where("id_job_application = ?", jobApplication.IdJobApplication).First(&data).Error
	switch {
	case err == nil:
		updates := map[string]any{
			"questions": mergedQuestions,
		}
		if request.CoverLetter != nil {
			updates["cover_letter"] = request.CoverLetter
		}
		if err := tx.Model(&data).Updates(updates).Error; err != nil {
			_ = tx.Rollback()
			e.logger.ErrorContext(c.Request.Context(), "failed to update application data", "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to sync application data"})
			return
		}
	case errors.Is(err, gorm.ErrRecordNotFound):
		data = model.JobApplicationData{
			UserId:           jobApplication.UserId,
			JobApplicationId: jobApplication.IdJobApplication,
			Questions:        mergedQuestions,
			CoverLetter:      request.CoverLetter,
		}
		if err := tx.Create(&data).Error; err != nil {
			_ = tx.Rollback()
			e.logger.ErrorContext(c.Request.Context(), "failed to create application data", "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to sync application data"})
			return
		}
	default:
		_ = tx.Rollback()
		e.logger.ErrorContext(c.Request.Context(), "failed to get application data", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to sync application data"})
		return
	}

	if err := tx.Model(&jobApplication).Update("updated_at", time.Now()).Error; err != nil {
		_ = tx.Rollback()
		e.logger.ErrorContext(c.Request.Context(), "failed to update job application", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to sync application data"})
		return
	}

	if err := tx.Commit().Error; err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to commit sync transaction", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to sync application data"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": "Application data synced"})
}

// mergeJobApplicationQuestions folds existing + incoming Q&A into one list, keyed by
// trimmed case-insensitive question text so duplicates collapse to a single answer.
func mergeJobApplicationQuestions(existing *model.JobApplicationData, incoming []model.JobApplicationQuestions) model.JobApplicationQuestionsList {
	byKey := make(map[string]model.JobApplicationQuestions)
	order := make([]string, 0)

	upsert := func(q model.JobApplicationQuestions) {
		trimmed := strings.TrimSpace(q.Question)
		if trimmed == "" {
			return
		}
		key := strings.ToLower(trimmed)
		q.Question = trimmed
		if prev, ok := byKey[key]; ok {
			prev.Answer = q.Answer
			prev.IsOptional = q.IsOptional
			prev.Question = trimmed
			byKey[key] = prev
			return
		}
		byKey[key] = q
		order = append(order, key)
	}

	if existing != nil {
		for _, q := range existing.Questions {
			upsert(q)
		}
	}
	for _, q := range incoming {
		upsert(q)
	}

	out := make(model.JobApplicationQuestionsList, 0, len(order))
	for _, key := range order {
		out = append(out, byKey[key])
	}
	return out
}
