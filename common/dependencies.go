package common

import (
	redisInit "github.com/SomtoJF/iris-api/initializers/redis"
	"github.com/SomtoJF/iris-api/initializers/s3"
	"github.com/SomtoJF/iris-api/initializers/sqldb"
	redispubsub "github.com/SomtoJF/iris-api/pkg/redis"
	s3Pkg "github.com/aws/aws-sdk-go-v2/service/s3"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
)

type Dependencies interface {
	GetDB() *gorm.DB
	GetTemporalClient() client.Client
	GetRedisPubSub() *redispubsub.RedisPubSub
	GetS3Client() *s3Pkg.Client
	Cleanup()
}

type dependencies struct {
	db             *gorm.DB
	temporalClient client.Client
	redisPubSub    *redispubsub.RedisPubSub
	s3Client       *s3Pkg.Client
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

func (d *dependencies) GetS3Client() *s3Pkg.Client {
	return d.s3Client
}

func (d *dependencies) Cleanup() {
	// Close the Temporal client
	if d.temporalClient != nil {
		d.temporalClient.Close()
	}

	redisInit.CloseRedis()
}

func MakeDependencies() (Dependencies, error) {
	temporalClient, err := client.Dial(client.Options{})
	if err != nil {
		return nil, err
	}

	err = sqldb.ConnectToSQLite()
	if err != nil {
		return nil, err
	}

	db := sqldb.DB

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

	return &dependencies{
		db:             db,
		temporalClient: temporalClient,
		redisPubSub:    redisPubSub,
		s3Client:       s3Client,
	}, nil
}
