package auth

import (
	"errors"
	"fmt"
	"time"

	"auth-service/internal/model"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var ErrInvalidToken = errors.New("invalid access token")

type Claims struct {
	Role model.Role `json:"role"`
	jwt.RegisteredClaims
}

type TokenManager struct {
	secret   []byte
	issuer   string
	audience string
	ttl      time.Duration
	now      func() time.Time
}

func NewTokenManager(secret, issuer, audience string, ttl time.Duration) *TokenManager {
	return &TokenManager{
		secret:   []byte(secret),
		issuer:   issuer,
		audience: audience,
		ttl:      ttl,
		now:      time.Now,
	}
}

func (m *TokenManager) Issue(user *model.User) (string, error) {
	if user == nil || user.ID == uuid.Nil || !user.Role.Valid() || user.Status != model.UserStatusActive {
		return "", ErrInvalidToken
	}
	now := m.now().UTC()
	claims := Claims{
		Role: user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID.String(),
			Issuer:    m.issuer,
			Audience:  jwt.ClaimStrings{m.audience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	if err != nil {
		return "", fmt.Errorf("sign access token: %w", err)
	}
	return signed, nil
}

func (m *TokenManager) Parse(raw string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(
		raw,
		claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, ErrInvalidToken
			}
			return m.secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(m.issuer),
		jwt.WithAudience(m.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithTimeFunc(func() time.Time { return m.now().UTC() }),
	)
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	if claims.IssuedAt == nil || claims.ExpiresAt == nil ||
		!claims.ExpiresAt.Time.After(claims.IssuedAt.Time) {
		return nil, ErrInvalidToken
	}
	if id, err := uuid.Parse(claims.Subject); err != nil || id == uuid.Nil || !claims.Role.Valid() {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

func (m *TokenManager) ExpiresInSeconds() int64 {
	return int64(m.ttl / time.Second)
}
