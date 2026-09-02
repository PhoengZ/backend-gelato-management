package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"catalog-service/internal/model"
	"catalog-service/internal/repository"

	"github.com/google/uuid"
)

var (
	ErrInvalidInput   = errors.New("invalid input")
	ErrFlavorNotFound = repository.ErrFlavorNotFound
	ErrNameConflict   = repository.ErrNameConflict
)

type CreateFlavorInput struct {
	Name        *string           `json:"name"`
	Description *string           `json:"description"`
	Price       *model.Money      `json:"price"`
	ImageURL    *string           `json:"image_url"`
	Allergens   *[]model.Allergen `json:"allergens"`
	Recipe      *string           `json:"recipe"`
	Active      *bool             `json:"active"`
}

type UpdateFlavorInput struct {
	Name        *string           `json:"name"`
	Description *string           `json:"description"`
	Price       *model.Money      `json:"price"`
	ImageURL    *string           `json:"image_url"`
	Allergens   *[]model.Allergen `json:"allergens"`
	Recipe      *string           `json:"recipe"`
	Active      *bool             `json:"active"`
}

type FlavorRepository interface {
	Create(ctx context.Context, flavor *model.FlavorAdmin) error
	FindByID(ctx context.Context, id uuid.UUID) (*model.FlavorAdmin, error)
	List(ctx context.Context) ([]*model.FlavorAdmin, error)
	Update(ctx context.Context, previousName string, flavor *model.FlavorAdmin) error
}

type CatalogService interface {
	Create(ctx context.Context, input CreateFlavorInput) (*model.FlavorAdmin, error)
	List(ctx context.Context, active *bool) ([]model.Flavor, error)
	Get(ctx context.Context, id uuid.UUID) (*model.Flavor, error)
	Update(ctx context.Context, id uuid.UUID, input UpdateFlavorInput) (*model.FlavorAdmin, error)
	Archive(ctx context.Context, id uuid.UUID) error
	GetRecipe(ctx context.Context, id uuid.UUID) (*model.FlavorRecipe, error)
}

type catalogService struct {
	repository FlavorRepository
	now        func() time.Time
}

func NewCatalogService(repository FlavorRepository) CatalogService {
	return &catalogService{repository: repository, now: time.Now}
}

func (s *catalogService) Create(ctx context.Context, input CreateFlavorInput) (*model.FlavorAdmin, error) {
	name, description, price, imageURL, allergens, recipe, active, err := validateCreateInput(input)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	flavor := &model.FlavorAdmin{
		Flavor: model.Flavor{
			ID:          uuid.New(),
			Name:        name,
			Description: description,
			Price:       price,
			ImageURL:    imageURL,
			Allergens:   allergens,
			Active:      active,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		Recipe: recipe,
	}
	if err := s.repository.Create(ctx, flavor); err != nil {
		if errors.Is(err, repository.ErrNameConflict) {
			return nil, ErrNameConflict
		}
		return nil, fmt.Errorf("create flavor: %w", err)
	}
	return flavor, nil
}

func (s *catalogService) List(ctx context.Context, active *bool) ([]model.Flavor, error) {
	records, err := s.repository.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list flavors: %w", err)
	}
	items := make([]model.Flavor, 0, len(records))
	for _, record := range records {
		if active != nil && record.Active != *active {
			continue
		}
		items = append(items, record.Flavor)
	}
	return items, nil
}

func (s *catalogService) Get(ctx context.Context, id uuid.UUID) (*model.Flavor, error) {
	record, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	public := record.Flavor
	return &public, nil
}

func (s *catalogService) Update(ctx context.Context, id uuid.UUID, input UpdateFlavorInput) (*model.FlavorAdmin, error) {
	if !hasUpdate(input) {
		return nil, fmt.Errorf("%w: at least one field must be provided", ErrInvalidInput)
	}

	record, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	previousName := record.Name

	if input.Name != nil {
		record.Name, err = validateName(*input.Name)
		if err != nil {
			return nil, err
		}
	}
	if input.Description != nil {
		record.Description, err = validateDescription(*input.Description)
		if err != nil {
			return nil, err
		}
	}
	if input.Price != nil {
		if err := validateMoney(*input.Price); err != nil {
			return nil, err
		}
		record.Price = *input.Price
	}
	if input.ImageURL != nil {
		record.ImageURL, err = validateImageURL(*input.ImageURL)
		if err != nil {
			return nil, err
		}
	}
	if input.Allergens != nil {
		record.Allergens, err = validateAllergens(*input.Allergens)
		if err != nil {
			return nil, err
		}
	}
	if input.Recipe != nil {
		record.Recipe, err = validateRecipe(*input.Recipe)
		if err != nil {
			return nil, err
		}
	}
	if input.Active != nil {
		record.Active = *input.Active
	}
	record.UpdatedAt = s.now().UTC()

	if err := s.repository.Update(ctx, previousName, record); err != nil {
		switch {
		case errors.Is(err, repository.ErrFlavorNotFound):
			return nil, ErrFlavorNotFound
		case errors.Is(err, repository.ErrNameConflict):
			return nil, ErrNameConflict
		default:
			return nil, fmt.Errorf("update flavor: %w", err)
		}
	}
	return record, nil
}

func (s *catalogService) Archive(ctx context.Context, id uuid.UUID) error {
	record, err := s.find(ctx, id)
	if err != nil {
		return err
	}
	if !record.Active {
		return nil
	}
	previousName := record.Name
	record.Active = false
	record.UpdatedAt = s.now().UTC()
	if err := s.repository.Update(ctx, previousName, record); err != nil {
		if errors.Is(err, repository.ErrFlavorNotFound) {
			return ErrFlavorNotFound
		}
		return fmt.Errorf("archive flavor: %w", err)
	}
	return nil
}

func (s *catalogService) GetRecipe(ctx context.Context, id uuid.UUID) (*model.FlavorRecipe, error) {
	record, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	return &model.FlavorRecipe{
		FlavorID:  record.ID,
		Recipe:    record.Recipe,
		UpdatedAt: record.UpdatedAt,
	}, nil
}

func (s *catalogService) find(ctx context.Context, id uuid.UUID) (*model.FlavorAdmin, error) {
	record, err := s.repository.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrFlavorNotFound) {
			return nil, ErrFlavorNotFound
		}
		return nil, fmt.Errorf("find flavor: %w", err)
	}
	return record, nil
}

func validateCreateInput(input CreateFlavorInput) (string, string, model.Money, string, []model.Allergen, string, bool, error) {
	if input.Name == nil || input.Description == nil || input.Price == nil || input.Allergens == nil || input.Recipe == nil {
		return "", "", model.Money{}, "", nil, "", false, fmt.Errorf("%w: name, description, price, allergens, and recipe are required", ErrInvalidInput)
	}
	name, err := validateName(*input.Name)
	if err != nil {
		return "", "", model.Money{}, "", nil, "", false, err
	}
	description, err := validateDescription(*input.Description)
	if err != nil {
		return "", "", model.Money{}, "", nil, "", false, err
	}
	if err := validateMoney(*input.Price); err != nil {
		return "", "", model.Money{}, "", nil, "", false, err
	}
	imageURL := ""
	if input.ImageURL != nil {
		imageURL, err = validateImageURL(*input.ImageURL)
		if err != nil {
			return "", "", model.Money{}, "", nil, "", false, err
		}
	}
	allergens, err := validateAllergens(*input.Allergens)
	if err != nil {
		return "", "", model.Money{}, "", nil, "", false, err
	}
	recipe, err := validateRecipe(*input.Recipe)
	if err != nil {
		return "", "", model.Money{}, "", nil, "", false, err
	}
	active := true
	if input.Active != nil {
		active = *input.Active
	}
	return name, description, *input.Price, imageURL, allergens, recipe, active, nil
}

func validateName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	count := utf8.RuneCountInString(name)
	if count < 1 || count > 100 {
		return "", fmt.Errorf("%w: name must contain between 1 and 100 characters", ErrInvalidInput)
	}
	return name, nil
}

func validateDescription(raw string) (string, error) {
	description := strings.TrimSpace(raw)
	if utf8.RuneCountInString(description) > 1000 {
		return "", fmt.Errorf("%w: description must contain at most 1000 characters", ErrInvalidInput)
	}
	return description, nil
}

func validateMoney(money model.Money) error {
	if money.AmountMinor < 0 || money.Currency != "THB" {
		return fmt.Errorf("%w: price must be a non-negative amount in THB", ErrInvalidInput)
	}
	return nil
}

func validateImageURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	parsed, err := url.ParseRequestURI(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", fmt.Errorf("%w: image_url must be an absolute HTTP or HTTPS URL", ErrInvalidInput)
	}
	return value, nil
}

func validateAllergens(raw []model.Allergen) ([]model.Allergen, error) {
	allergens := append([]model.Allergen(nil), raw...)
	seen := make(map[model.Allergen]struct{}, len(allergens))
	for _, allergen := range allergens {
		if !allergen.Valid() {
			return nil, fmt.Errorf("%w: allergen %q is not supported", ErrInvalidInput, allergen)
		}
		if _, exists := seen[allergen]; exists {
			return nil, fmt.Errorf("%w: allergens must be unique", ErrInvalidInput)
		}
		seen[allergen] = struct{}{}
	}
	sort.Slice(allergens, func(i, j int) bool { return allergens[i] < allergens[j] })
	if allergens == nil {
		allergens = []model.Allergen{}
	}
	return allergens, nil
}

func validateRecipe(raw string) (string, error) {
	recipe := strings.TrimSpace(raw)
	if recipe == "" {
		return "", fmt.Errorf("%w: recipe must not be empty", ErrInvalidInput)
	}
	return recipe, nil
}

func hasUpdate(input UpdateFlavorInput) bool {
	return input.Name != nil || input.Description != nil || input.Price != nil || input.ImageURL != nil ||
		input.Allergens != nil || input.Recipe != nil || input.Active != nil
}
