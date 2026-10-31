package observability

import (
	"context"
	"testing"
)

// 未配置 OTEL_EXPORTER_OTLP_ENDPOINT 时必须是 no-op:不装 provider、
// shutdown 幂等返回 nil —— 桌面模式的启动路径不得因追踪而改变。
func TestSetupNoEndpointIsNoop(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	shutdown, err := Setup(context.Background())
	if err != nil {
		t.Fatalf("Setup(未配置端点) = %v, want nil", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown = %v, want nil", err)
	}
	if Tracer() == nil {
		t.Error("Tracer() = nil")
	}
}

// 配置端点后 Setup 安装 provider 并返回可用的 shutdown(导出器惰性建连,
// 端点可达性不影响初始化);环境变量由 t.Setenv 在用例后还原。
func TestSetupWithEndpointInstallsProvider(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1/v1/traces")
	shutdown, err := Setup(context.Background())
	if err != nil {
		t.Fatalf("Setup(带端点) = %v, want nil", err)
	}
	defer func() {
		if err := shutdown(context.Background()); err != nil {
			t.Errorf("shutdown = %v, want nil", err)
		}
	}()
	if Tracer() == nil {
		t.Error("Tracer() = nil")
	}
}
