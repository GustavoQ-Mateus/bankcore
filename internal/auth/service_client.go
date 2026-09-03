package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/GustavoQ-Mateus/bankcore/internal/platform/database"
	"github.com/GustavoQ-Mateus/bankcore/internal/platform/httpx"
)

type ServiceClient struct {
	ID        uuid.UUID `json:"id"`
	ClientID  string    `json:"client_id"`
	Role      Role      `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Service) CreateServiceClient(ctx context.Context) (ServiceClient, string, error) {
	clientID, err := randomToken("svc_")
	if err != nil {
		return ServiceClient{}, "", httpx.ErrInternal
	}
	secret, err := randomToken("")
	if err != nil {
		return ServiceClient{}, "", httpx.ErrInternal
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(secret), s.bcryptCost)
	if err != nil {
		return ServiceClient{}, "", httpx.ErrInternal
	}

	var sc ServiceClient
	err = s.pool.QueryRow(ctx,
		`INSERT INTO service_clients (client_id, secret_hash, role)
		 VALUES ($1, $2, $3)
		 RETURNING id, client_id, role, created_at`,
		clientID, string(hash), RoleSettlement,
	).Scan(&sc.ID, &sc.ClientID, &sc.Role, &sc.CreatedAt)
	if err != nil {
		if database.IsUniqueViolation(err) {
			return ServiceClient{}, "", httpx.ErrConflict("client_id já existe")
		}
		return ServiceClient{}, "", httpx.ErrInternal
	}
	return sc, secret, nil
}

func (s *Service) IssueServiceToken(ctx context.Context, clientID, clientSecret string) (string, int, error) {
	var (
		id         uuid.UUID
		secretHash string
		role       Role
	)
	err := s.pool.QueryRow(ctx,
		`SELECT id, secret_hash, role FROM service_clients WHERE client_id = $1`, clientID,
	).Scan(&id, &secretHash, &role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, httpx.ErrUnauthorized
		}
		return "", 0, httpx.ErrInternal
	}

	if err := bcrypt.CompareHashAndPassword([]byte(secretHash), []byte(clientSecret)); err != nil {
		return "", 0, httpx.ErrUnauthorized
	}

	now := time.Now()
	claims := Claims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   id.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.serviceTTL)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.jwtSecret)
	if err != nil {
		return "", 0, httpx.ErrInternal
	}
	return signed, int(s.serviceTTL.Seconds()), nil
}

func randomToken(prefix string) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}
