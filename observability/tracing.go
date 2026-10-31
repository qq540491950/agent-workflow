// Package observability 初始化 OpenTelemetry 链路追踪。
//
// 默认 no-op:未设置 OTEL_EXPORTER_OTLP_ENDPOINT 时完全不初始化 provider,
// 桌面模式的启动路径与事件行为逐位不变。设置端点后使用 OTLP/HTTP 批量
// 上报;导出失败只影响追踪,不影响工作流执行。
package observability

import (
	"context"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
)

// Tracer 返回本应用的 tracer。每次调用都取当前全局 provider,
// 测试可以在运行前替换全局 provider。
func Tracer() trace.Tracer {
	return otel.Tracer("agentworkflow")
}

// Setup 按 OTEL_EXPORTER_OTLP_ENDPOINT 初始化全局 tracer provider。
//
// 返回的 shutdown 在进程退出前调用(导出批量缓冲);未配置端点时
// 返回 no-op shutdown。
func Setup(ctx context.Context) (func(context.Context) error, error) {
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}
	exp, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(endpoint),
		otlptracehttp.WithTimeout(10*time.Second),
	)
	if err != nil {
		return nil, err
	}
	res, _ := resource.Merge(resource.Default(), resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName("agent-workflow"),
	))
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}
