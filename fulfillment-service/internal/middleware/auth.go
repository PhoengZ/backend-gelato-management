package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type Role string

const (
	RoleCustomer Role = "CUSTOMER"
	RoleStaff    Role = "STAFF"
	RoleManager  Role = "MANAGER"
)

func (r Role) Valid() bool {
	return r == RoleCustomer || r == RoleStaff || r == RoleManager
}

type Claims struct {
	Role Role `json:"role"`
	jwt.RegisteredClaims
}

var (
	ErrInvalidToken = errors.New("invalid access token")
	ErrExpiredToken = errors.New("access token is expired")
)

const (
	ContextUserID   = "authenticated_user_id"
	ContextUserRole = "authenticated_user_role"
)

type Verifier struct {
	secret   []byte
	issuer   string
	audience string
}

func NewVerifier(secret, issuer, audience string) *Verifier {
	return &Verifier{
		secret:   []byte(secret),
		issuer:   issuer,
		audience: audience,
	}
}

func (v *Verifier) Parse(raw string) (*Claims, error) {
	if len(v.secret) == 0 {
		return nil, ErrInvalidToken
	}

	claims := &Claims{}
	token, err := jwt.ParseWithClaims(
		raw,
		claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, ErrInvalidToken
			}
			return v.secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
	)
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	if claims.IssuedAt == nil || claims.ExpiresAt == nil ||
		!claims.ExpiresAt.Time.After(claims.IssuedAt.Time) {
		return nil, ErrInvalidToken
	}
	userID, err := uuid.Parse(claims.Subject)
	if err != nil || userID == uuid.Nil || !claims.Role.Valid() {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

type AuthMiddleware struct {
	verifier *Verifier
}

func NewAuthMiddleware(verifier *Verifier) *AuthMiddleware {
	return &AuthMiddleware{verifier: verifier}
}

type authErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func failUnauthorized(c echo.Context, message string) error {
	if message == "" {
		message = "Authentication is required"
	}
	return c.JSON(http.StatusUnauthorized, authErrorResponse{
		Code:    "UNAUTHORIZED",
		Message: message,
	})
}

func failForbidden(c echo.Context, message string) error {
	if message == "" {
		message = "Insufficient permissions"
	}
	return c.JSON(http.StatusForbidden, authErrorResponse{
		Code:    "FORBIDDEN",
		Message: message,
	})
}

// GetUserID retrieves the authenticated user's UUID from the Echo context.
func GetUserID(c echo.Context) (uuid.UUID, bool) {
	val := c.Get(ContextUserID)
	if val == nil {
		return uuid.Nil, false
	}
	switch v := val.(type) {
	case uuid.UUID:
		return v, v != uuid.Nil
	case string:
		id, err := uuid.Parse(v)
		return id, err == nil && id != uuid.Nil
	default:
		return uuid.Nil, false
	}
}

// GetUserRole retrieves the authenticated user's role from the Echo context.
func GetUserRole(c echo.Context) (Role, bool) {
	val := c.Get(ContextUserRole)
	if val == nil {
		return "", false
	}
	switch v := val.(type) {
	case Role:
		return v, v.Valid()
	case string:
		r := Role(v)
		return r, r.Valid()
	default:
		return "", false
	}
}

// Authenticate verifies the JWT Bearer token and places claims in the Echo context.
func (m *AuthMiddleware) Authenticate(required bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			authHeader := c.Request().Header.Get("Authorization")
			if authHeader == "" {
				if !required {
					return next(c)
				}
				return failUnauthorized(c, "Authorization header is required")
			}

			parts := strings.Fields(authHeader)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				return failUnauthorized(c, "Invalid Authorization header format")
			}

			claims, err := m.verifier.Parse(parts[1])
			if err != nil {
				return failUnauthorized(c, "Invalid or expired access token")
			}

			c.Set(ContextUserID, claims.Subject)
			c.Set(ContextUserRole, claims.Role)
			return next(c)
		}
	}
}

// RequireRoles enforces that a valid JWT is present and has one of the allowed roles.
func (m *AuthMiddleware) RequireRoles(allowed ...Role) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if c.Get(ContextUserID) == nil {
				authHeader := c.Request().Header.Get("Authorization")
				if authHeader == "" {
					return failUnauthorized(c, "Authorization header is required")
				}

				parts := strings.Fields(authHeader)
				if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
					return failUnauthorized(c, "Invalid Authorization header format")
				}

				claims, err := m.verifier.Parse(parts[1])
				if err != nil {
					return failUnauthorized(c, "Invalid or expired access token")
				}

				c.Set(ContextUserID, claims.Subject)
				c.Set(ContextUserRole, claims.Role)
			}

			userRole, ok := GetUserRole(c)
			if !ok {
				return failUnauthorized(c, "User role is invalid")
			}

			for _, allowedRole := range allowed {
				if userRole == allowedRole {
					return next(c)
				}
			}

			return failForbidden(c, "You do not have permission to perform this action")
		}
	}
}

// RequireStaffOrManager is a convenience middleware requiring STAFF or MANAGER role.
func (m *AuthMiddleware) RequireStaffOrManager() echo.MiddlewareFunc {
	return m.RequireRoles(RoleStaff, RoleManager)
}
