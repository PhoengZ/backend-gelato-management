package router

import (
	"auth-service/internal/auth"
	v1 "auth-service/internal/handler/v1"
	"auth-service/internal/middleware"
	"auth-service/internal/service"
	"time"

	"github.com/gofiber/fiber/v2"
)

type Options struct{ RequestTimeout time.Duration }

func Setup(app *fiber.App, authService service.AuthService, tokens *auth.TokenManager, options ...Options) {
	timeout := 5 * time.Second
	if len(options) > 0 && options[0].RequestTimeout > 0 {
		timeout = options[0].RequestTimeout
	}
	limiter := middleware.NewCredentialLimiter()
	handler := v1.NewAuthHandler(authService, limiter)

	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	authRoutes := app.Group("/api/v1/auth", middleware.PreventCaching, middleware.RequestTimeout(timeout))
	authRoutes.Post("/signup", limiter.Handle, middleware.RequireCredentialJSON, handler.SignUp)
	authRoutes.Post("/login", limiter.Handle, middleware.RequireCredentialJSON, handler.Login)
	authRoutes.Get("/me", middleware.RequireAccessToken(tokens), handler.Me)
}
