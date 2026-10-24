package http

import (
	_ "embed"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

//go:embed dashboard.html
var dashboardHTML string

// NewServer initializes an Echo instance configured with standard middleware.
func NewServer() *echo.Echo {
	e := echo.New()
	e.HideBanner = true

	// Middleware
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(middleware.CORS())

	// Health check endpoint
	e.GET("/healthz", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{
			"status": "healthy",
			"engine": "flashlock",
		})
	})

	// Serve Interactive Live Dashboard
	e.GET("/", func(c echo.Context) error {
		return c.HTML(http.StatusOK, dashboardHTML)
	})
	e.GET("/dashboard", func(c echo.Context) error {
		return c.HTML(http.StatusOK, dashboardHTML)
	})

	return e
}
