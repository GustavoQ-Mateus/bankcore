package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
)

type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string { return e.Message }

func NewError(status int, code, message string) *APIError {
	return &APIError{Status: status, Code: code, Message: message}
}

var (
	ErrValidation   = func(msg string) *APIError { return NewError(http.StatusBadRequest, "VALIDATION", msg) }
	ErrUnauthorized = NewError(http.StatusUnauthorized, "UNAUTHORIZED", "credenciais inválidas ou ausentes")
	ErrForbidden    = NewError(http.StatusForbidden, "FORBIDDEN", "acesso negado")
	ErrNotFound     = NewError(http.StatusNotFound, "NOT_FOUND", "recurso não encontrado")
	ErrConflict     = func(msg string) *APIError { return NewError(http.StatusConflict, "CONFLICT", msg) }
	ErrInternal     = NewError(http.StatusInternalServerError, "INTERNAL", "erro interno")
)

func Decode(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return ErrValidation("corpo da requisição inválido")
	}
	return nil
}

func JSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if payload != nil {
		_ = json.NewEncoder(w).Encode(payload)
	}
}

func Error(w http.ResponseWriter, err error) {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		apiErr = ErrInternal
	}
	JSON(w, apiErr.Status, map[string]any{
		"error": map[string]string{
			"code":    apiErr.Code,
			"message": apiErr.Message,
		},
	})
}
