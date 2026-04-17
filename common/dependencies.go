package common

import (
	"os"

	redisInit "github.com/SomtoJF/iris-api/initializers/redis"
	"github.com/SomtoJF/iris-api/initializers/s3"
	"github.com/SomtoJF/iris-api/initializers/sqldb"
	redispubsub "github.com/SomtoJF/iris-api/pkg/redis"
	s3pkg "github.com/SomtoJF/iris-api/pkg/s3"
	"github.com/redis/go-redis/v9"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
)

type Dependencies interface {
	GetDB() *gorm.DB
	GetTemporalClient() client.Client
	GetRedisPubSub() *redispubsub.RedisPubSub
	GetRedisClient() *redis.Client
	GetS3Manager() *s3pkg.S3Manager
	Cleanup()
}

type dependencies struct {
	db             *gorm.DB
	temporalClient client.Client
	redisPubSub    *redispubsub.RedisPubSub
	redisClient    *redis.Client
	s3Manager      *s3pkg.S3Manager
}

func (d *dependencies) GetDB() *gorm.DB {
	return d.db
}

func (d *dependencies) GetTemporalClient() client.Client {
	return d.temporalClient
}

func (d *dependencies) GetRedisPubSub() *redispubsub.RedisPubSub {
	return d.redisPubSub
}

func (d *dependencies) GetRedisClient() *redis.Client {
	return d.redisClient
}

func (d *dependencies) GetS3Manager() *s3pkg.S3Manager {
	return d.s3Manager
}

func (d *dependencies) Cleanup() {
	// Close the Temporal client
	if d.temporalClient != nil {
		d.temporalClient.Close()
	}

	redisInit.CloseRedis()
}

func MakeDependencies() (Dependencies, error) {
	temporalHost := os.Getenv("TEMPORAL_HOST")
	if temporalHost == "" {
		temporalHost = "localhost:7233"
	}

	temporalClient, err := client.Dial(client.Options{
		HostPort: temporalHost,
	})
	if err != nil {
		return nil, err
	}

	db, err := sqldb.ConnectToPostgres()
	if err != nil {
		return nil, err
	}

	err = redisInit.ConnectToRedis()
	if err != nil {
		return nil, err
	}

	rdb := redisInit.RedisClient

	redisPubSub := redispubsub.NewRedisPubSub(rdb)

	s3Client, err := s3.InitializeS3()
	if err != nil {
		return nil, err
	}

	bucket := os.Getenv("AWS_BUCKET")
	s3Manager := s3pkg.NewS3Manager(s3Client, bucket)

	return &dependencies{
		db:             db,
		temporalClient: temporalClient,
		redisPubSub:    redisPubSub,
		redisClient:    rdb,
		s3Manager:      s3Manager,
	}, nil
}
