package main

import (
	"fmt"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-contrib/sessions/redis"
	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	"go-file/common"
	"go-file/model"
	"go-file/router"
	"os"
	"os/signal"
	"strconv"
	"syscall"
)

func main() {
	common.Init()
	common.SetupGinLog()
	common.SysLog(fmt.Sprintf("Go File %s started at port %d", common.Version, *common.Port))
	if os.Getenv("GIN_MODE") != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}
	// Initialize SQL Database
	db, err := model.InitDB()
	if err != nil {
		common.FatalLog(err)
	}
	defer func(db *gorm.DB) {
		err := db.Close()
		if err != nil {
			common.FatalLog("failed to close database: " + err.Error())
		}
	}(db)

	// Initialize Redis
	err = common.InitRedisClient()
	if err != nil {
		common.FatalLog(err)
	}

	// Initialize options
	model.InitOptionMap()

	// Collect visit statistics in the background and keep buffered visits on shutdown
	model.StartStatCollector()
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
		<-quit
		if err := model.FlushStats(); err != nil {
			common.SysError("failed to flush stats: " + err.Error())
		}
		os.Exit(0)
	}()

	// Initialize HTTP server
	server := gin.Default()
	templates, err := common.NewThemeRenderer(common.FS)
	if err != nil {
		common.FatalLog(err)
	}
	server.HTMLRender = templates

	// Initialize session store
	var store sessions.Store
	if common.RedisEnabled {
		opt := common.ParseRedisOption()
		store, _ = redis.NewStore(opt.MinIdleConns, opt.Network, opt.Addr, opt.Password, []byte(common.SessionSecret))
	} else {
		store = cookie.NewStore([]byte(common.SessionSecret))
	}
	store.Options(sessions.Options{
		HttpOnly: true,
	})
	server.Use(sessions.Sessions("session", store))

	router.SetRouter(server)
	var realPort = os.Getenv("PORT")
	if realPort == "" {
		realPort = strconv.Itoa(*common.Port)
	}
	if *common.Host == "" {
		ip := common.GetIp()
		if ip != "" {
			*common.Host = ip
		} else {
			*common.Host = "localhost"
		}
	}
	serverUrl := "http://" + *common.Host + ":" + realPort + "/"
	if !*common.NoBrowser {
		common.OpenBrowser(serverUrl)
	}
	if *common.EnableP2P {
		go common.StartP2PServer()
	}
	err = server.Run(":" + realPort)
	if err != nil {
		common.FatalLog(err)
	}
}
