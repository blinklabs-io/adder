package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	_ "github.com/blinklabs-io/adder/docs"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"     // swagger embed files
	ginSwagger "github.com/swaggo/gin-swagger" // gin-swagger middleware
)

// HealthChecker is an interface for components that can report their health status
type HealthChecker interface {
	IsRunning() bool
}

type API interface {
	Start() error
	AddRoute(method, path string, handler gin.HandlerFunc)
}

type APIv1 struct {
	engine   *gin.Engine
	ApiGroup *gin.RouterGroup
	Host     string
	Port     uint
}

type APIRouteRegistrar interface {
	RegisterRoutes()
}

type APIOption func(*APIv1)

func WithGroup(group string) APIOption {
	// Expects '/v1' as the group
	return func(a *APIv1) {
		a.ApiGroup = a.engine.Group(group)
	}
}

func WithHost(host string) APIOption {
	return func(a *APIv1) {
		a.Host = host
	}
}

func WithPort(port uint) APIOption {
	return func(a *APIv1) {
		a.Port = port
	}
}

// Initialize singleton API instance.
var apiInstance = &APIv1{
	engine: ConfigureRouter(false),
	Host:   "0.0.0.0",
	Port:   8080,
}

var once sync.Once

// healthCheckers holds registered health checkers
var (
	healthCheckers   []HealthChecker
	healthCheckersMu sync.RWMutex
)

// RegisterHealthChecker adds a health checker to be queried during health checks
func RegisterHealthChecker(hc HealthChecker) {
	healthCheckersMu.Lock()
	defer healthCheckersMu.Unlock()
	healthCheckers = append(healthCheckers, hc)
}

// ResetHealthCheckers clears all registered health checkers.
// This is intended for use in tests to prevent state leakage between tests.
func ResetHealthCheckers() {
	healthCheckersMu.Lock()
	defer healthCheckersMu.Unlock()
	healthCheckers = nil
}

func New(debug bool, options ...APIOption) *APIv1 {
	once.Do(func() {
		apiInstance = &APIv1{
			engine: ConfigureRouter(debug),
			Host:   "0.0.0.0",
			Port:   8080,
		}
		for _, opt := range options {
			opt(apiInstance)
		}
	})

	return apiInstance
}

func GetInstance() *APIv1 {
	return apiInstance
}

func (a *APIv1) Engine() *gin.Engine {
	return a.engine
}

//	@title			Adder API
//	@version		v1
//	@description	Adder API
//	@Schemes		http
//	@BasePath		/v1

//	@contact.name	Blink Labs
//	@contact.url	https://blinklabs.io
//	@contact.email	support@blinklabs.io

// @license.name	Apache 2.0
// @license.url	http://www.apache.org/licenses/LICENSE-2.0.html
func (a *APIv1) Start() error {
	address := fmt.Sprintf("%s:%d", a.Host, a.Port)
	// Use buffered channel to not block goroutine
	errChan := make(chan error, 1)

	go func() {
		// Capture the error returned by Run
		errChan <- a.engine.Run(address)
	}()

	select {
	case err := <-errChan:
		return err
	default:
		// No starting errors, start server
	}

	return nil
}

func (a *APIv1) AddRoute(method, path string, handler gin.HandlerFunc) {
	// Inner function to add routes to a given target
	// (either gin.Engine or gin.RouterGroup)
	addRouteToTarget := func(target gin.IRoutes) {
		switch method {
		case "GET":
			target.GET(path, handler)
		case "POST":
			target.POST(path, handler)
		case "PUT":
			target.PUT(path, handler)
		case "DELETE":
			target.DELETE(path, handler)
		case "PATCH":
			target.PATCH(path, handler)
		case "HEAD":
			target.HEAD(path, handler)
		case "OPTIONS":
			target.OPTIONS(path, handler)
		default:
			log.Printf("Unsupported HTTP method: %s", method)
		}
	}

	// Check if a specific apiGroup is set
	// If so, add the route to it. Otherwise, add to the main engine.
	if a.ApiGroup != nil {
		addRouteToTarget(a.ApiGroup)
	} else {
		addRouteToTarget(a.engine)
	}
}

func ConfigureRouter(debug bool) *gin.Engine {
	if !debug {
		gin.SetMode(gin.ReleaseMode)
	}

	gin.DisableConsoleColor()
	g := gin.New()
	g.Use(gin.Recovery())
	// Custom access logging
	g.Use(gin.LoggerWithFormatter(accessLogger))
	// Healthcheck endpoint
	g.GET("/healthcheck", handleHealthcheck)
	// No-op API endpoint for testing
	g.GET("/ping", func(c *gin.Context) {
		c.String(200, "pong")
	})
	// Swagger UI
	g.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	return g
}

func accessLogger(param gin.LogFormatterParams) string {
	logEntry := gin.H{
		"type":          "access",
		"client_ip":     param.ClientIP,
		"timestamp":     param.TimeStamp.Format(time.RFC1123),
		"method":        param.Method,
		"path":          param.Path,
		"proto":         param.Request.Proto,
		"status_code":   param.StatusCode,
		"latency":       param.Latency,
		"user_agent":    param.Request.UserAgent(),
		"error_message": param.ErrorMessage,
	}

	ret, err := json.Marshal(logEntry)
	if err != nil {
		return ""
	}

	return string(ret) + "\n"
}

func handleHealthcheck(c *gin.Context) {
	healthCheckersMu.RLock()
	// Make a copy of the slice to avoid races with concurrent RegisterHealthChecker calls
	checkers := make([]HealthChecker, len(healthCheckers))
	copy(checkers, healthCheckers)
	healthCheckersMu.RUnlock()

	// If no health checkers are registered, report as healthy
	if len(checkers) == 0 {
		c.JSON(http.StatusOK, gin.H{"failed": false})
		return
	}

	// Check all registered health checkers
	for _, hc := range checkers {
		if !hc.IsRunning() {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"failed": true,
				"reason": "pipeline is not running",
			})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"failed": false})
}
