package auth

import (
	"errors"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
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
	if _, err := uuid.Parse(claims.Subject); err != nil || !claims.Role.Valid() {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
