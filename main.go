package main

import (
	"context"
	"log"
	"log/slog"
	"os"
	"strings"

	"github.com/SomtoJF/iris-api/common"
	"github.com/SomtoJF/iris-api/endpoints/auth"
	"github.com/SomtoJF/iris-api/endpoints/costtracking"
	"github.com/SomtoJF/iris-api/endpoints/coverletter"
	"github.com/SomtoJF/iris-api/endpoints/extension"
	"github.com/SomtoJF/iris-api/endpoints/health"
	"github.com/SomtoJF/iris-api/endpoints/issue"
	"github.com/SomtoJF/iris-api/endpoints/jobapplication"
	"github.com/SomtoJF/iris-api/endpoints/jobapplicationdata"
	"github.com/SomtoJF/iris-api/endpoints/jobapplicationprofile"
	"github.com/SomtoJF/iris-api/endpoints/jobsearch"
	realtimeeventsse "github.com/SomtoJF/iris-api/endpoints/realtimeeventssse"
	"github.com/SomtoJF/iris-api/endpoints/resume"
	workflowendpoint "github.com/SomtoJF/iris-api/endpoints/workflow"
	"github.com/SomtoJF/iris-api/initializers/env"
	"github.com/SomtoJF/iris-api/middleware/verifyauth"
	"github.com/SomtoJF/iris-api/temporal"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/posthog/posthog-go"
)

func init() {
	err := env.LoadEnvVariables()
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	dependencies, err := common.MakeDependencies()
	if err != nil {
		log.Fatal(err)
	}
	defer dependencies.Cleanup()

	db := dependencies.GetDB()
	temporalClient := dependencies.GetTemporalClient()
	s3Manager := dependencies.GetS3Manager()
	redisPubSub := dependencies.GetRedisPubSub()
	redisClient := dependencies.GetRedisClient()
	posthogClient := dependencies.GetPosthogClient()

	baseHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})

	logger := slog.New(posthog.NewSlogCaptureHandler(baseHandler, posthogClient,
		posthog.WithDistinctIDFn(func(ctx context.Context, r slog.Record) string {
			if email, ok := verifyauth.UserEmailFromContext(ctx); ok {
				return email
			}
			return "anonymous"
		}),
	))

	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	environment := os.Getenv("ENVIRONMENT")
	var allowedOrigins []string
	if environment == "production" {
		allowedOrigins = []string{"https://app.applywithiris.com"}
	} else {
		allowedOrigins = []string{"http://localhost:5173"}
	}

	// AllowOriginFunc: gin-contrib/cors returns 403 when Origin is set and not
	// allowed. Extension side-panel fetches send Origin: chrome-extension://…;
	// host_permissions only bypass browser CORS checks, not this server reject.
	r.Use(cors.New(cors.Config{
		AllowOriginFunc: func(origin string) bool {
			for _, o := range allowedOrigins {
				if o == origin {
					return true
				}
			}
			return strings.HasPrefix(origin, "chrome-extension://")
		},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowHeaders:     []string{"Content-Type", "Authorization"},
		AllowCredentials: true,
	}))

	defer func() {
		if r := recover(); r != nil {
			log.Printf("Recovered from panic: %v", r)
			dependencies.Cleanup()
			panic(r)
		}
	}()

	authEndpoint := auth.NewEndpoint(db, os.Getenv("CLIENT_DOMAIN"), logger)
	healthEndpoint := health.NewEndpoint()
	jobEndpoint := jobapplication.NewEndpoint(db, temporalClient, logger, temporal.JobApplicationTaskQueueName)
	jobSearchEndpoint := jobsearch.NewEndpoint(db, temporalClient, redisClient, logger, temporal.JobApplicationTaskQueueName)
	realtimeEventsEndpoint := realtimeeventsse.NewEndpoint(redisPubSub, logger)
	resumeEndpoint := resume.NewEndpoint(db, s3Manager, logger, temporalClient, temporal.JobApplicationTaskQueueName)
	jobApplicationProfileEndpoint := jobapplicationprofile.NewEndpoint(db, logger)
	workflowEndpoint := workflowendpoint.NewEndpoint(temporalClient, logger)
	costTrackingEndpoint := costtracking.NewEndpoint(db)
	issueEndpoint := issue.NewEndpoint(db, temporalClient, logger, temporal.JobApplicationTaskQueueName)
	jobApplicationDataEndpoint := jobapplicationdata.NewEndpoint(db, logger)
	coverLetterEndpoint := coverletter.NewEndpoint(db, temporalClient, logger, temporal.JobApplicationTaskQueueName, redisPubSub)
	extensionEndpoint := extension.NewEndpoint(db, temporalClient, logger, temporal.JobApplicationTaskQueueName)

	authMiddleware := verifyauth.NewMiddleware(db)

	public := r.Group("/")
	{
		public.POST("/login", authEndpoint.Login)
		public.POST("/signup", authEndpoint.Signup)

		public.GET("/health", healthEndpoint.HealthCheck)
	}

	protected := r.Group("/")
	protected.Use(authMiddleware.VerifyAuth())
	{
		protected.POST("/logout", authEndpoint.Logout)
		protected.POST("/reset-password", authEndpoint.ResetPassword)
		protected.GET("/me", authEndpoint.GetCurrentUser)

		protected.GET("/cost", costTrackingEndpoint.GetCostTracking)
		protected.GET("/cost/search", costTrackingEndpoint.SearchCostEntities)

		protected.POST("/jobs/apply", jobEndpoint.ApplyForJob)
		protected.POST("/jobs/:id/retry-application", jobEndpoint.RetryApplication)
		protected.POST("/jobs/:id/cancel", jobEndpoint.CancelApplication)
		protected.DELETE("/jobs/:id", jobEndpoint.DeleteApplication)
		protected.PATCH("/jobs/:id", jobEndpoint.PatchJobApplication)
		protected.GET("/jobs/:id/user-action", jobEndpoint.GetUserAction)
		protected.GET("/jobs/:id/comprehensive", jobEndpoint.FetchJobApplicationComprehensive)
		protected.GET("/jobs", jobEndpoint.FetchAllJobApplications)
		protected.GET("/jobs/:id/application-data", jobApplicationDataEndpoint.GetJobApplicationData)
		protected.GET("/jobs/search/history", jobSearchEndpoint.GetJobSearchHistory)
		protected.POST("/jobs/search", jobSearchEndpoint.TriggerJobSearch)

		protected.POST("/extension/initiate", extensionEndpoint.InitiateApplication)
		protected.POST("/extension/application/:id/autofill", extensionEndpoint.AutofillApplication)
		protected.POST("/extension/application/:id/sync-data", extensionEndpoint.SyncApplicationData)
		protected.POST("/extension/application/:id/mark-as-applied", extensionEndpoint.MarkAsApplied) // extension mark-as-applied

		protected.POST("/coverletter", coverLetterEndpoint.CreateCoverLetter)
		protected.POST("/coverletter/regenerate", coverLetterEndpoint.RegenerateCoverLetter)
		protected.GET("/coverletter", coverLetterEndpoint.GetCoverLetters)
		protected.GET("/coverletter/job-application/:jobApplicationId", coverLetterEndpoint.GetCoverLetter)

		protected.GET("/realtime/events", realtimeEventsEndpoint.StreamEvents)

		protected.GET("/resumes", resumeEndpoint.FetchResumes)
		protected.POST("/resumes", resumeEndpoint.UploadResume)
		protected.PUT("/resumes/:id/activate", resumeEndpoint.SetResumeAsActive)
		protected.PUT("/resumes/:id/display-name", resumeEndpoint.UpdateDisplayName)
		protected.DELETE("/resumes/:id", resumeEndpoint.DeleteResume)
		protected.GET("/resumes/:id/download", resumeEndpoint.GetResumeDownloadUrl)

		protected.GET("/jobapplicationprofile", jobApplicationProfileEndpoint.GetJobApplicationProfile)
		protected.POST("/jobapplicationprofile", jobApplicationProfileEndpoint.UpsertJobApplicationProfile)
		protected.PATCH("/jobapplicationprofile", jobApplicationProfileEndpoint.PatchJobApplicationProfile)

		protected.GET("/issues", issueEndpoint.FetchIssues)
		protected.POST("/issue", issueEndpoint.CreateIssue)
		protected.GET("/issue/:id", issueEndpoint.GetIssue)
		protected.GET("/issue/:id/comments", issueEndpoint.GetIssueComments)
		protected.POST("/issue/:id/comments", issueEndpoint.CreateIssueComment)
		protected.POST("/issue/:id/upvote", issueEndpoint.UpvoteIssue)
		protected.DELETE("/issue/:id/upvote", issueEndpoint.UndoIssueUpvote)
		protected.POST("/issue/:id/comments/:commentId/upvote", issueEndpoint.UpvoteIssueComment)
		protected.DELETE("/issue/:id/comments/:commentId/upvote", issueEndpoint.UndoIssueCommentUpvote)
		protected.POST("/issue/:id/comments/:commentId/comment", issueEndpoint.CommentOnIssue)
		protected.POST("/issue/:id/resolve", issueEndpoint.MarkIssueAsResolved)

		protected.POST("/workflows/signal", workflowEndpoint.SendWorkflowSignal)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "4000"
	}

	log.Printf("Starting server on port %s", port)
	if err := r.Run(":" + port); err != nil {
		log.Panicf("error: %s", err)
	}
}
