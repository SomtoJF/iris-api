package main

import (
	"log"

	"github.com/SomtoJF/iris-api/initializers/sqldb"
	"github.com/gin-gonic/gin"
)

func init() {
	err := sqldb.ConnectToSQLite()
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	r := gin.Default()

	r.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"message": "pong",
		})
	})

	r.Run()
}
