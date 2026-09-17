package services

import (
	"context"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"orderflow/contracts"
	"orderflow/domains/user"
)

type Claims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

type UserService struct {
	repo      contracts.UserRepository
	jwtSecret string
	tokenTTL  time.Duration
}

func NewUserService(repo contracts.UserRepository, jwtSecret string, tokenTTL time.Duration) *UserService {
	return &UserService{repo: repo, jwtSecret: jwtSecret, tokenTTL: tokenTTL}
}

func (s *UserService) Register(ctx context.Context, email, password string) (*user.User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	u := &user.User{Email: email, PasswordHash: string(hash)}
	if err := s.repo.Create(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

func (s *UserService) Login(ctx context.Context, email, password string) (string, error) {
	u, err := s.repo.GetByEmail(ctx, email)
	if err != nil {
		return "", user.ErrBadCreds
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return "", user.ErrBadCreds
	}

	claims := Claims{
		Role: u.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   u.ID,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(s.tokenTTL)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(s.jwtSecret))
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return signed, nil
}
