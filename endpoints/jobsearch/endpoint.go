package jobsearch

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/SomtoJF/iris-api/model"
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
	logger         *slog.Logger
	taskQueueName  temporal.TaskQueueName
}

func NewEndpoint(db *gorm.DB, temporalClient client.Client, redis *redis.Client, logger *slog.Logger, taskQueueName temporal.TaskQueueName) *Endpoint {
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

// triggerJobSearchJSON is the HTTP request body (camelCase JSON).
type triggerJobSearchRequest struct {
	SearchQuery string  `json:"searchQuery" binding:"required"`
	Location    string  `json:"location" binding:"required"`
	DateCutoff  *string `json:"dateCutoff"`
}

// jobSearchResponseJSON is the HTTP response for POST /jobs/search (camelCase JSON).
type jobSearchResponse struct {
	Jobs []discoveredJobJSON `json:"jobs"`
}

type discoveredJobJSON struct {
	Title       string `json:"title"`
	Url         string `json:"url"`
	CompanyName string `json:"companyName"`
	DatePosted  string `json:"datePosted"`
	Applied     bool   `json:"applied"`
}

// JobSearchHistoryEntryJSON is one history row for API and Redis list v2 (camelCase JSON).
type JobSearchHistoryEntryJSON struct {
	SearchQuery string    `json:"searchQuery"`
	Location    string    `json:"location"`
	DateCutoff  string    `json:"dateCutoff"`
	RequestedAt time.Time `json:"requestedAt"`
}

func jobDiscoveryOutputToResponse(out JobDiscoveryWorkflowOutput, appliedUrls map[string]bool) jobSearchResponse {
	jobs := make([]discoveredJobJSON, 0, len(out.Jobs))
	for _, j := range out.Jobs {
		jobs = append(jobs, discoveredJobJSON{
			Title:       j.Title,
			Url:         j.Url,
			CompanyName: j.CompanyName,
			DatePosted:  j.DatePosted,
			Applied:     appliedUrls[j.Url],
		})
	}
	return jobSearchResponse{Jobs: jobs}
}

func (e *Endpoint) getAppliedUrls(ctx context.Context, userId uint, urls []string) map[string]bool {
	if len(urls) == 0 {
		return nil
	}
	var appliedUrls []string
	if err := e.db.Model(&model.JobApplication{}).Where("id_user = ? AND url IN ?", userId, urls).Pluck("url", &appliedUrls).Error; err != nil {
		e.logger.ErrorContext(ctx, "failed to query applied urls", "error", err)
		return nil
	}
	m := make(map[string]bool, len(appliedUrls))
	for _, u := range appliedUrls {
		m[u] = true
	}
	return m
}

func extractUrls(jobs []DiscoveredJob) []string {
	urls := make([]string, 0, len(jobs))
	for _, j := range jobs {
		urls = append(urls, j.Url)
	}
	return urls
}

func (e *Endpoint) TriggerJobSearch(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(c.Request.Context(), "unauthorized", "handler", "TriggerJobSearch")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	var body triggerJobSearchRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		e.logger.WarnContext(c.Request.Context(), "failed to bind JSON", "handler", "TriggerJobSearch", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	searchQuery := body.SearchQuery
	location := body.Location
	dateCutoff := ""
	if body.DateCutoff != nil {
		dateCutoff = *body.DateCutoff
	}

	cacheKey := jobSearchCacheKey(userId, searchQuery, location, dateCutoff)
	if cached, ok := e.tryGetJobSearchFromCache(c.Request.Context(), cacheKey); ok {
		appliedUrls := e.getAppliedUrls(c.Request.Context(), userId, extractUrls(cached.Jobs))
		c.JSON(http.StatusOK, jobDiscoveryOutputToResponse(cached, appliedUrls))
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
		SearchQuery: searchQuery,
		Location:    location,
		DateCutoff:  dateCutoff,
	}
	workflowRun, err := e.temporalClient.ExecuteWorkflow(c.Request.Context(), workflowOptions, "JobDiscoveryWorkflow", workflowInput)
	if err != nil {
		e.logger.ErrorContext(c.Request.Context(), "failed to start job discovery workflow", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start job search"})
		return
	}

	var output JobDiscoveryWorkflowOutput
	if err := workflowRun.Get(c.Request.Context(), &output); err != nil {
		e.logger.ErrorContext(c.Request.Context(), "job discovery workflow failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Job search failed"})
		return
	}

	e.setJobSearchCache(c.Request.Context(), cacheKey, output)
	e.appendJobSearchHistory(c.Request.Context(), userId, searchQuery, location, dateCutoff)

	appliedUrls := e.getAppliedUrls(c.Request.Context(), userId, extractUrls(output.Jobs))
	c.JSON(http.StatusOK, jobDiscoveryOutputToResponse(output, appliedUrls))
}

func (e *Endpoint) GetJobSearchHistory(c *gin.Context) {
	userId := c.GetUint("userId")
	if userId == 0 {
		e.logger.InfoContext(c.Request.Context(), "unauthorized", "handler", "GetJobSearchHistory")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	entries := e.listJobSearchHistory(c.Request.Context(), userId)
	c.JSON(http.StatusOK, gin.H{"data": entries}) // []JobSearchHistoryEntryJSON
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
			e.logger.ErrorContext(ctx, "job search cache redis GET failed", "error", err, "cache_key", cacheKey)
		}
		return JobDiscoveryWorkflowOutput{}, false
	}
	var out JobDiscoveryWorkflowOutput
	if err := json.Unmarshal([]byte(val), &out); err != nil {
		e.logger.ErrorContext(ctx, "job search cache corrupt entry", "cache_key", cacheKey, "error", err)
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
		e.logger.ErrorContext(ctx, "job search cache marshal failed", "error", err)
		return
	}
	if err := e.redis.Set(ctx, cacheKey, payload, REDIS_JOB_SEARCH_TTL).Err(); err != nil {
		e.logger.ErrorContext(ctx, "job search cache redis SET failed", "error", err, "cache_key", cacheKey)
	}
}

func jobSearchHistoryKey(userId uint) string {
	return fmt.Sprintf("%shistory:v2:%d", REDIS_JOB_SEARCH_KEY_PREFIX, userId)
}

// appendJobSearchHistory records a successful search request (LPUSH: newest first). Fail-open.
func (e *Endpoint) appendJobSearchHistory(ctx context.Context, userId uint, searchQuery, location, dateCutoff string) {
	if e.redis == nil {
		return
	}
	entry := JobSearchHistoryEntryJSON{
		SearchQuery: searchQuery,
		Location:    location,
		DateCutoff:  dateCutoff,
		RequestedAt: time.Now().UTC(),
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		e.logger.ErrorContext(ctx, "job search history marshal failed", "error", err)
		return
	}
	key := jobSearchHistoryKey(userId)
	if err := e.redis.LPush(ctx, key, payload).Err(); err != nil {
		e.logger.ErrorContext(ctx, "job search history redis LPUSH failed", "error", err)
		return
	}
	if err := e.redis.LTrim(ctx, key, 0, int64(JOB_SEARCH_HISTORY_MAX_ENTRIES-1)).Err(); err != nil {
		e.logger.ErrorContext(ctx, "job search history redis LTRIM failed", "error", err)
	}
	if err := e.redis.Expire(ctx, key, REDIS_JOB_SEARCH_TTL).Err(); err != nil {
		e.logger.ErrorContext(ctx, "job search history redis EXPIRE failed", "error", err)
	}
}

// listJobSearchHistory returns history newest-first (Redis LPUSH order). On errors or corrupt elements, logs and fail-open (partial or empty slice).
func (e *Endpoint) listJobSearchHistory(ctx context.Context, userId uint) []JobSearchHistoryEntryJSON {
	out := make([]JobSearchHistoryEntryJSON, 0)
	if e.redis == nil {
		return out
	}
	key := jobSearchHistoryKey(userId)
	raw, err := e.redis.LRange(ctx, key, 0, -1).Result()
	if err != nil {
		e.logger.ErrorContext(ctx, "job search history redis LRANGE failed", "error", err)
		return out
	}
	out = make([]JobSearchHistoryEntryJSON, 0, len(raw))
	for i, s := range raw {
		var entry JobSearchHistoryEntryJSON
		if err := json.Unmarshal([]byte(s), &entry); err != nil {
			e.logger.ErrorContext(ctx, "job search history corrupt list element", "index", i, "error", err)
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
