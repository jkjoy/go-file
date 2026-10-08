package controller

import (
	"github.com/gin-gonic/gin"
	"go-file/common"
	"go-file/model"
	"net/http"
	"strconv"
	"time"
)

// statDays reads the "days" query parameter, limited to the retention window.
func statDays(c *gin.Context) int {
	days, err := strconv.Atoi(c.DefaultQuery("days", "7"))
	if err != nil || days < 1 {
		days = 7
	}
	if days > common.StatRetentionDays {
		days = common.StatRetentionDays
	}
	return days
}

func statResponse(c *gin.Context, data interface{}, err error) {
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": err.Error(),
			"data":    nil,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    data,
	})
}

// Each query flushes buffered visits first so the dashboard shows the latest numbers.
func GetStatSummary(c *gin.Context) {
	if err := model.FlushStats(); err != nil {
		statResponse(c, nil, err)
		return
	}
	summary, err := model.GetStatSummary(statDays(c), time.Now())
	statResponse(c, summary, err)
}

func GetReqs(c *gin.Context) {
	if err := model.FlushStats(); err != nil {
		statResponse(c, nil, err)
		return
	}
	trend, err := model.GetStatTrend(statDays(c), time.Now())
	statResponse(c, trend, err)
}

func GetIPs(c *gin.Context) {
	if err := model.FlushStats(); err != nil {
		statResponse(c, nil, err)
		return
	}
	ips, err := model.GetTopStatIPs(statDays(c), common.StatIPNum, time.Now())
	statResponse(c, ips, err)
}

func GetURLs(c *gin.Context) {
	if err := model.FlushStats(); err != nil {
		statResponse(c, nil, err)
		return
	}
	urls, err := model.GetTopStatURLs(statDays(c), common.StatURLNum, time.Now())
	statResponse(c, urls, err)
}

func ClearStats(c *gin.Context) {
	statResponse(c, nil, model.ClearStats())
}
