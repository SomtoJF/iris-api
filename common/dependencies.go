package common

import (
	"github.com/SomtoJF/iris-api/initializers/sqldb"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
)

type Dependencies interface {
	GetDB() *gorm.DB
	GetTemporalClient() client.Client
	Cleanup()
}

type dependencies struct {
	db             *gorm.DB
	temporalClient client.Client
}

func (d *dependencies) GetDB() *gorm.DB {
	return d.db
}

func (d *dependencies) GetTemporalClient() client.Client {
	return d.temporalClient
}

func (d *dependencies) Cleanup() {
	// Close the Temporal client
	if d.temporalClient != nil {
		d.temporalClient.Close()
	}

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

	return &dependencies{
		db:             db,
		temporalClient: temporalClient,
	}, nil
}
