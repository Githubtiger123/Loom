// Package apperr 定义平台统一错误码。
//
// 原则：
//   - Code 给程序判断（前端/中间件按码分支）
//   - Message 直接展示给用户（中文，不含内部细节）
//   - cause 只进日志，不返回给前端
package apperr

import (
	"errors"
	"fmt"
	"net/http"
)

// Code 是稳定的机器可读错误码。
type Code string

const (
	CodeInvalid       Code = "INVALID_ARGUMENT"
	CodeUnauthorized  Code = "UNAUTHORIZED"
	CodeForbidden     Code = "FORBIDDEN"
	CodeNotFound      Code = "NOT_FOUND"
	CodeConflict      Code = "CONFLICT"
	CodeQuotaExceeded Code = "QUOTA_EXCEEDED"
	CodeK8sRejected   Code = "K8S_REJECTED"
	CodeInternal      Code = "INTERNAL"
	CodeUnavailable   Code = "UNAVAILABLE"
	CodeRateLimited   Code = "RATE_LIMITED"
)

// Error 是平台唯一的错误载体。
type Error struct {
	Code    Code
	Message string
	cause   error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.cause }

// HTTPStatus 把错误码翻译成 HTTP 状态码。
func (e *Error) HTTPStatus() int {
	switch e.Code {
	case CodeInvalid:
		return http.StatusBadRequest
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeForbidden:
		return http.StatusForbidden
	case CodeNotFound:
		return http.StatusNotFound
	case CodeConflict:
		return http.StatusConflict
	case CodeQuotaExceeded:
		return http.StatusUnprocessableEntity
	case CodeUnavailable:
		return http.StatusServiceUnavailable
	case CodeRateLimited:
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}

// New 构造一个无内部原因的错误。
func New(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Newf 构造带格式化消息的错误。
func Newf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Wrap 把底层错误包成平台错误，cause 只进日志。
func Wrap(code Code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, cause: cause}
}

// From 把任意 error 归一化成 *Error；未知错误一律视为内部错误。
func From(err error) *Error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return Wrap(CodeInternal, "服务内部错误，请稍后重试", err)
}
