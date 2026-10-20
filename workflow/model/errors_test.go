package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// TestErrorMessageFormat 锁住结构化错误的可读格式。
func TestErrorMessageFormat(t *testing.T) {
	e := &Error{Kind: KindValidationError, Code: "MISSING_AGENT", Node: "plan", Message: "缺少 agent 配置"}
	want := "ValidationError(MISSING_AGENT)[node=plan]: 缺少 agent 配置"
	if got := e.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	// 无节点时省略 [node=...] 段
	e2 := &Error{Kind: KindWorkflowError, Code: "INTERNAL", Message: "炸了"}
	want2 := "WorkflowError(INTERNAL): 炸了"
	if got := e2.Error(); got != want2 {
		t.Errorf("Error() = %q, want %q", got, want2)
	}
}

// TestAsErrorPreservesModelError 验证 AsError 对已是 *Error 的错误原样返回。
func TestAsErrorPreservesModelError(t *testing.T) {
	orig := NewError(KindTimeoutError, "CLAUDE_TIMEOUT", "超时")
	if got := AsError(orig); got != orig {
		t.Errorf("AsError 应返回同一个 *Error,实际 %+v", got)
	}
	// 包一层 fmt.Errorf 后仍能取到
	wrapped := fmt.Errorf("执行失败: %w", orig)
	if got := AsError(wrapped); got != orig {
		t.Errorf("AsError(包装后) 应返回同一个 *Error,实际 %+v", got)
	}
}

// TestAsErrorWrapsPlainError 验证普通错误被兜底包装成 *Error,
// 且**保留原始错误链**(errors.Is 能找回根因)。
func TestAsErrorWrapsPlainError(t *testing.T) {
	root := errors.New("disk full")
	got := AsError(root)
	if got == nil {
		t.Fatal("AsError 不应返回 nil")
	}
	if got.Kind != KindWorkflowError {
		t.Errorf("kind = %q, want %q", got.Kind, KindWorkflowError)
	}
	if got.Code != "INTERNAL" {
		t.Errorf("code = %q, want INTERNAL", got.Code)
	}
	if got.Message != "disk full" {
		t.Errorf("message = %q", got.Message)
	}
	// 原始错误链必须保留,否则根因在结构化包装里被彻底吞掉
	if !errors.Is(got, root) {
		t.Error("AsError 兜底分支丢失了原始错误链(errors.Is 失败)")
	}
	if got.Unwrap() != root {
		t.Error("Unwrap 应返回原始错误")
	}
}

// TestAsErrorNil 验证 nil 输入返回 nil(不制造假错误)。
func TestAsErrorNil(t *testing.T) {
	if got := AsError(nil); got != nil {
		t.Errorf("AsError(nil) = %+v, want nil", got)
	}
}

// TestIsKind 锁住错误分类判定(含包装链)。
func TestIsKind(t *testing.T) {
	e := NewError(KindPermissionError, "DENIED", "没有权限")
	if !IsKind(e, KindPermissionError) {
		t.Error("应识别为 PermissionError")
	}
	if IsKind(e, KindTimeoutError) {
		t.Error("不应识别为 TimeoutError")
	}
	// 包装后仍能识别
	wrapped := fmt.Errorf("节点执行失败: %w", e)
	if !IsKind(wrapped, KindPermissionError) {
		t.Error("包装后仍应识别为 PermissionError")
	}
	// 包装里是普通错误 → 不是任何 model 分类
	if IsKind(errors.New("boom"), KindWorkflowError) {
		t.Error("普通错误不应被判为 model 分类")
	}
	if IsKind(nil, KindWorkflowError) {
		t.Error("nil 不应被判为任何分类")
	}
}

// TestErrorJSONRoundTrip 锁住错误的前端契约字段(kind/code/node/message/detail)。
func TestErrorJSONRoundTrip(t *testing.T) {
	e := &Error{
		Kind:    KindStateError,
		Code:    "INVALID_RESUME",
		Node:    "review",
		Message: "执行不在可恢复状态",
		Detail:  "state=COMPLETED",
	}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := &Error{}
	if err := json.Unmarshal(raw, got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(e, got) {
		t.Errorf("错误往返不一致:\nwant %+v\ngot  %+v", e, got)
	}
	// node / detail 为空时不出现在 JSON 里
	raw2, err := json.Marshal(NewError(KindValidationError, "X", "msg"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw2, &m); err != nil {
		t.Fatalf("unmarshal map: %v", err)
	}
	if _, ok := m["node"]; ok {
		t.Error("空 node 不应出现在 JSON 中")
	}
	if _, ok := m["detail"]; ok {
		t.Error("空 detail 不应出现在 JSON 中")
	}
}

// TestErrorKindConstants 锁住错误分类的字符串值(前端/日志按值匹配,不能随意改)。
func TestErrorKindConstants(t *testing.T) {
	want := map[ErrorKind]string{
		KindValidationError:  "ValidationError",
		KindAgentError:       "AgentError",
		KindProcessError:     "ProcessError",
		KindTimeoutError:     "TimeoutError",
		KindPermissionError:  "PermissionError",
		KindWorkflowError:    "WorkflowError",
		KindStateError:       "StateError",
		KindPersistenceError: "PersistenceError",
		KindGitError:         "GitError",
		KindCancelledError:   "CancelledError",
	}
	for kind, s := range want {
		if string(kind) != s {
			t.Errorf("错误分类 %q 的字面量变成了 %q", s, string(kind))
		}
	}
}
