package main

import (
	"context"
	"log"
	"log/slog"
	"os"

	"github.com/SomtoJF/iris-api/common"
	"github.com/SomtoJF/iris-api/endpoints/auth"
	"github.com/SomtoJF/iris-api/endpoints/costtracking"
	"github.com/SomtoJF/iris-api/endpoints/health"
	"github.com/SomtoJF/iris-api/endpoints/issue"
	"github.com/SomtoJF/iris-api/endpoints/jobapplication"
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

	r.Use(cors.New(cors.Config{
		AllowOrigins:     allowedOrigins,
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
	issueEndpoint := issue.NewEndpoint(db, logger)

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
		protected.GET("/jobs/:id/user-action", jobEndpoint.GetUserAction)
		protected.GET("/jobs", jobEndpoint.FetchAllJobApplications)
		protected.GET("/jobs/search/history", jobSearchEndpoint.GetJobSearchHistory)
		protected.POST("/jobs/search", jobSearchEndpoint.TriggerJobSearch)

		protected.GET("/realtime/events", realtimeEventsEndpoint.StreamEvents)

		protected.GET("/resumes", resumeEndpoint.FetchResumes)
		protected.POST("/resumes", resumeEndpoint.UploadResume)
		protected.PUT("/resumes/:id/activate", resumeEndpoint.SetResumeAsActive)
		protected.DELETE("/resumes/:id", resumeEndpoint.DeleteResume)
		protected.GET("/resumes/:id/download", resumeEndpoint.GetResumeDownloadUrl)

		protected.GET("/jobapplicationprofile", jobApplicationProfileEndpoint.GetJobApplicationProfile)
		protected.POST("/jobapplicationprofile", jobApplicationProfileEndpoint.UpsertJobApplicationProfile)
		protected.PATCH("/jobapplicationprofile", jobApplicationProfileEndpoint.PatchJobApplicationProfile)

		protected.POST("/issue", issueEndpoint.CreateIssue)
		protected.GET("/issue/:id", issueEndpoint.GetIssue)
		protected.GET("/issue/:id/comments", issueEndpoint.GetIssueComments)
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
