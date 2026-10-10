package v1

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"unicode/utf8"

	"auth-service/internal/middleware"
	"auth-service/internal/service"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type AuthHandler struct {
	service service.AuthService
	limiter *middleware.CredentialLimiter
}

func NewAuthHandler(authService service.AuthService, limiter *middleware.CredentialLimiter) *AuthHandler {
	return &AuthHandler{service: authService, limiter: limiter}
}

func (h *AuthHandler) SignUp(c *fiber.Ctx) error {
	fields, err := decodeCredentials(c.Request().Body(), "email", "password", "display_name")
	if err != nil {
		return badRequest(c, "INVALID_REQUEST", "Request body is invalid")
	}
	email, err := service.NormalizeEmail(fields["email"])
	if err != nil {
		return handleServiceError(c, err)
	}
	release, retry := h.limiter.Acquire(email)
	if release == nil {
		return middleware.RateLimited(c, retry)
	}
	defer release()
	input := service.SignUpInput{Email: email, Password: fields["password"], DisplayName: fields["display_name"]}

	user, err := h.service.SignUp(c.UserContext(), input)
	if err != nil {
		return handleServiceError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(user)
}

func (h *AuthHandler) Login(c *fiber.Ctx) error {
	fields, err := decodeCredentials(c.Request().Body(), "email", "password")
	if err != nil {
		return badRequest(c, "INVALID_REQUEST", "Request body is invalid")
	}
	email, err := service.NormalizeEmail(fields["email"])
	if err != nil {
		return invalidCredentials(c)
	}
	release, retry := h.limiter.Acquire(email)
	if release == nil {
		return middleware.RateLimited(c, retry)
	}
	defer release()
	input := service.LoginInput{Email: email, Password: fields["password"]}

	result, err := h.service.Login(c.UserContext(), input)
	if err != nil {
		if errors.Is(err, service.ErrInvalidCredentials) {
			return invalidCredentials(c)
		}
		return handleServiceError(c, err)
	}
	return c.Status(fiber.StatusOK).JSON(result)
}

func (h *AuthHandler) Me(c *fiber.Ctx) error {
	rawID, ok := c.Locals(middleware.LocalUserID).(string)
	if !ok {
		return unauthorized(c)
	}
	userID, err := uuid.Parse(rawID)
	if err != nil {
		return unauthorized(c)
	}

	user, err := h.service.GetCurrentUser(c.UserContext(), userID)
	if err != nil {
		if errors.Is(err, service.ErrUnauthorized) {
			return unauthorized(c)
		}
		return handleServiceError(c, err)
	}
	return c.Status(fiber.StatusOK).JSON(user)
}

// Credentials are a flat object of required strings. Token decoding avoids the
// duplicate-key, case-insensitive-field and null coercions of struct decoding.
func decodeCredentials(body []byte, required ...string) (map[string]string, error) {
	invalid := errors.New("invalid credential object")
	if !utf8.Valid(body) {
		return nil, invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return nil, invalid
	}
	allowed := make(map[string]bool, len(required))
	for _, key := range required {
		allowed[key] = true
	}
	fields := make(map[string]string, len(required))
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, invalid
		}
		key, ok := keyToken.(string)
		if !ok || !allowed[key] {
			return nil, invalid
		}
		if _, exists := fields[key]; exists {
			return nil, invalid
		}
		valueToken, err := decoder.Token()
		if err != nil {
			return nil, invalid
		}
		value, ok := valueToken.(string)
		if !ok {
			return nil, invalid
		}
		fields[key] = value
	}
	last, err := decoder.Token()
	if err != nil || last != json.Delim('}') || len(fields) != len(required) {
		return nil, invalid
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, invalid
	}
	return fields, nil
}

func handleServiceError(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, service.ErrInvalidInput):
		return badRequest(c, "INVALID_INPUT", err.Error())
	case errors.Is(err, service.ErrEmailAlreadyExists):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"code":    "EMAIL_ALREADY_EXISTS",
			"message": "An account with this email already exists",
		})
	default:
		log.Printf("auth request failed: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"code":    "INTERNAL_ERROR",
			"message": "The request could not be completed",
		})
	}
}

func badRequest(c *fiber.Ctx, code, message string) error {
	return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
		"code":    code,
		"message": message,
	})
}

func invalidCredentials(c *fiber.Ctx) error {
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
		"code":    "INVALID_CREDENTIALS",
		"message": "Email or password is invalid",
	})
}

func unauthorized(c *fiber.Ctx) error {
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
		"code":    "UNAUTHORIZED",
		"message": "Authentication is required",
	})
}
