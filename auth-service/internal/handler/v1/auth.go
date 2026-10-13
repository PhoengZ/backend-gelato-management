package v1

import (
	"auth-service/internal/audit"
	"auth-service/internal/handler"
	"auth-service/internal/middleware"
	"auth-service/internal/service"
)

type AuthHandler = handler.AuthHandler

func NewAuthHandler(authService service.AuthService, limiter *middleware.CredentialLimiter) *AuthHandler {
	return handler.NewAuthHandler(authService, limiter, nil, nil)
}

func NewAuthHandlerWithAudit(
	authService service.AuthService,
	limiter *middleware.CredentialLimiter,
	rateLimiter *middleware.RateLimiter,
	auditLogger audit.AuditLogger,
) *AuthHandler {
	return handler.NewAuthHandler(authService, limiter, rateLimiter, auditLogger)
}
