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

// CodeMisplacedSection 是"permissions 段写错层级"的统一错误码。
//
// DSL 解析层(workflow/dsl,workflow:/settings: 段错放)与校验层
// (workflow/validator,节点 config 里的 permissions 段)共用同一个
// code —— 错误码字符串只在这一处定义,两处引用,不得再手抄第二份。
// 放 model 而不放 dsl:dsl 的测试包 import 了 validator,validator 再
// 回头 import dsl 会构成测试期 import cycle;model 是两包共同的底层依赖。
const CodeMisplacedSection = "DSL_MISPLACED_KEY"

// Error 是全项目的结构化错误。Code 用于程序化处理,Kind 用于分类。
type Error struct {
	Kind    ErrorKind `json:"kind"`
	Code    string    `json:"code"`
	Node    string    `json:"node,omitempty"`
	Message string    `json:"message"`
	Detail  string    `json:"detail,omitempty"`
	// cause 保存被包装的原始错误(AsError 兜底分支填入)。
	// 不导出 → 不进 JSON(前端只看到 kind/code/node/message/detail),
	// 但保留原始错误链,errors.Is / errors.As 能一路挖到根因。
	cause error
}

// Unwrap 暴露被包装的原始错误,使 errors.Is / errors.As 能穿透到根因。
func (e *Error) Unwrap() error { return e.cause }

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
	// 兜底:保留原始错误(cause),调用方既能拿到统一结构,
	// 又能 errors.Is(err, 原始错误) 找回根因。
	return &Error{Kind: KindWorkflowError, Code: "INTERNAL", Message: err.Error(), cause: err}
}

// IsKind 判断错误链中是否包含指定分类的错误。
func IsKind(err error, kind ErrorKind) bool {
	var se *Error
	return errors.As(err, &se) && se.Kind == kind
}
