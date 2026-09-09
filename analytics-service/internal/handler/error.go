package handler

import (
	"errors"
	"fmt"

	"analytics-service/internal/models"

	"github.com/gofiber/fiber/v2"
)

// CustomErrorHandler provides centralized error handling for the Fiber app,
// ensuring all framework-level errors (404, 405, etc.) conform to docs/API_SPEC.md.
func CustomErrorHandler(c *fiber.Ctx, err error) error {
	statusCode := fiber.StatusInternalServerError
	errorCode := "UNKNOWN_ERROR"

	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		statusCode = fiberErr.Code
		switch statusCode {
		case fiber.StatusBadRequest:
			errorCode = "HTTP_400"
		case fiber.StatusUnauthorized:
			errorCode = "HTTP_401"
		case fiber.StatusForbidden:
			errorCode = "HTTP_403"
		case fiber.StatusNotFound:
			errorCode = "HTTP_404"
		case fiber.StatusMethodNotAllowed:
			errorCode = "HTTP_405"
		default:
			errorCode = fmt.Sprintf("HTTP_%d", statusCode)
		}
	}

	return c.Status(statusCode).JSON(models.ApiErrorResponse{
		Message: err.Error(),
		Code:    errorCode,
	})
}
