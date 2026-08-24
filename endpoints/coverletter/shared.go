package coverletter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SomtoJF/iris-api/model"
	redispubsub "github.com/SomtoJF/iris-api/pkg/redis"
	"github.com/google/uuid"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
)

const coverLetterTimeout = 10 * time.Minute

// coverLetterWorkflowInput mirrors the worker's coverletter.CoverLetterWorkflowInput.
type coverLetterWorkflowInput struct {
	IdCoverLetter    uint              `json:"id_cover_letter"`
	IdJobApplication *uint             `json:"id_job_application,omitempty"`
	IdUser           uint              `json:"id_user"`
	WorkflowID       *string           `json:"workflow_id,omitempty"`
	ElementIndex     *int              `json:"element_index,omitempty"`
	EditInstructions *EditInstructions `json:"edit_instructions,omitempty"`
	// UltraWrite only applies in edit mode (EditInstructions set): true runs the
	// full analysis write instead of the lightweight edit.
	UltraWrite bool `json:"ultra_write,omitempty"`
}

type EditInstructions struct {
	Instructions string `json:"instructions"`
}

func NewCoverLetterWorkflowID() string {
	return fmt.Sprintf("cover-letter-%s", uuid.New().String())
}

func (e *Endpoint) runCoverLetterWorkflow(ctx context.Context, workflowId string, userId uint, coverLetter model.CoverLetter, edit *EditInstructions, ultraWrite bool) (string, error) {
	options := client.StartWorkflowOptions{
		ID:                       workflowId,
		TaskQueue:                string(e.taskQueueName),
		WorkflowExecutionTimeout: coverLetterTimeout,
		WorkflowTaskTimeout:      1 * time.Minute,
	}

	workflowInput := coverLetterWorkflowInput{
		IdCoverLetter:    coverLetter.IdCoverLetter,
		IdJobApplication: coverLetter.JobApplicationId,
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

	body, _ := result["cover_letter"].(string)
	if body == "" {
		return "", errors.New("cover letter workflow returned empty result")
	}
	return body, nil
}

// GenerateCoverLetterAsync runs the cover letter workflow in the background so the
// request handler can return immediately. On completion it persists the result and
// publishes a realtime event (ready/failed) to the user. coverLetter is taken by
// value so the caller can return safely once the goroutine is spawned.
func (e *Endpoint) GenerateCoverLetterAsync(userId uint, coverLetter model.CoverLetter, workflowId string, edit *EditInstructions, ultraWrite bool) {
	go func() {
		// The request context is cancelled once the handler returns, so use a fresh one.
		ctx := context.Background()

		body, err := e.runCoverLetterWorkflow(ctx, workflowId, userId, coverLetter, edit, ultraWrite)
		if err != nil {
			e.logger.Error("cover letter workflow failed", "error", err)
			e.failCoverLetter(ctx, &coverLetter, userId, err)
			return
		}

		if err := e.persistCoverLetter(&coverLetter, body); err != nil {
			e.logger.Error("failed to persist cover letter", "error", err)
			e.failCoverLetter(ctx, &coverLetter, userId, err)
			return
		}

		e.publishCoverLetterEvent(ctx, userId, coverLetter.IdExternal.String(), redispubsub.EventCoverLetterReady, model.CoverLetterStatusReady)
	}()
}

func (e *Endpoint) failCoverLetter(ctx context.Context, coverLetter *model.CoverLetter, userId uint, cause error) {
	e.markCoverLetterFailed(coverLetter, cause)
	e.publishCoverLetterEvent(ctx, userId, coverLetter.IdExternal.String(), redispubsub.EventCoverLetterFailed, model.CoverLetterStatusFailed)
}

func (e *Endpoint) publishCoverLetterEvent(ctx context.Context, userId uint, coverLetterId string, event redispubsub.EventType, status model.CoverLetterStatus) {
	data := map[string]any{"coverLetterId": coverLetterId, "status": status}
	if err := e.redisPubSub.PublishToUser(ctx, fmt.Sprintf("%d", userId), event, data); err != nil {
		e.logger.Error("failed to publish cover letter event", "error", err)
	}
}

func (e *Endpoint) persistCoverLetter(coverLetter *model.CoverLetter, body string) error {
	return e.db.Model(coverLetter).Updates(map[string]any{
		"body":   &body,
		"status": model.CoverLetterStatusReady,
	}).Error
}

func (e *Endpoint) markCoverLetterFailed(coverLetter *model.CoverLetter, cause error) {
	reason := cause.Error()
	if err := e.db.Model(coverLetter).Updates(map[string]any{
		"status":         model.CoverLetterStatusFailed,
		"failure_reason": &reason,
	}).Error; err != nil {
		e.logger.Error("failed to mark cover letter as failed", "error", err)
	}
}

// UpsertAttachedCoverLetter creates or updates the cover letter row for a job application.
func UpsertAttachedCoverLetter(db *gorm.DB, jobApp model.JobApplication, resumeId uint, status model.CoverLetterStatus, workflowId *string, body *string) (model.CoverLetter, error) {
	jobAppID := jobApp.IdJobApplication

	var coverLetter model.CoverLetter
	err := db.Where("id_job_application = ?", jobApp.IdJobApplication).First(&coverLetter).Error
	switch {
	case err == nil:
		updates := map[string]any{
			"id_resume":       resumeId,
			"job_title":       jobApp.JobTitle,
			"company_name":    jobApp.CompanyName,
			"job_description": jobApp.JobDescription,
			"url":             jobApp.Url,
			"status":          status,
			"workflow_id":     workflowId,
		}
		if body != nil {
			updates["body"] = body
		}
		if err := db.Model(&coverLetter).Updates(updates).Error; err != nil {
			return model.CoverLetter{}, err
		}
		coverLetter.ResumeId = resumeId
		coverLetter.JobTitle = jobApp.JobTitle
		coverLetter.CompanyName = jobApp.CompanyName
		coverLetter.JobDescription = jobApp.JobDescription
		coverLetter.Url = jobApp.Url
		coverLetter.Status = status
		coverLetter.WorkflowID = workflowId
		if body != nil {
			coverLetter.Body = body
		}
		return coverLetter, nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		coverLetter = model.CoverLetter{
			UserId:           jobApp.UserId,
			ResumeId:         resumeId,
			JobApplicationId: &jobAppID,
			JobTitle:         jobApp.JobTitle,
			CompanyName:      jobApp.CompanyName,
			JobDescription:   jobApp.JobDescription,
			Url:              jobApp.Url,
			Status:           status,
			WorkflowID:       workflowId,
			Body:             body,
		}
		if err := db.Create(&coverLetter).Error; err != nil {
			return model.CoverLetter{}, err
		}
		return coverLetter, nil
	default:
		return model.CoverLetter{}, err
	}
}
