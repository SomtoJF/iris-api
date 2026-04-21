package issue

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

type CreateIssueRequest struct {
	Title            string          `json:"title" binding:"required"`
	Type             string          `json:"type" binding:"required"`
	JobApplicationId string          `json:"jobApplicationId"`
	ContentJSON      json.RawMessage `json:"contentJson" binding:"required"`
	ContentText      string          `json:"contentText" binding:"required"`
}

type jobApplication struct {
	Id            string    `json:"id"`
	Title         string    `json:"title"`
	CompanyName   string    `json:"companyName"`
	Url           string    `json:"url"`
	Status        string    `json:"status"`
	FailureReason *string   `json:"failureReason"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type GetIssueResponse struct {
	Id               string          `json:"id"`
	Title            string          `json:"title"`
	Type             string          `json:"type"`
	JobApplicationId string          `json:"jobApplicationId"`
	ContentJSON      json.RawMessage `json:"contentJson"`
	ContentText      string          `json:"contentText"`
	Summary          string          `json:"summary"`
	IsResolved       bool            `json:"isResolved"`
	OwnerId          string          `json:"ownerId"`
	IsUserOwner      bool            `json:"isUserOwner"`
	JobApplication   *jobApplication `json:"jobApplication,omitempty"`
	UpvoteCount      int             `json:"upvoteCount"`
	UserUpvoted      bool            `json:"userUpvoted"`
	CreatedAt        time.Time       `json:"createdAt"`
	UpdatedAt        time.Time       `json:"updatedAt"`
}

type GetIssueCommentsResponse struct {
	Id          string          `json:"id"`
	CommentJSON json.RawMessage `json:"commentJson"`
	CommentText string          `json:"commentText"`
	OwnerId     string          `json:"ownerId"`
	IsUserOwner bool            `json:"isUserOwner"`
	UpvoteCount int             `json:"upvoteCount"`
	UserUpvoted bool            `json:"userUpvoted"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

type GetIssueCommentsRequest struct {
	Page  int `form:"page" binding:"required"`
	Limit int `form:"limit" binding:"required"`
}

type GetIssueCommentsPaginatedResponse struct {
	Data  []GetIssueCommentsResponse `json:"data"`
	Total int                        `json:"total"`
	Page  int                        `json:"page"`
	Limit int                        `json:"limit"`
}

type CommentOnIssueRequest struct {
	CommentJSON json.RawMessage `json:"commentJson" binding:"required"`
	CommentText string          `json:"commentText" binding:"required"`
}

type FetchIssuesRequest struct {
	Page     int    `form:"page" binding:"required"`
	Limit    int    `form:"limit" binding:"required"`
	Search   string `form:"search"`
	Type     string `form:"type"`
	Resolved *bool  `form:"resolved"`
}

type IssueListItem struct {
	Id           string    `json:"id"`
	Title        string    `json:"title"`
	Type         string    `json:"type"`
	Summary      string    `json:"summary"`
	IsResolved   bool      `json:"isResolved"`
	UpvoteCount  int       `json:"upvoteCount"`
	CommentCount int       `json:"commentCount"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type FetchIssuesResponse struct {
	Data  []IssueListItem `json:"data"`
	Total int             `json:"total"`
	Page  int             `json:"page"`
	Limit int             `json:"limit"`
}

func jobApplicationToDTO(ja *model.JobApplication) *jobApplication {
	if ja == nil {
		return nil
	}
	return &jobApplication{
		Id:            ja.IdExternal.String(),
		Title:         ja.JobTitle,
		CompanyName:   ja.CompanyName,
		Url:           ja.Url,
		Status:        string(ja.Status),
		FailureReason: ja.FailureReason,
		CreatedAt:     ja.CreatedAt,
		UpdatedAt:     ja.UpdatedAt,
	}
}

func (e *Endpoint) issueUpvoteMeta(issuePK uint, userId uint) (count int64, userUpvoted bool) {
	e.db.Model(&model.IssueUpvote{}).Where("id_issue = ?", issuePK).Count(&count)
	var row model.IssueUpvote
	err := e.db.Where("id_issue = ? AND id_user = ?", issuePK, userId).Take(&row).Error
	userUpvoted = err == nil
	return count, userUpvoted
}

func (e *Endpoint) commentUpvoteMeta(commentPK uint, userId uint) (count int64, userUpvoted bool) {
	e.db.Model(&model.IssueCommentUpvote{}).Where("id_issue_comment = ?", commentPK).Count(&count)
	var row model.IssueCommentUpvote
	err := e.db.Where("id_issue_comment = ? AND id_user = ?", commentPK, userId).Take(&row).Error
	userUpvoted = err == nil
	return count, userUpvoted
}

// [post] /issue
func (e *Endpoint) CreateIssue(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(c.Request.Context(), "unauthorized", "handler", "CreateIssue")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var request CreateIssueRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		e.logger.WarnContext(c.Request.Context(), "failed to bind JSON", "handler", "CreateIssue", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var jobAppID *uint
	if request.JobApplicationId != "" {
		var jobApplication model.JobApplication
		if err := e.db.Where("id_external = ?", request.JobApplicationId).First(&jobApplication).Error; err != nil {
			e.logger.WarnContext(c.Request.Context(), "job application not found for issue create", "error", err)
			c.JSON(http.StatusNotFound, gin.H{"error": "Job application not found"})
			return
		}
		jobAppID = &jobApplication.IdJobApplication
	}

	issueType := model.IssueType(request.Type)

	issue := model.Issue{
		Title:            request.Title,
		Type:             issueType,
		JobApplicationId: jobAppID,
		ContentJSON:      request.ContentJSON,
		ContentText:      request.ContentText,
		UserId:           userId,
	}
	if err := e.db.Create(&issue).Error; err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to create issue", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create issue"})
		return
	}

	// Fire-and-forget: kick off summary generation asynchronously.
	workflowOptions := client.StartWorkflowOptions{
		ID:        fmt.Sprintf("summarize-issue-%s-%s", issue.IdExternal.String(), uuid.New().String()),
		TaskQueue: string(e.taskQueueName),
	}
	workflowInput := map[string]string{
		"issue_external_id": issue.IdExternal.String(),
	}
	if _, err := e.temporalClient.ExecuteWorkflow(context.Background(), workflowOptions, "SummarizeIssueWorkflow", workflowInput); err != nil {
		e.logger.WarnContext(c.Request.Context(), "failed to start summarize issue workflow", "issue_id", issue.IdExternal.String(), "error", err)
	}

	c.JSON(http.StatusCreated, gin.H{"message": "Issue created successfully"})
}

// [get] /issues
func (e *Endpoint) FetchIssues(c *gin.Context) {
	reqCtx := c.Request.Context()
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(reqCtx, "unauthorized", "handler", "FetchIssues")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var request FetchIssuesRequest
	if err := c.ShouldBindQuery(&request); err != nil {
		e.logger.WarnContext(reqCtx, "failed to bind query", "handler", "FetchIssues", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	baseQuery := e.db.Model(&model.Issue{})
	if request.Resolved != nil {
		baseQuery = baseQuery.Where("is_resolved = ?", *request.Resolved)
	}
	if request.Type != "" {
		baseQuery = baseQuery.Where("type = ?", request.Type)
	}
	if request.Search != "" {
		like := "%" + request.Search + "%"
		baseQuery = baseQuery.Where("title LIKE ? OR summary LIKE ?", like, like)
	}

	var issues []model.Issue
	if err := baseQuery.
		Order("created_at DESC").
		Limit(request.Limit).
		Offset((request.Page - 1) * request.Limit).
		Find(&issues).Error; err != nil {
		e.logger.ErrorContext(reqCtx, "failed to fetch issues", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch issues"})
		return
	}

	var total int64
	if err := baseQuery.Count(&total).Error; err != nil {
		e.logger.ErrorContext(reqCtx, "failed to count issues", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch total issues"})
		return
	}

	out := make([]IssueListItem, 0, len(issues))
	for _, issue := range issues {
		var commentCount int64
		e.db.Model(&model.IssueComment{}).Where("id_issue = ?", issue.IdIssue).Count(&commentCount)

		var upvoteCount int64
		e.db.Model(&model.IssueUpvote{}).Where("id_issue = ?", issue.IdIssue).Count(&upvoteCount)

		summary := ""
		if issue.Summary != nil {
			summary = *issue.Summary
		}

		out = append(out, IssueListItem{
			Id:           issue.IdExternal.String(),
			Title:        issue.Title,
			Type:         string(issue.Type),
			Summary:      summary,
			IsResolved:   issue.IsResolved,
			UpvoteCount:  int(upvoteCount),
			CommentCount: int(commentCount),
			CreatedAt:    issue.CreatedAt,
			UpdatedAt:    issue.UpdatedAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{"data": FetchIssuesResponse{
		Data:  out,
		Total: int(total),
		Page:  request.Page,
		Limit: request.Limit,
	}})
}

// [get] /issue/{id}
func (e *Endpoint) GetIssue(c *gin.Context) {
	reqCtx := c.Request.Context()
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(reqCtx, "unauthorized", "handler", "GetIssue")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	idParam := c.Param("id")
	issueUUID, err := uuid.Parse(idParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid issue ID"})
		return
	}

	var issue model.Issue
	if err := e.db.Where("id_external = ?", issueUUID).Preload("User").Preload("JobApplication").First(&issue).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Issue not found"})
			return
		}
		e.logger.ErrorContext(reqCtx, "failed to load issue", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load issue"})
		return
	}

	upCount, userUp := e.issueUpvoteMeta(issue.IdIssue, userId)

	jobAppIDStr := ""
	if issue.JobApplicationId != nil && issue.JobApplication != nil {
		jobAppIDStr = issue.JobApplication.IdExternal.String()
	}

	summary := ""
	if issue.Summary != nil {
		summary = *issue.Summary
	}

	c.JSON(http.StatusOK, GetIssueResponse{
		Id:               issue.IdExternal.String(),
		Title:            issue.Title,
		Type:             string(issue.Type),
		JobApplicationId: jobAppIDStr,
		ContentJSON:      issue.ContentJSON,
		ContentText:      issue.ContentText,
		Summary:          summary,
		IsResolved:       issue.IsResolved,
		OwnerId:          issue.User.IdExternal.String(),
		IsUserOwner:      issue.UserId == userId,
		JobApplication:   jobApplicationToDTO(issue.JobApplication),
		UpvoteCount:      int(upCount),
		UserUpvoted:      userUp,
		CreatedAt:        issue.CreatedAt,
		UpdatedAt:        issue.UpdatedAt,
	})
}

// [get] /issue/{id}/comments
func (e *Endpoint) GetIssueComments(c *gin.Context) {
	reqCtx := c.Request.Context()
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(reqCtx, "unauthorized", "handler", "GetIssueComments")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	issueUUID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid issue ID"})
		return
	}

	var request GetIssueCommentsRequest
	if err := c.ShouldBindQuery(&request); err != nil {
		e.logger.WarnContext(reqCtx, "failed to bind query", "handler", "GetIssueComments", "error", err)
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

	var issue model.Issue
	if err := e.db.Where("id_external = ?", issueUUID).First(&issue).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Issue not found"})
			return
		}
		e.logger.ErrorContext(reqCtx, "failed to load issue", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load issue"})
		return
	}

	var comments []model.IssueComment
	baseQuery := e.db.Model(&model.IssueComment{}).Where("id_issue = ?", issue.IdIssue)

	if err := baseQuery.
		Preload("User").
		Order("created_at ASC").
		Limit(request.Limit).
		Offset((request.Page - 1) * request.Limit).
		Find(&comments).Error; err != nil {
		e.logger.ErrorContext(reqCtx, "failed to load issue comments", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load comments"})
		return
	}

	var total int64
	if err := baseQuery.Count(&total).Error; err != nil {
		e.logger.ErrorContext(reqCtx, "failed to count issue comments", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load comments"})
		return
	}

	out := make([]GetIssueCommentsResponse, 0, len(comments))
	for _, com := range comments {
		upCount, userUp := e.commentUpvoteMeta(com.IdIssueComment, userId)
		out = append(out, GetIssueCommentsResponse{
			Id:          com.IdExternal.String(),
			CommentJSON: com.CommentJSON,
			CommentText: com.CommentText,
			OwnerId:     com.User.IdExternal.String(),
			IsUserOwner: com.UserId == userId,
			UpvoteCount: int(upCount),
			UserUpvoted: userUp,
			CreatedAt:   com.CreatedAt,
			UpdatedAt:   com.UpdatedAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{"data": GetIssueCommentsPaginatedResponse{
		Data:  out,
		Total: int(total),
		Page:  request.Page,
		Limit: request.Limit,
	}})
}

// [post] /issue/{id}/comments/{commentId}/upvote
func (e *Endpoint) UpvoteIssueComment(c *gin.Context) {
	reqCtx := c.Request.Context()
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(reqCtx, "unauthorized", "handler", "UpvoteIssueComment")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	issueUUID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid issue ID"})
		return
	}
	commentUUID, err := uuid.Parse(c.Param("commentId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid comment ID"})
		return
	}

	var issue model.Issue
	if err := e.db.Where("id_external = ?", issueUUID).First(&issue).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Issue not found"})
			return
		}
		e.logger.ErrorContext(reqCtx, "failed to load issue", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load issue"})
		return
	}

	if issue.IsResolved {
		c.JSON(http.StatusConflict, gin.H{"error": "Issue is resolved"})
		return
	}

	var comment model.IssueComment
	if err := e.db.Where("id_external = ?", commentUUID).First(&comment).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Comment not found"})
			return
		}
		e.logger.ErrorContext(reqCtx, "failed to load comment", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load comment"})
		return
	}

	if comment.IssueId != issue.IdIssue {
		c.JSON(http.StatusNotFound, gin.H{"error": "Comment not found"})
		return
	}

	up := model.IssueCommentUpvote{
		IssueCommentId: comment.IdIssueComment,
		UserId:         userId,
	}
	if err := e.db.Create(&up).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			c.JSON(http.StatusConflict, gin.H{"error": "Already upvoted"})
			return
		}
		e.logger.ErrorContext(reqCtx, "failed to create comment upvote", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to upvote comment"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"message": "Comment upvoted successfully"})
}

// [delete] /issue/{id}/comments/{commentId}/upvote
func (e *Endpoint) UndoIssueCommentUpvote(c *gin.Context) {
	reqCtx := c.Request.Context()
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(reqCtx, "unauthorized", "handler", "UndoIssueCommentUpvote")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	issueUUID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid issue ID"})
		return
	}
	commentUUID, err := uuid.Parse(c.Param("commentId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid comment ID"})
		return
	}

	var issue model.Issue
	if err := e.db.Where("id_external = ?", issueUUID).First(&issue).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Issue not found"})
			return
		}
		e.logger.ErrorContext(reqCtx, "failed to load issue", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load issue"})
		return
	}

	if issue.IsResolved {
		c.JSON(http.StatusConflict, gin.H{"error": "Issue is resolved"})
		return
	}

	var comment model.IssueComment
	if err := e.db.Where("id_external = ?", commentUUID).First(&comment).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Comment not found"})
			return
		}
		e.logger.ErrorContext(reqCtx, "failed to load comment", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load comment"})
		return
	}

	if comment.IssueId != issue.IdIssue {
		c.JSON(http.StatusNotFound, gin.H{"error": "Comment not found"})
		return
	}

	res := e.db.Where("id_issue_comment = ? AND id_user = ?", comment.IdIssueComment, userId).Delete(&model.IssueCommentUpvote{})
	if res.Error != nil {
		e.logger.ErrorContext(reqCtx, "failed to remove comment upvote", "error", res.Error)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to remove upvote"})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Upvote not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Comment upvote removed"})
}

// [post] /issue/{id}/upvote
func (e *Endpoint) UpvoteIssue(c *gin.Context) {
	reqCtx := c.Request.Context()
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(reqCtx, "unauthorized", "handler", "UpvoteIssue")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	issueUUID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid issue ID"})
		return
	}

	var issue model.Issue
	if err := e.db.Where("id_external = ?", issueUUID).First(&issue).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Issue not found"})
			return
		}
		e.logger.ErrorContext(reqCtx, "failed to load issue", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load issue"})
		return
	}

	if issue.IsResolved {
		c.JSON(http.StatusConflict, gin.H{"error": "Issue is resolved"})
		return
	}

	up := model.IssueUpvote{
		IssueId: issue.IdIssue,
		UserId:  userId,
	}
	if err := e.db.Create(&up).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			c.JSON(http.StatusConflict, gin.H{"error": "Already upvoted"})
			return
		}
		e.logger.ErrorContext(reqCtx, "failed to create issue upvote", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to upvote issue"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"message": "Issue upvoted successfully"})
}

// [delete] /issue/{id}/upvote
func (e *Endpoint) UndoIssueUpvote(c *gin.Context) {
	reqCtx := c.Request.Context()
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(reqCtx, "unauthorized", "handler", "UndoIssueUpvote")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	issueUUID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid issue ID"})
		return
	}

	var issue model.Issue
	if err := e.db.Where("id_external = ?", issueUUID).First(&issue).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Issue not found"})
			return
		}
		e.logger.ErrorContext(reqCtx, "failed to load issue", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load issue"})
		return
	}

	if issue.IsResolved {
		c.JSON(http.StatusConflict, gin.H{"error": "Issue is resolved"})
		return
	}

	res := e.db.Where("id_issue = ? AND id_user = ?", issue.IdIssue, userId).Delete(&model.IssueUpvote{})
	if res.Error != nil {
		e.logger.ErrorContext(reqCtx, "failed to remove issue upvote", "error", res.Error)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to remove upvote"})
		return
	}
	if res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Upvote not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Issue upvote removed"})
}

// [post] /issue/{id}/comments/{commentId}/comment
func (e *Endpoint) CommentOnIssue(c *gin.Context) {
	// Backward-compatible route. Delegates to CreateIssueComment.
	e.CreateIssueComment(c)
}

// [post] /issue/{id}/comments
func (e *Endpoint) CreateIssueComment(c *gin.Context) {
	reqCtx := c.Request.Context()
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(reqCtx, "unauthorized", "handler", "CreateIssueComment")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	issueUUID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid issue ID"})
		return
	}

	var request CommentOnIssueRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		e.logger.WarnContext(reqCtx, "failed to bind JSON", "handler", "CreateIssueComment", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var issue model.Issue
	if err := e.db.Where("id_external = ?", issueUUID).First(&issue).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Issue not found"})
			return
		}
		e.logger.ErrorContext(reqCtx, "failed to load issue", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load issue"})
		return
	}

	if issue.IsResolved {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Comments are disabled on resolved issues"})
		return
	}

	newComment := model.IssueComment{
		IssueId:     issue.IdIssue,
		UserId:      userId,
		CommentJSON: request.CommentJSON,
		CommentText: request.CommentText,
	}
	if err := e.db.Create(&newComment).Error; err != nil {
		e.logger.ErrorContext(reqCtx, "failed to create comment", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create comment"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"message": "Comment created successfully"})
}

// [post] /issue/{id}/resolve
func (e *Endpoint) MarkIssueAsResolved(c *gin.Context) {
	reqCtx := c.Request.Context()
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(reqCtx, "unauthorized", "handler", "MarkIssueAsResolved")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	currentUser, ok := c.MustGet("currentUser").(model.User)
	if !ok {
		e.logger.ErrorContext(reqCtx, "currentUser missing from context", "handler", "MarkIssueAsResolved")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal error"})
		return
	}

	issueUUID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid issue ID"})
		return
	}

	var issue model.Issue
	if err := e.db.Where("id_external = ?", issueUUID).First(&issue).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "Issue not found"})
			return
		}
		e.logger.ErrorContext(reqCtx, "failed to load issue", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load issue"})
		return
	}

	if issue.UserId != userId && !currentUser.IsAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "Forbidden"})
		return
	}

	if err := e.db.Model(&issue).Update("is_resolved", true).Error; err != nil {
		e.logger.ErrorContext(reqCtx, "failed to resolve issue", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to resolve issue"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"message": "Issue marked as resolved"})
}
