package extension

import (
	"log/slog"
	"net/http"

	"github.com/SomtoJF/iris-api/temporal"
	"github.com/gin-gonic/gin"
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
	ResumeId *string `json:"resumeId"`
}

func NewEndpoint(db *gorm.DB, temporalClient client.Client, logger *slog.Logger, taskQueueName temporal.TaskQueueName) *Endpoint {
	return &Endpoint{db: db, temporalClient: temporalClient, logger: logger, taskQueueName: taskQueueName}
}

func (e *Endpoint) InitiateApplication(c *gin.Context) {
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
}
