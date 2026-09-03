package internal

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// AppError — структурированная ошибка приложения с кодом для i18n.
type AppError struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"` // fallback для не-i18n клиентов
	Status  int    `json:"-"`
}

func (e *AppError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

// Стандартные коды ошибок (frontend переводит через i18n).
var (
	ErrAuthRequired = &AppError{Code: "auth_required", Message: "требуется вход", Status: http.StatusUnauthorized}
	ErrInvalidRequest = &AppError{Code: "invalid_request", Message: "неверный запрос", Status: http.StatusBadRequest}
	ErrSessionCreate = &AppError{Code: "session_create_failed", Message: "не удалось создать сессию", Status: http.StatusInternalServerError}
	ErrRateLimited = &AppError{Code: "rate_limited", Message: "слишком много попыток, повторите позже", Status: http.StatusTooManyRequests}
	ErrAccountExists = &AppError{Code: "account_exists", Message: "регистрация закрыта: аккаунт уже существует", Status: http.StatusConflict}
	ErrAdminOnly = &AppError{Code: "admin_only", Message: "настройки доступны только администратору", Status: http.StatusForbidden}
	ErrEmptyComment = &AppError{Code: "empty_comment", Message: "пустой комментарий", Status: http.StatusBadRequest}
	ErrCommentTooLong = &AppError{Code: "comment_too_long", Message: "слишком длинный комментарий", Status: http.StatusBadRequest}
	ErrCommentSave = &AppError{Code: "comment_save_failed", Message: "не удалось сохранить комментарий", Status: http.StatusInternalServerError}
	ErrCommentNotFound = &AppError{Code: "comment_not_found", Message: "комментарий не найден", Status: http.StatusNotFound}
	ErrCommentNotYours = &AppError{Code: "comment_not_yours", Message: "чужой комментарий удалить нельзя", Status: http.StatusForbidden}
	ErrCommentDelete = &AppError{Code: "comment_delete_failed", Message: "не удалось удалить", Status: http.StatusInternalServerError}
	ErrQRInvalid = &AppError{Code: "qr_invalid", Message: "код недействителен или уже использован", Status: http.StatusBadRequest}
	ErrQRNoAccount = &AppError{Code: "qr_no_account", Message: "аккаунт не найден", Status: http.StatusNotFound}
	ErrProviderUnavailable = &AppError{Code: "provider_unavailable", Message: "источник постов недоступен", Status: http.StatusBadGateway}
	ErrConfirmRequired = &AppError{Code: "confirm_required", Message: "подтверждение обязательно", Status: http.StatusBadRequest}
	ErrProfileFormat = &AppError{Code: "profile_format_error", Message: "ожидается объект вида {\"profile\": {...}}", Status: http.StatusBadRequest}
)

// AbortWithError код —.abort with AppError, frontend переводит по code.
func AbortWithError(c *gin.Context, err *AppError) {
	c.AbortWithStatusJSON(err.Status, gin.H{"error": err.Code, "message": err.Message})
}

// AbortWithErrorMessage —.abort with custom message (fallback).
func AbortWithErrorMessage(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": message})
}
