package job

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/SomtoJF/iris-api/model"
	"github.com/SomtoJF/iris-api/temporal"
	"github.com/gin-gonic/gin"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
)

type Endpoint struct {
	db             *gorm.DB
	temporalClient client.Client
	logger         *log.Logger
	taskQueueName  temporal.TaskQueueName
}

func NewEndpoint(db *gorm.DB, temporalClient client.Client, logger *log.Logger, taskQueueName temporal.TaskQueueName) *Endpoint {
	return &Endpoint{db: db, temporalClient: temporalClient, logger: logger, taskQueueName: taskQueueName}
}

type ApplyForJobRequest struct {
	Url string `json:"url" binding:"required"`
}

type JobApplicationWorkflowInput struct {
	Url              string `json:"url"`
	IdJobApplication uint   `json:"id_job_application"`
}

func (e *Endpoint) ApplyForJob(c *gin.Context) {
	var request ApplyForJobRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	jobApplication := model.JobApplication{Url: request.Url, Status: model.JobApplicationStatusPending}
	if err := e.db.Create(&jobApplication).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			c.JSON(http.StatusConflict, gin.H{"error": "Job application already exists"})
			return
		}
		e.logger.Printf("Failed to create job application: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create job application"})
		return
	}

	workflowOptions := client.StartWorkflowOptions{
		ID:                       fmt.Sprintf("job-application-%s-%d", request.Url, time.Now().Unix()),
		TaskQueue:                string(e.taskQueueName),
		WorkflowExecutionTimeout: 40 * time.Minute,
		WorkflowTaskTimeout:      1 * time.Minute,
	}

	_, err := e.temporalClient.ExecuteWorkflow(context.Background(), workflowOptions, "JobApplicationWorkflow", JobApplicationWorkflowInput{Url: request.Url, IdJobApplication: jobApplication.IdJobApplication})
	if err != nil {
		e.logger.Printf("Failed to start job application process: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start job application process"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"message": "Job application initiated"})
}

type FetchAllJobApplicationsRequest struct {
	Page  int `json:"page" binding:"required"`
	Limit int `json:"limit" binding:"required"`
}

type JobApplication struct {
	Id        string                     `json:"id"`
	Url       string                     `json:"url"`
	Status    model.JobApplicationStatus `json:"status"`
	CreatedAt time.Time                  `json:"createdAt"`
	UpdatedAt time.Time                  `json:"updatedAt"`
}

type FetchAllJobApplicationsResponse struct {
	Data  []JobApplication `json:"data"`
	Total int              `json:"total"`
	Page  int              `json:"page"`
	Limit int              `json:"limit"`
}

func (e *Endpoint) FetchAllJobApplications(c *gin.Context) {
	var request FetchAllJobApplicationsRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var jobApplications []model.JobApplication
	if err := e.db.Limit(request.Limit).Offset((request.Page - 1) * request.Limit).Find(&jobApplications).Error; err != nil {
		e.logger.Printf("Failed to fetch job applications: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch job applications"})
		return
	}

	var total int64
	if err := e.db.Model(&model.JobApplication{}).Count(&total).Error; err != nil {
		e.logger.Printf("Failed to fetch total job applications: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch total job applications"})
		return
	}
	applications := make([]JobApplication, len(jobApplications))

	for _, jobApplication := range jobApplications {
		applications = append(applications, JobApplication{
			Id:        jobApplication.IdExternal.String(),
			Url:       jobApplication.Url,
			Status:    jobApplication.Status,
			CreatedAt: jobApplication.CreatedAt,
			UpdatedAt: jobApplication.UpdatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"data": FetchAllJobApplicationsResponse{
		Data:  applications,
		Total: int(total),
		Page:  request.Page,
		Limit: request.Limit,
	}})
}
