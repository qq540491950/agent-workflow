// Package model 内的结构化错误定义。
// 所有错误区分类型(ValidationError / AgentError / ...),支持判断与包装。
package model

import (
	"errors"
	"fmt"
)

// ErrorKind 是错误的分类。
type ErrorKind string

const (
	KindValidationError  ErrorKind = "ValidationError"
	KindAgentError       ErrorKind = "AgentError"
	KindProcessError     ErrorKind = "ProcessError"
	KindTimeoutError     ErrorKind = "TimeoutError"
	KindPermissionError  ErrorKind = "PermissionError"
	KindWorkflowError    ErrorKind = "WorkflowError"
	KindStateError       ErrorKind = "StateError"
	KindPersistenceError ErrorKind = "PersistenceError"
	KindGitError         ErrorKind = "GitError"
	KindCancelledError   ErrorKind = "CancelledError"
)

// Error 是全项目的结构化错误。Code 用于程序化处理,Kind 用于分类。
type Error struct {
	Kind    ErrorKind `json:"kind"`
	Code    string    `json:"code"`
	Node    string    `json:"node,omitempty"`
	Message string    `json:"message"`
	Detail  string    `json:"detail,omitempty"`
}

func (e *Error) Error() string {
	if e.Node != "" {
		return fmt.Sprintf("%s(%s)[node=%s]: %s", e.Kind, e.Code, e.Node, e.Message)
	}
	return fmt.Sprintf("%s(%s): %s", e.Kind, e.Code, e.Message)
}

// NewError 构造一个结构化错误。
func NewError(kind ErrorKind, code, message string) *Error {
	return &Error{Kind: kind, Code: code, Message: message}
}

// AsError 将任意 error 转换为 *Error,保持原错误链。
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var se *Error
	if errors.As(err, &se) {
		return se
	}
	return &Error{Kind: KindWorkflowError, Code: "INTERNAL", Message: err.Error()}
}

// IsKind 判断错误链中是否包含指定分类的错误。
func IsKind(err error, kind ErrorKind) bool {
	var se *Error
	return errors.As(err, &se) && se.Kind == kind
}
