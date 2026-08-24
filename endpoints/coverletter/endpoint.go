package coverletter

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/SomtoJF/iris-api/model"
	redispubsub "github.com/SomtoJF/iris-api/pkg/redis"
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
	redisPubSub    *redispubsub.RedisPubSub
}

func NewEndpoint(db *gorm.DB, temporalClient client.Client, logger *slog.Logger, taskQueueName temporal.TaskQueueName, redisPubSub *redispubsub.RedisPubSub) *Endpoint {
	return &Endpoint{db: db, temporalClient: temporalClient, logger: logger, taskQueueName: taskQueueName, redisPubSub: redisPubSub}
}

type CreateCoverLetterInput struct {
	ResumeId       *string `json:"resumeId"`
	CompanyName    string  `json:"companyName" binding:"required"`
	JobTitle       string  `json:"jobTitle" binding:"required"`
	JobDescription string  `json:"jobDescription" binding:"required"`
	Url            string  `json:"url" binding:"required"`
}

// CreateCoverLetterResponse is returned immediately (202) once generation has been
// kicked off in the background. The cover letter body is delivered later via a
// COVER_LETTER_READY realtime event, not in this response.
type CreateCoverLetterResponse struct {
	Id     string                  `json:"id"`
	Status model.CoverLetterStatus `json:"status"`
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

	coverLetter, err := e.createStandaloneCoverLetter(userId, resume.IdResume, input)
	if err != nil {
		if utils.IsUniqueConstraintViolation(err) {
			e.logger.WarnContext(ctx, "cover letter duplicate key", "error", err)
			c.JSON(http.StatusConflict, gin.H{"error": "Cover letter already exists"})
			return
		}
		e.logger.ErrorContext(ctx, "failed to create cover letter", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create cover letter"})
		return
	}

	e.GenerateCoverLetterAsync(userId, coverLetter, *coverLetter.WorkflowID, nil, false)

	c.JSON(http.StatusAccepted, CreateCoverLetterResponse{
		Id:     coverLetter.IdExternal.String(),
		Status: coverLetter.Status,
	})
}

type RegenerateCoverLetterInput struct {
	Id               string `json:"id" binding:"required"`
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

	id, err := uuid.Parse(input.Id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid cover letter ID"})
		return
	}

	coverLetter, err := e.getStandaloneCoverLetter(id, userId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Cover letter not found"})
			return
		}
		e.logger.ErrorContext(ctx, "failed to load cover letter", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load cover letter"})
		return
	}

	workflowId := NewCoverLetterWorkflowID()
	if err := e.db.Model(&coverLetter).Updates(map[string]any{
		"status":         model.CoverLetterStatusProcessing,
		"workflow_id":    &workflowId,
		"body":           gorm.Expr("NULL"),
		"failure_reason": gorm.Expr("NULL"),
	}).Error; err != nil {
		e.logger.ErrorContext(ctx, "failed to update cover letter on regenerate", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to regenerate cover letter"})
		return
	}

	var edit *EditInstructions
	if input.EditInstructions != "" {
		edit = &EditInstructions{Instructions: input.EditInstructions}
	}

	coverLetter.Status = model.CoverLetterStatusProcessing
	coverLetter.WorkflowID = &workflowId
	e.GenerateCoverLetterAsync(userId, coverLetter, workflowId, edit, input.UltraWrite)

	c.JSON(http.StatusAccepted, CreateCoverLetterResponse{
		Id:     coverLetter.IdExternal.String(),
		Status: model.CoverLetterStatusProcessing,
	})
}

type GetCoverLetterResponse struct {
	Id             string                  `json:"id"`
	CoverLetter    string                  `json:"coverLetter"`
	CompanyName    string                  `json:"companyName"`
	JobTitle       string                  `json:"jobTitle"`
	JobDescription string                  `json:"jobDescription"`
	Url            string                  `json:"url"`
	ResumeId       string                  `json:"resumeId"`
	Status         model.CoverLetterStatus `json:"status"`
	CreatedAt      time.Time               `json:"createdAt"`
}

// GET /coverletter/:id
func (e *Endpoint) GetCoverLetter(c *gin.Context) {
	ctx := c.Request.Context()

	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(ctx, "unauthorized", "handler", "GetCoverLetter")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid cover letter ID"})
		return
	}

	var coverLetter model.CoverLetter
	if err := e.db.
		Where("id_external = ? AND id_user = ? AND id_job_application IS NULL AND deleted_at IS NULL", id, userId).
		Preload("Resume").
		First(&coverLetter).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Cover letter not found"})
			return
		}
		e.logger.ErrorContext(ctx, "failed to fetch cover letter", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch cover letter"})
		return
	}

	c.JSON(http.StatusOK, buildCoverLetterResponse(&coverLetter))
}

type FetchCoverLettersRequest struct {
	Page   int    `form:"page"`
	Limit  int    `form:"limit"`
	Search string `form:"search"`
}

// CoverLetterListItem is GetCoverLetterResponse without the cover letter body.
type CoverLetterListItem struct {
	Id             string                  `json:"id"`
	CompanyName    string                  `json:"companyName"`
	JobTitle       string                  `json:"jobTitle"`
	JobDescription string                  `json:"jobDescription"`
	Url            string                  `json:"url"`
	ResumeId       string                  `json:"resumeId"`
	Status         model.CoverLetterStatus `json:"status"`
	CreatedAt      time.Time               `json:"createdAt"`
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

	baseQuery := e.db.Model(&model.CoverLetter{}).
		Where("id_user = ? AND id_job_application IS NULL AND deleted_at IS NULL", userId).
		Preload("Resume")
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

	var coverLetters []model.CoverLetter
	if err := baseQuery.
		Order("created_at DESC").
		Limit(request.Limit).
		Offset((request.Page - 1) * request.Limit).
		Find(&coverLetters).Error; err != nil {
		e.logger.ErrorContext(ctx, "failed to fetch cover letters", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch cover letters"})
		return
	}

	items := make([]CoverLetterListItem, 0, len(coverLetters))
	for i := range coverLetters {
		items = append(items, buildCoverLetterListItem(&coverLetters[i]))
	}

	c.JSON(http.StatusOK, gin.H{"data": FetchCoverLettersResponse{
		Data:  items,
		Total: int(total),
		Page:  request.Page,
		Limit: request.Limit,
	}})
}

// ── helpers ──

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

func (e *Endpoint) createStandaloneCoverLetter(userId, resumeId uint, input CreateCoverLetterInput) (model.CoverLetter, error) {
	workflowId := NewCoverLetterWorkflowID()
	coverLetter := model.CoverLetter{
		UserId:         userId,
		ResumeId:       resumeId,
		JobTitle:       input.JobTitle,
		CompanyName:    input.CompanyName,
		JobDescription: input.JobDescription,
		Url:            input.Url,
		Status:         model.CoverLetterStatusProcessing,
		WorkflowID:     &workflowId,
	}
	if err := e.db.Create(&coverLetter).Error; err != nil {
		return model.CoverLetter{}, err
	}
	return coverLetter, nil
}

func (e *Endpoint) getStandaloneCoverLetter(id uuid.UUID, userId uint) (model.CoverLetter, error) {
	var coverLetter model.CoverLetter
	err := e.db.
		Where("id_external = ? AND id_user = ? AND id_job_application IS NULL AND deleted_at IS NULL", id, userId).
		First(&coverLetter).Error
	return coverLetter, err
}

func buildCoverLetterResponse(coverLetter *model.CoverLetter) GetCoverLetterResponse {
	res := GetCoverLetterResponse{
		Id:             coverLetter.IdExternal.String(),
		CompanyName:    coverLetter.CompanyName,
		JobTitle:       coverLetter.JobTitle,
		JobDescription: coverLetter.JobDescription,
		Url:            coverLetter.Url,
		Status:         coverLetter.Status,
		CreatedAt:      coverLetter.CreatedAt,
	}
	if coverLetter.Body != nil {
		res.CoverLetter = *coverLetter.Body
	}
	res.ResumeId = coverLetter.Resume.IdExternal.String()
	return res
}

func buildCoverLetterListItem(coverLetter *model.CoverLetter) CoverLetterListItem {
	item := CoverLetterListItem{
		Id:             coverLetter.IdExternal.String(),
		CompanyName:    coverLetter.CompanyName,
		JobTitle:       coverLetter.JobTitle,
		JobDescription: coverLetter.JobDescription,
		Url:            coverLetter.Url,
		Status:         coverLetter.Status,
		CreatedAt:      coverLetter.CreatedAt,
	}
	item.ResumeId = coverLetter.Resume.IdExternal.String()
	return item
}
