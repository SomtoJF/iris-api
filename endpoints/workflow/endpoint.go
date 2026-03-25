package workflow

import (
	"context"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.temporal.io/sdk/client"
)

type Endpoint struct {
	temporalClient client.Client
	logger         *log.Logger
}

func NewEndpoint(temporalClient client.Client, logger *log.Logger) *Endpoint {
	return &Endpoint{temporalClient: temporalClient, logger: logger}
}

type SendSignalRequest struct {
	WorkflowID string      `json:"workflow_id" binding:"required"`
	SignalName string      `json:"signal_name" binding:"required"`
	Payload    interface{} `json:"payload"`
}

func (e *Endpoint) SendWorkflowSignal(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var req SendSignalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	err := e.temporalClient.SignalWorkflow(
		context.Background(),
		req.WorkflowID,
		"",
		req.SignalName,
		req.Payload,
	)
	if err != nil {
		e.logger.Printf("Failed to signal workflow %s: %v", req.WorkflowID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to signal workflow"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Signal sent"})
}
