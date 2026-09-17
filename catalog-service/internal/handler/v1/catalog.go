package v1

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"

	"catalog-service/internal/auth"
	"catalog-service/internal/middleware"
	"catalog-service/internal/repository"
	"catalog-service/internal/service"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

type CatalogHandler struct {
	service service.CatalogService
}

func NewCatalogHandler(catalogService service.CatalogService) *CatalogHandler {
	return &CatalogHandler{service: catalogService}
}

func (h *CatalogHandler) List(c *fiber.Ctx) error {
	var active *bool
	if c.Context().QueryArgs().Has("active") {
		if len(c.Context().QueryArgs().PeekMulti("active")) != 1 {
			return badRequest(c, "INVALID_ARGUMENT", "active must be provided only once")
		}
		raw := string(c.Context().QueryArgs().Peek("active"))
		if raw != "true" && raw != "false" {
			return badRequest(c, "INVALID_ARGUMENT", "active must be true or false")
		}
		parsed := raw == "true"
		active = &parsed
	}
	scope := readScope(c)
	if active != nil && !*active && scope != service.ManagerRead {
		return middleware.ManagerRequired(c)
	}
	items, err := h.service.List(c.UserContext(), active, scope)
	if err != nil {
		return handleServiceError(c, err)
	}
	return c.Status(fiber.StatusOK).JSON(fiber.Map{"items": items})
}

func (h *CatalogHandler) Get(c *fiber.Ctx) error {
	id, err := flavorID(c)
	if err != nil {
		return badRequest(c, "INVALID_ARGUMENT", "flavor_id must be a UUID")
	}
	flavor, err := h.service.Get(c.UserContext(), id, readScope(c))
	if err != nil {
		return handleServiceError(c, err)
	}
	return c.Status(fiber.StatusOK).JSON(flavor)
}

func readScope(c *fiber.Ctx) service.ReadScope {
	if c.Locals(middleware.LocalUserRole) == string(auth.RoleManager) {
		return service.ManagerRead
	}
	return service.PublicRead
}

func (h *CatalogHandler) Create(c *fiber.Ctx) error {
	var input service.CreateFlavorInput
	if err := decodeJSON(c.Body(), &input); err != nil {
		return badRequest(c, "INVALID_REQUEST", "Request body is invalid")
	}
	flavor, err := h.service.Create(c.UserContext(), input)
	if err != nil {
		return handleServiceError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(flavor)
}

func (h *CatalogHandler) Update(c *fiber.Ctx) error {
	id, err := flavorID(c)
	if err != nil {
		return badRequest(c, "INVALID_ARGUMENT", "flavor_id must be a UUID")
	}
	var input service.UpdateFlavorInput
	if err := decodeJSON(c.Body(), &input); err != nil {
		return badRequest(c, "INVALID_REQUEST", "Request body is invalid")
	}
	flavor, err := h.service.Update(c.UserContext(), id, input)
	if err != nil {
		return handleServiceError(c, err)
	}
	return c.Status(fiber.StatusOK).JSON(flavor)
}

func (h *CatalogHandler) Replace(c *fiber.Ctx) error {
	id, err := flavorID(c)
	if err != nil {
		return badRequest(c, "INVALID_ARGUMENT", "flavor_id must be a UUID")
	}
	var input service.ReplaceFlavorInput
	if err := decodeJSON(c.Body(), &input); err != nil {
		return badRequest(c, "INVALID_REQUEST", "Request body is invalid")
	}
	flavor, err := h.service.Replace(c.UserContext(), id, input)
	if err != nil {
		return handleServiceError(c, err)
	}
	return c.Status(fiber.StatusOK).JSON(flavor)
}

func (h *CatalogHandler) Archive(c *fiber.Ctx) error {
	id, err := flavorID(c)
	if err != nil {
		return badRequest(c, "INVALID_ARGUMENT", "flavor_id must be a UUID")
	}
	if err := h.service.Archive(c.UserContext(), id); err != nil {
		return handleServiceError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *CatalogHandler) GetRecipe(c *fiber.Ctx) error {
	id, err := flavorID(c)
	if err != nil {
		return badRequest(c, "INVALID_ARGUMENT", "flavor_id must be a UUID")
	}
	recipe, err := h.service.GetRecipe(c.UserContext(), id)
	if err != nil {
		return handleServiceError(c, err)
	}
	return c.Status(fiber.StatusOK).JSON(recipe)
}

func flavorID(c *fiber.Ctx) (uuid.UUID, error) {
	return uuid.Parse(c.Params("flavor_id"))
}

func decodeJSON(body []byte, destination any) error {
	if len(body) == 0 {
		return io.EOF
	}
	parser := json.NewDecoder(bytes.NewReader(body))
	parser.UseNumber()
	rawDocument, err := uniqueJSONValue(parser, 0)
	if err != nil {
		return err
	}
	if _, err := parser.Token(); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	document, ok := rawDocument.(map[string]any)
	if !ok {
		return errors.New("request body must be a JSON object")
	}
	// encoding/json matches struct fields without regard to case. Require the
	// canonical names before decoding so aliases cannot bypass nested validation.
	for key := range document {
		switch key {
		case "name", "description", "price", "image_url", "allergens", "recipe", "active":
		default:
			return errors.New("unknown flavor field")
		}
	}
	if price, ok := document["price"].(map[string]any); ok {
		if _, exists := price["amount_minor"]; !exists {
			return errors.New("price.amount_minor is required")
		}
		if _, exists := price["currency"]; !exists {
			return errors.New("price.currency is required")
		}
		if len(price) != 2 {
			return errors.New("unknown price field")
		}
	}
	if containsNull(rawDocument) {
		return errors.New("null values are not supported")
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

// Reject duplicate keys (including escaped aliases) before decoding into Go
// structs. Bound nesting even for bodies that will later fail schema validation.
func uniqueJSONValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > 16 {
		return nil, errors.New("JSON nesting exceeds the limit")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		document := make(map[string]any)
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("invalid object key")
			}
			if _, exists := document[key]; exists {
				return nil, errors.New("duplicate JSON key")
			}
			value, err := uniqueJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			document[key] = value
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return nil, errors.New("invalid JSON object")
		}
		return document, nil
	case json.Delim('['):
		var values []any
		for decoder.More() {
			value, err := uniqueJSONValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return nil, errors.New("invalid JSON array")
		}
		return values, nil
	default:
		return token, nil
	}
}

func containsNull(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case map[string]any:
		for _, nested := range typed {
			if containsNull(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if containsNull(nested) {
				return true
			}
		}
	}
	return false
}

func handleServiceError(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, service.ErrInvalidInput):
		return badRequest(c, "INVALID_INPUT", err.Error())
	case errors.Is(err, service.ErrFlavorNotFound):
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"code":    "FLAVOR_NOT_FOUND",
			"message": "Flavor was not found",
		})
	case errors.Is(err, service.ErrNameConflict):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"code":    "FLAVOR_NAME_CONFLICT",
			"message": "A flavor with this name already exists",
		})
	case errors.Is(err, repository.ErrUpdateConflict):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"code":    "FLAVOR_UPDATE_CONFLICT",
			"message": "Flavor changed during this request; reload and retry",
		})
	default:
		log.Printf("catalog request failed: %v", err)
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
