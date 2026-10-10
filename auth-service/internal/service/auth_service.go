package service

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"

	"auth-service/internal/auth"
	"auth-service/internal/model"
	"auth-service/internal/repository"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidInput       = errors.New("invalid input")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUnauthorized       = errors.New("unauthorized")
	ErrEmailAlreadyExists = repository.ErrEmailAlreadyExists
)

type SignUpInput struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

type LoginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type AuthResult struct {
	AccessToken string      `json:"access_token"`
	TokenType   string      `json:"token_type"`
	ExpiresIn   int64       `json:"expires_in"`
	User        *model.User `json:"user"`
}

type AuthService interface {
	SignUp(ctx context.Context, input SignUpInput) (*model.User, error)
	Login(ctx context.Context, input LoginInput) (*AuthResult, error)
	GetCurrentUser(ctx context.Context, id uuid.UUID) (*model.User, error)
}

type authService struct {
	repository      UserRepository
	tokens          *auth.TokenManager
	bcryptCost      int
	dummyHash       []byte
	comparePassword func([]byte, []byte) error
}

type UserRepository interface {
	Create(ctx context.Context, user *model.User) error
	FindByEmail(ctx context.Context, email string) (*model.User, error)
	FindByID(ctx context.Context, id uuid.UUID) (*model.User, error)
}

func NewAuthService(repository UserRepository, tokens *auth.TokenManager, bcryptCost int) (AuthService, error) {
	if bcryptCost < bcrypt.MinCost || bcryptCost > 14 {
		return nil, fmt.Errorf("bcrypt cost must be between %d and 14", bcrypt.MinCost)
	}
	dummyHash, err := bcrypt.GenerateFromPassword([]byte("unavailable-account-dummy-password"), bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("initialize credential comparison: %w", err)
	}
	return &authService{
		repository:      repository,
		tokens:          tokens,
		bcryptCost:      bcryptCost,
		dummyHash:       dummyHash,
		comparePassword: bcrypt.CompareHashAndPassword,
	}, nil
}

func (s *authService) SignUp(ctx context.Context, input SignUpInput) (*model.User, error) {
	email, err := NormalizeEmail(input.Email)
	if err != nil {
		return nil, err
	}
	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" || !utf8.ValidString(displayName) || utf8.RuneCountInString(displayName) > 100 {
		return nil, fmt.Errorf("%w: display_name must contain between 1 and 100 characters", ErrInvalidInput)
	}
	if err := validatePassword(input.Password, 8); err != nil {
		return nil, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), s.bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	user := &model.User{
		ID:           uuid.New(),
		Email:        email,
		DisplayName:  displayName,
		PasswordHash: string(hash),
		Role:         model.RoleCustomer,
		Status:       model.UserStatusActive,
	}
	if err := s.repository.Create(ctx, user); err != nil {
		if errors.Is(err, repository.ErrEmailAlreadyExists) {
			return nil, ErrEmailAlreadyExists
		}
		return nil, fmt.Errorf("create account: %w", err)
	}
	return user, nil
}

func (s *authService) Login(ctx context.Context, input LoginInput) (*AuthResult, error) {
	email, err := NormalizeEmail(input.Email)
	if err != nil || validatePassword(input.Password, 1) != nil {
		return nil, ErrInvalidCredentials
	}

	user, err := s.repository.FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			_ = s.comparePassword(s.dummyHash, []byte(input.Password))
			return nil, ErrInvalidCredentials
		}
		return nil, fmt.Errorf("find account: %w", err)
	}
	// Disabled users take the same configured-cost comparison path as missing
	// users. Neither status nor account existence bypasses password work.
	if user.Status != model.UserStatusActive {
		_ = s.comparePassword(s.dummyHash, []byte(input.Password))
		return nil, ErrInvalidCredentials
	}
	if s.comparePassword([]byte(user.PasswordHash), []byte(input.Password)) != nil {
		return nil, ErrInvalidCredentials
	}

	accessToken, err := s.tokens.Issue(user)
	if err != nil {
		return nil, err
	}
	return &AuthResult{
		AccessToken: accessToken,
		TokenType:   "Bearer",
		ExpiresIn:   s.tokens.ExpiresInSeconds(),
		User:        user,
	}, nil
}

func (s *authService) GetCurrentUser(ctx context.Context, id uuid.UUID) (*model.User, error) {
	user, err := s.repository.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			return nil, ErrUnauthorized
		}
		return nil, fmt.Errorf("find current user: %w", err)
	}
	if user.Status != model.UserStatusActive {
		return nil, ErrUnauthorized
	}
	return user, nil
}

// NormalizeEmail is the canonical account key used by storage and throttling.
func NormalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" || len(email) > 254 {
		return "", fmt.Errorf("%w: email is invalid", ErrInvalidInput)
	}
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Name != "" || parsed.Address != email {
		return "", fmt.Errorf("%w: email is invalid", ErrInvalidInput)
	}
	return email, nil
}

func validatePassword(password string, minimumCharacters int) error {
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < minimumCharacters || len(password) > 72 {
		return fmt.Errorf("%w: password must contain at least %d characters and at most 72 bytes", ErrInvalidInput, minimumCharacters)
	}
	return nil
}
