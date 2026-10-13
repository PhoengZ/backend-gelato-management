package middleware

import (
	"bytes"
	"errors"
	"strings"

	"analytics-service/internal/models"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
)

const (
	LocalUserID   = "authenticated_user_id"
	LocalUserRole = "authenticated_user_role"
)

var ErrInvalidToken = errors.New("invalid access token")

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
	if claims.IssuedAt == nil || claims.ExpiresAt == nil {
		return nil, ErrInvalidToken
	}
	if claims.Subject == "" || !claims.Role.Valid() {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

// RequireManager enforces that a valid Bearer JWT is present and has the MANAGER role.
func RequireManager(verifier *Verifier) fiber.Handler {
	return RequireRole(verifier, RoleManager)
}

// RequireRole enforces that a valid Bearer JWT is present and contains one of the allowed roles.
func RequireRole(verifier *Verifier, allowedRoles ...Role) fiber.Handler {
	return func(c *fiber.Ctx) error {
		allowed, err := authenticate(c, verifier)
		if !allowed {
			return err
		}

		userRole := Role(c.Locals(LocalUserRole).(string))
		hasRole := false
		for _, role := range allowedRoles {
			if userRole == role {
				hasRole = true
				break
			}
		}

		if !hasRole {
			return c.Status(fiber.StatusForbidden).JSON(models.ApiErrorResponse{
				Code:    "FORBIDDEN",
				Message: "Manager role is required",
			})
		}

		return c.Next()
	}
}

func authenticate(c *fiber.Ctx, verifier *Verifier) (bool, error) {
	c.Locals(LocalUserID, "")
	c.Locals(LocalUserRole, "")

	count := 0
	c.Request().Header.VisitAll(func(key, _ []byte) {
		if bytes.EqualFold(key, []byte(fiber.HeaderAuthorization)) {
			count++
		}
	})

	if count != 1 {
		return false, unauthorized(c)
	}

	parts := strings.Fields(c.Get(fiber.HeaderAuthorization))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return false, unauthorized(c)
	}

	if verifier == nil {
		return false, unauthorized(c)
	}

	claims, err := verifier.Parse(parts[1])
	if err != nil {
		return false, unauthorized(c)
	}

	c.Locals(LocalUserID, claims.Subject)
	c.Locals(LocalUserRole, string(claims.Role))
	return true, nil
}

func unauthorized(c *fiber.Ctx) error {
	return c.Status(fiber.StatusUnauthorized).JSON(models.ApiErrorResponse{
		Code:    "UNAUTHORIZED",
		Message: "Authentication is required",
	})
}
