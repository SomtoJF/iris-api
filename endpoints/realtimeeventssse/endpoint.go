package realtimeeventsse

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	redispubsub "github.com/SomtoJF/iris-api/pkg/redis"

	"github.com/gin-gonic/gin"
)

type Endpoint struct {
	redisPubSub *redispubsub.RedisPubSub
	logger      *slog.Logger
}

func NewEndpoint(redisPubSub *redispubsub.RedisPubSub, logger *slog.Logger) *Endpoint {
	return &Endpoint{
		redisPubSub: redisPubSub,
		logger:      logger,
	}
}

// StreamEvents handles SSE connections for real-time events
// @Summary Stream real-time events
// @Description Establishes an SSE connection to receive real-time events for the authenticated user
// @Tags realtime
// @Accept  json
// @Produce text/plain
// @Security BearerAuth
// @Success 200 {string} string "SSE stream"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 500 {object} map[string]interface{} "Internal server error"
// @Router /realtime/events [get]
func (e *Endpoint) StreamEvents(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.Info("unauthorized", "handler", "StreamEvents")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Set SSE headers
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	// Note: CORS headers are handled by global middleware

	// Create context with timeout
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	userIDStr := fmt.Sprintf("%d", userId)

	// Subscribe to user's Redis channel
	eventChan, err := e.redisPubSub.SubscribeToUser(ctx, userIDStr)
	if err != nil {
		e.logger.Error("failed to subscribe to redis pubsub", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to subscribe to events"})
		return
	}

	e.logger.Info("sse client connected", "user_id", userId)

	// Send initial connection message
	initialEvent := fmt.Sprintf("data: {\"action\":\"SYSTEM_MESSAGE\",\"data\":{\"message\":\"Connected to real-time events\",\"timestamp\":\"%s\"}}\n\n", time.Now().Format(time.RFC3339))
	c.Writer.WriteString(initialEvent)
	c.Writer.Flush()

	// Set up heartbeat ticker
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	// Stream events
	for {
		select {
		case event, ok := <-eventChan:
			if !ok {
				e.logger.Info("sse event channel closed", "user_id", userId)
				return
			}

			// Format the event as SSE
			sseData := fmt.Sprintf("data: {\"action\":\"%s\",\"data\":%s}\n\n", event.Action, e.jsonStringify(event.Data))

			// Write to client
			_, err := c.Writer.WriteString(sseData)
			if err != nil {
				e.logger.Warn("failed to write sse to client", "error", err)
				return
			}
			c.Writer.Flush()

		case <-heartbeat.C:
			// Send heartbeat
			heartbeatEvent := fmt.Sprintf("data: {\"action\":\"HEARTBEAT\",\"data\":{\"timestamp\":\"%s\"}}\n\n", time.Now().Format(time.RFC3339))
			_, err := c.Writer.WriteString(heartbeatEvent)
			if err != nil {
				e.logger.Warn("failed to send sse heartbeat", "error", err)
				return
			}
			c.Writer.Flush()

		case <-ctx.Done():
			e.logger.Info("sse client disconnected", "user_id", userId)
			return
		}
	}
}

func (e *Endpoint) jsonStringify(data interface{}) string {
	if data == nil {
		return "null"
	}

	jsonBytes, err := json.Marshal(data)
	if err != nil {
		e.logger.Error("failed to marshal sse event data", "error", err)
		return "null"
	}

	return string(jsonBytes)
}

// TriggerTestEvent handles the endpoint for triggering a test event
// @Summary Trigger a test event
// @Description Triggers a test event for the authenticated user
// @Tags realtime
// @Accept  json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} map[string]interface{} "Test event sent successfully"
// @Failure 401 {object} map[string]interface{} "Unauthorized"
// @Failure 400 {object} map[string]interface{} "Bad request"
// @Failure 500 {object} map[string]interface{} "Internal server error"
// @Router /realtime/test [post]
func (e *Endpoint) TriggerTestEvent(c *gin.Context) {
	userID := c.GetUint("userId")
	if userID == 0 {
		e.logger.Info("unauthorized", "handler", "TriggerTestEvent")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	userIDStr := fmt.Sprintf("%d", userID)

	// Parse request body
	var requestData map[string]interface{}
	if err := c.ShouldBindJSON(&requestData); err != nil {
		e.logger.Warn("failed to bind JSON", "handler", "TriggerTestEvent", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	// Add timestamp to the test data
	testData := map[string]interface{}{
		"message":   requestData,
		"timestamp": time.Now().Format(time.RFC3339),
		"userID":    userIDStr,
	}

	// Send test event
	err := e.redisPubSub.PublishToUser(c.Request.Context(), userIDStr, redispubsub.EventUserActionRequired, testData)
	if err != nil {
		e.logger.Error("failed to publish test event", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to send test event"})
		return
	}

	e.logger.Info("test event sent", "user_id", userID)
	c.JSON(http.StatusOK, gin.H{
		"message": "Test event sent successfully",
		"data":    testData,
	})
}
