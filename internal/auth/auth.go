package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/GustavoQ-Mateus/bankcore/internal/platform/database"
	"github.com/GustavoQ-Mateus/bankcore/internal/platform/httpx"
)

type Role string

const (
	RoleCustomer   Role = "CUSTOMER"
	RoleAdmin      Role = "ADMIN"
	RoleSettlement Role = "SETTLEMENT"
)

type Customer struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Role         Role      `json:"role"`
	CreatedAt    time.Time `json:"created_at"`
}

type Service struct {
	pool                     *pgxpool.Pool
	jwtSecret                []byte
	jwtTTL                   time.Duration
	serviceTTL               time.Duration
	bcryptCost               int
	allowPublicAdminRegister bool
}

func NewService(pool *pgxpool.Pool, jwtSecret string, jwtTTL, serviceTTL time.Duration, bcryptCost int, allowPublicAdminRegister bool) *Service {
	return &Service{
		pool:                     pool,
		jwtSecret:                []byte(jwtSecret),
		jwtTTL:                   jwtTTL,
		serviceTTL:               serviceTTL,
		bcryptCost:               bcryptCost,
		allowPublicAdminRegister: allowPublicAdminRegister,
	}
}

func (s *Service) Register(ctx context.Context, name, email, password string, role Role) (Customer, error) {
	if role == "" {
		role = RoleCustomer
	}
	if role != RoleCustomer {
		if !s.allowPublicAdminRegister {
			return Customer{}, httpx.ErrAdminRegisterDisabled
		}
		if role != RoleAdmin {
			role = RoleCustomer
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.bcryptCost)
	if err != nil {
		return Customer{}, httpx.ErrInternal
	}

	var c Customer
	err = s.pool.QueryRow(ctx,
		`INSERT INTO customers (name, email, password_hash, role)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, name, email, password_hash, role, created_at`,
		name, email, string(hash), role,
	).Scan(&c.ID, &c.Name, &c.Email, &c.PasswordHash, &c.Role, &c.CreatedAt)
	if err != nil {
		if database.IsUniqueViolation(err) {
			return Customer{}, httpx.ErrConflict("email já cadastrado")
		}
		return Customer{}, httpx.ErrInternal
	}
	return c, nil
}

func (s *Service) Login(ctx context.Context, email, password string) (string, Customer, error) {
	var c Customer
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, email, password_hash, role, created_at
		 FROM customers WHERE email = $1`, email,
	).Scan(&c.ID, &c.Name, &c.Email, &c.PasswordHash, &c.Role, &c.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", Customer{}, httpx.ErrUnauthorized
		}
		return "", Customer{}, httpx.ErrInternal
	}

	if err := bcrypt.CompareHashAndPassword([]byte(c.PasswordHash), []byte(password)); err != nil {
		return "", Customer{}, httpx.ErrUnauthorized
	}

	token, err := s.issueToken(c)
	if err != nil {
		return "", Customer{}, httpx.ErrInternal
	}
	return token, c, nil
}

func (s *Service) EnsureAdmin(ctx context.Context, name, email, password string) (bool, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM customers WHERE email = $1)`, email,
	).Scan(&exists); err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), s.bcryptCost)
	if err != nil {
		return false, err
	}

	_, err = s.pool.Exec(ctx,
		`INSERT INTO customers (name, email, password_hash, role) VALUES ($1, $2, $3, $4)`,
		name, email, string(hash), RoleAdmin,
	)
	if err != nil {
		if database.IsUniqueViolation(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
