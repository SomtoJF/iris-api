package jobsearch

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/SomtoJF/iris-api/temporal"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
)

type Endpoint struct {
	db             *gorm.DB
	temporalClient client.Client
	redis          *redis.Client
	logger         *log.Logger
	taskQueueName  temporal.TaskQueueName
}

func NewEndpoint(db *gorm.DB, temporalClient client.Client, redis *redis.Client, logger *log.Logger, taskQueueName temporal.TaskQueueName) *Endpoint {
	return &Endpoint{db: db, temporalClient: temporalClient, redis: redis, logger: logger, taskQueueName: taskQueueName}
}

const DAILY_SEARCH_LIMIT = 2
const REDIS_JOB_SEARCH_TTL = 24 * time.Hour // 24 hours
const REDIS_JOB_SEARCH_KEY_PREFIX = "job_search:"

// JOB_SEARCH_HISTORY_MAX_ENTRIES caps Redis list length per user (newest-first via LPUSH).
const JOB_SEARCH_HISTORY_MAX_ENTRIES = 100

type JobDiscoveryWorkflowInput struct {
	IdUser      uint   `json:"id_user"`
	SearchQuery string `json:"search_query"`
	Location    string `json:"location"`
	DateCutoff  string `json:"date_cutoff"`
}

type JobDiscoveryWorkflowOutput struct {
	Jobs []DiscoveredJob `json:"jobs"`
}

type DiscoveredJob struct {
	Title       string `json:"title"`
	Url         string `json:"url"`
	CompanyName string `json:"company_name"`
	// DatePosted is the date the job was posted in YYYY-MM-DD format
	DatePosted string `json:"date_posted"`
}

type RedisJobSearchPayload struct {
	IdUser       uint            `json:"id_user"`
	SearchQuery  string          `json:"search_query"`
	Location     string          `json:"location"`
	DateCutoff   string          `json:"date_cutoff"`
	Jobs         []DiscoveredJob `json:"jobs"`
	CurrentUsage int             `json:"current_usage"`
}

type TriggerJobSearchRequest struct {
	SearchQuery string `json:"search_query" binding:"required"`
	Location    string `json:"location" binding:"required"`
	DateCutoff  string `json:"date_cutoff" binding:"required"`
}

// JobSearchHistoryEntry is one recorded search request (no job results).
type JobSearchHistoryEntry struct {
	SearchQuery string    `json:"search_query"`
	Location    string    `json:"location"`
	DateCutoff  string    `json:"date_cutoff"`
	RequestedAt time.Time `json:"requested_at"`
}

func (e *Endpoint) TriggerJobSearch(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var request TriggerJobSearchRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		e.logger.Printf("Failed to bind job search request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cacheKey := jobSearchCacheKey(userId, request.SearchQuery, request.Location, request.DateCutoff)
	if cached, ok := e.tryGetJobSearchFromCache(c.Request.Context(), cacheKey); ok {
		e.appendJobSearchHistory(c.Request.Context(), userId, request)
		c.JSON(http.StatusOK, cached)
		return
	}

	workflowOptions := client.StartWorkflowOptions{
		ID:                       fmt.Sprintf("job-discovery-%d-%s", userId, uuid.New().String()),
		TaskQueue:                string(e.taskQueueName),
		WorkflowExecutionTimeout: 40 * time.Minute,
		WorkflowTaskTimeout:      1 * time.Minute,
	}

	workflowInput := JobDiscoveryWorkflowInput{
		IdUser:      userId,
		SearchQuery: request.SearchQuery,
		Location:    request.Location,
		DateCutoff:  request.DateCutoff,
	}
	workflowRun, err := e.temporalClient.ExecuteWorkflow(c.Request.Context(), workflowOptions, "JobDiscoveryWorkflow", workflowInput)
	if err != nil {
		e.logger.Printf("Failed to start job discovery workflow: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start job search"})
		return
	}

	var output JobDiscoveryWorkflowOutput
	if err := workflowRun.Get(c.Request.Context(), &output); err != nil {
		e.logger.Printf("Job discovery workflow failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Job search failed"})
		return
	}

	e.setJobSearchCache(c.Request.Context(), cacheKey, output)
	e.appendJobSearchHistory(c.Request.Context(), userId, request)

	c.JSON(http.StatusOK, output)
}

func (e *Endpoint) GetJobSearchHistory(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	entries := e.listJobSearchHistory(c.Request.Context(), userId)
	c.JSON(http.StatusOK, gin.H{"data": entries})
}

// tryGetJobSearchFromCache returns (output, true) on a cache hit. On miss, corrupt
// payload, Redis errors, or nil client it returns (zero, false) and logs when appropriate (fail-open).
func (e *Endpoint) tryGetJobSearchFromCache(ctx context.Context, cacheKey string) (JobDiscoveryWorkflowOutput, bool) {
	if e.redis == nil {
		return JobDiscoveryWorkflowOutput{}, false
	}
	val, err := e.redis.Get(ctx, cacheKey).Result()
	if err != nil {
		if err != redis.Nil {
			e.logger.Printf("job search cache: redis GET failed: %v", err)
		}
		return JobDiscoveryWorkflowOutput{}, false
	}
	var out JobDiscoveryWorkflowOutput
	if err := json.Unmarshal([]byte(val), &out); err != nil {
		e.logger.Printf("job search cache: corrupt entry for key %s: %v", cacheKey, err)
		return JobDiscoveryWorkflowOutput{}, false
	}
	return out, true
}

func (e *Endpoint) setJobSearchCache(ctx context.Context, cacheKey string, output JobDiscoveryWorkflowOutput) {
	if e.redis == nil {
		return
	}
	payload, err := json.Marshal(output)
	if err != nil {
		e.logger.Printf("job search cache: marshal output: %v", err)
		return
	}
	if err := e.redis.Set(ctx, cacheKey, payload, REDIS_JOB_SEARCH_TTL).Err(); err != nil {
		e.logger.Printf("job search cache: redis SET failed: %v", err)
	}
}

func jobSearchHistoryKey(userId uint) string {
	return fmt.Sprintf("%shistory:v1:%d", REDIS_JOB_SEARCH_KEY_PREFIX, userId)
}

// appendJobSearchHistory records a successful search request (LPUSH: newest first). Fail-open.
func (e *Endpoint) appendJobSearchHistory(ctx context.Context, userId uint, req TriggerJobSearchRequest) {
	if e.redis == nil {
		return
	}
	entry := JobSearchHistoryEntry{
		SearchQuery: req.SearchQuery,
		Location:    req.Location,
		DateCutoff:  req.DateCutoff,
		RequestedAt: time.Now().UTC(),
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		e.logger.Printf("job search history: marshal entry: %v", err)
		return
	}
	key := jobSearchHistoryKey(userId)
	if err := e.redis.LPush(ctx, key, payload).Err(); err != nil {
		e.logger.Printf("job search history: redis LPUSH failed: %v", err)
		return
	}
	if err := e.redis.LTrim(ctx, key, 0, int64(JOB_SEARCH_HISTORY_MAX_ENTRIES-1)).Err(); err != nil {
		e.logger.Printf("job search history: redis LTRIM failed: %v", err)
	}
	if err := e.redis.Expire(ctx, key, REDIS_JOB_SEARCH_TTL).Err(); err != nil {
		e.logger.Printf("job search history: redis EXPIRE failed: %v", err)
	}
}

// listJobSearchHistory returns history newest-first (Redis LPUSH order). On errors or corrupt elements, logs and fail-open (partial or empty slice).
func (e *Endpoint) listJobSearchHistory(ctx context.Context, userId uint) []JobSearchHistoryEntry {
	out := make([]JobSearchHistoryEntry, 0)
	if e.redis == nil {
		return out
	}
	key := jobSearchHistoryKey(userId)
	raw, err := e.redis.LRange(ctx, key, 0, -1).Result()
	if err != nil {
		e.logger.Printf("job search history: redis LRANGE failed: %v", err)
		return out
	}
	out = make([]JobSearchHistoryEntry, 0, len(raw))
	for i, s := range raw {
		var entry JobSearchHistoryEntry
		if err := json.Unmarshal([]byte(s), &entry); err != nil {
			e.logger.Printf("job search history: corrupt list element at index %d: %v", i, err)
			continue
		}
		out = append(out, entry)
	}
	return out
}

type jobSearchCacheFingerprint struct {
	SearchQuery string `json:"search_query"`
	Location    string `json:"location"`
	DateCutoff  string `json:"date_cutoff"`
}

func jobSearchCacheKey(userId uint, searchQuery, location, dateCutoff string) string {
	fp, err := json.Marshal(jobSearchCacheFingerprint{
		SearchQuery: strings.TrimSpace(searchQuery),
		Location:    strings.TrimSpace(location),
		DateCutoff:  strings.TrimSpace(dateCutoff),
	})
	if err != nil {
		fp = []byte{}
	}
	sum := sha256.Sum256(fp)
	return fmt.Sprintf("%sresult:v1:%d:%x", REDIS_JOB_SEARCH_KEY_PREFIX, userId, sum)
}
