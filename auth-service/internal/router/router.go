package router

import (
	"time"

	"auth-service/internal/audit"
	"auth-service/internal/auth"
	"auth-service/internal/handler"
	"auth-service/internal/middleware"
	"auth-service/internal/service"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Options struct {
	RequestTimeout time.Duration
	Pool           *pgxpool.Pool
	AuditLogger    audit.AuditLogger
	RateLimiter    *middleware.RateLimiter
}

func Setup(app *fiber.App, authService service.AuthService, tokens *auth.TokenManager, options ...Options) {
	timeout := 5 * time.Second
	var pool *pgxpool.Pool
	var auditLogger audit.AuditLogger
	var rateLimiter *middleware.RateLimiter

	if len(options) > 0 {
		if options[0].RequestTimeout > 0 {
			timeout = options[0].RequestTimeout
		}
		pool = options[0].Pool
		auditLogger = options[0].AuditLogger
		rateLimiter = options[0].RateLimiter
	}

	limiter := middleware.NewCredentialLimiter()
	authHandler := handler.NewAuthHandler(authService, limiter, rateLimiter, auditLogger)
	healthHandler := handler.NewHealthHandler(pool)

	app.Get("/health", healthHandler.Health)

	authRoutes := app.Group("/api/v1/auth", middleware.PreventCaching, middleware.RequestTimeout(timeout))
	authRoutes.Post("/signup", limiter.Handle, middleware.RequireCredentialJSON, authHandler.SignUp)
	authRoutes.Post("/login", limiter.Handle, middleware.RequireCredentialJSON, authHandler.Login)
	authRoutes.Get("/me", middleware.RequireAccessToken(tokens), authHandler.Me)
}
