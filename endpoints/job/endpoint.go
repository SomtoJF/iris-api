package job

import (
	"context"
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
