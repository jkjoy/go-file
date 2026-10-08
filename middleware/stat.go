package middleware

import (
	"github.com/gin-gonic/gin"
	"go-file/common"
	"go-file/model"
	"net/http"
	"strings"
	"time"
)

// statSkipped reports requests that should not be counted at all:
// embedded assets and the dashboard's own statistics API.
func statSkipped(path string) bool {
	return strings.HasPrefix(path, "/public/") ||
		path == "/api/stat" || strings.HasPrefix(path, "/api/stat/") ||
		path == "/favicon.ico"
}

func AllStat() func(c *gin.Context) {
	return func(c *gin.Context) {
		c.Next()
		if !common.StatEnabled || statSkipped(c.Request.URL.Path) {
			return
		}
		url := ""
		// Unknown paths (scanners, typos) still count as traffic but are kept
		// out of the URL ranking so they cannot flood it.
		if c.Writer.Status() < http.StatusBadRequest && c.FullPath() != "" {
			url = c.Request.URL.RequestURI()
		}
		model.RecordVisit(c.ClientIP(), url, time.Now())
	}
}
