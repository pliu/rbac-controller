package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func GetServer() *gin.Engine {
	r := gin.Default()
	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "pong",
		})
	})
	return r
	// r.Run() // listen and serve on 0.0.0.0:8080 (for windows "localhost:8080")
}
