package main

import (
	"log"
	"os"

	"github.com/SomtoJF/iris-api/common"
	"github.com/SomtoJF/iris-api/endpoints/job"
	realtimeeventsse "github.com/SomtoJF/iris-api/endpoints/realtimeeventssse"
	"github.com/SomtoJF/iris-api/initializers/sqldb"
	"github.com/SomtoJF/iris-api/temporal"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func init() {
	err := sqldb.ConnectToSQLite()
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
	logger := log.Default()

	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	r.Use(cors.New(cors.Config{
		AllowOrigins: []string{"http://localhost:5173"},
		AllowMethods: []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders: []string{"Content-Type", "Authorization"},
	}))

	defer func() {
		if r := recover(); r != nil {
			log.Printf("Recovered from panic: %v", r)
			dependencies.Cleanup()
			panic(r)
		}
	}()

	jobEndpoint := job.NewEndpoint(db, temporalClient, logger, temporal.JobApplicationTaskQueueName)
	realtimeEventsEndpoint := realtimeeventsse.NewEndpoint(dependencies.GetRedisPubSub(), logger)

	r.POST("/jobs/apply", jobEndpoint.ApplyForJob)
	r.GET("/jobs", jobEndpoint.FetchAllJobApplications)
	r.GET("/realtime/events", realtimeEventsEndpoint.StreamEvents)

	port := os.Getenv("PORT")
	if port == "" {
		port = "4000"
	}

	log.Printf("Starting server on port %s", port)
	if err := r.Run(":" + port); err != nil {
		log.Panicf("error: %s", err)
	}
}
