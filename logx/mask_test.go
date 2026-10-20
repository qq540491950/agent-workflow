package logx

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// mustJSON 把脱敏结果序列化成 JSON,便于断言。
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("序列化脱敏结果失败: %v", err)
	}
	return string(b)
}

// renderOut 把脱敏结果渲染成可断言的文本。
// []byte 走 base64(JSON 规范),断言前先转回原始字节。
func renderOut(t *testing.T, v any) string {
	t.Helper()
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return mustJSON(t, v)
}

// maskCredPayload 是带 json tag 的结构体(字段名按 json 名判定敏感度)。
type maskCredPayload struct {
	APIKey string `json:"api_key"`
	Note   string `json:"note"`
	Hidden string `json:"-"`
}

// containerSpec 是"掩码类型覆盖矩阵"的一行:一个容器类型。
type containerSpec struct {
	name string
	// build 构造容器:withCred=true 放凭据键,false 放普通键。
	build func(withCred bool) any
	// cred 是凭据明文(带凭据那一行绝不能出现在输出里)。
	cred string
	// keep 是必须原样保留的普通内容。
	keep string
	// noMaskable 表示该容器装不下字符串(数值型),不存在"可脱敏的凭据"。
	noMaskable bool
}

// TestMaskValueContainerCoverage 是掩码的类型覆盖矩阵:
// 每个"能装字符串的容器"都测两行 —— 含凭据键 / 不含凭据键。
//
// 回归背景:此前 maskRecursive 只认 map[string]any 与 []any,
// map[string]string / []string / 结构体 / 指针等一律原样返回 ——
// 嵌在事件 payload 里的 map[string]string 凭据(实测
// map[env:map[API_KEY:sk-2]])原样落库并推给前端。
func TestMaskValueContainerCoverage(t *testing.T) {
	const cred = "sk-live-9f8e7d6c"
	const plain = "普通文本"

	specs := []containerSpec{
		{
			name: "map[string]any",
			build: func(c bool) any {
				m := map[string]any{"note": plain}
				if c {
					m["API_KEY"] = cred
				}
				return m
			},
			cred: cred, keep: plain,
		},
		{
			name: "map[string]string",
			build: func(c bool) any {
				m := map[string]string{"note": plain}
				if c {
					m["API_KEY"] = cred
				}
				return m
			},
			cred: cred, keep: plain,
		},
		{
			name: "map[string][]string",
			build: func(c bool) any {
				m := map[string][]string{"note": {plain}}
				if c {
					m["api_key"] = []string{cred}
				}
				return m
			},
			cred: cred, keep: plain,
		},
		{
			name: "map[string]map[string]string",
			build: func(c bool) any {
				m := map[string]map[string]string{"env": {"note": plain}}
				if c {
					m["env"]["API_KEY"] = cred
				}
				return m
			},
			cred: cred, keep: plain,
		},
		{
			name: "map[string]any 内嵌 map[string]string",
			build: func(c bool) any {
				inner := map[string]string{"note": plain}
				if c {
					inner["API_KEY"] = cred
				}
				return map[string]any{"env": inner}
			},
			cred: cred, keep: plain,
		},
		{
			name: "[]string",
			build: func(c bool) any {
				if c {
					// 切片元素没有键名线索:只有 key=value 形态能识别
					return []string{plain, "api_key=" + cred}
				}
				return []string{plain}
			},
			cred: cred, keep: plain,
		},
		{
			name: "[]any",
			build: func(c bool) any {
				if c {
					return []any{plain, "api_key=" + cred}
				}
				return []any{plain}
			},
			cred: cred, keep: plain,
		},
		{
			name: "[][]string",
			build: func(c bool) any {
				if c {
					return [][]string{{plain}, {"api_key=" + cred}}
				}
				return [][]string{{plain}}
			},
			cred: cred, keep: plain,
		},
		{
			name: "[]map[string]string",
			build: func(c bool) any {
				if c {
					return []map[string]string{{"note": plain}, {"API_KEY": cred}}
				}
				return []map[string]string{{"note": plain}}
			},
			cred: cred, keep: plain,
		},
		{
			name: "结构体(json tag)",
			build: func(c bool) any {
				p := maskCredPayload{Note: plain, Hidden: cred}
				if c {
					p.APIKey = cred
				}
				return p
			},
			cred: cred, keep: plain,
		},
		{
			name: "*map[string]string",
			build: func(c bool) any {
				m := map[string]string{"note": plain}
				if c {
					m["API_KEY"] = cred
				}
				return &m
			},
			cred: cred, keep: plain,
		},
		{
			name: "[]byte",
			build: func(c bool) any {
				if c {
					return []byte("api_key=" + cred)
				}
				return []byte(plain)
			},
			cred: cred, keep: plain,
		},
		{
			name: "map[string]int(装不下字符串)",
			build: func(c bool) any {
				m := map[string]int{"note": 1}
				if c {
					m["API_KEY"] = 42
				}
				return m
			},
			cred: "", keep: plain, noMaskable: true,
		},
	}

	for _, sp := range specs {
		sp := sp
		t.Run(sp.name, func(t *testing.T) {
			// 含凭据键:凭据明文不得出现在输出里。
			out := renderOut(t, MaskValue(sp.build(true)))
			if sp.noMaskable {
				// 数值容器装不下 "***" 占位符:值原样保留(见报告的已知边界)。
				if !strings.Contains(out, "42") {
					t.Errorf("数值型值应原样保留: %s", out)
				}
				return
			}
			if strings.Contains(out, sp.cred) {
				t.Errorf("凭据未脱敏: %s", out)
			}
			if !strings.Contains(out, "***") {
				t.Errorf("期望出现脱敏占位符: %s", out)
			}
			// 不含凭据键:普通内容不得被误伤。
			out2 := renderOut(t, MaskValue(sp.build(false)))
			if strings.Contains(out2, "***") {
				t.Errorf("普通内容被误伤: %s", out2)
			}
			if !strings.Contains(out2, sp.keep) {
				t.Errorf("普通内容丢失: %s", out2)
			}
		})
	}
}

// TestMaskValueNestedMapStringStringRegression 锁定上一轮报告的具体缺口:
// 事件 payload 里嵌套 map[string]string 时凭据原样输出。
func TestMaskValueNestedMapStringStringRegression(t *testing.T) {
	in := map[string]any{"env": map[string]string{"API_KEY": "sk-2"}}
	out, ok := MaskValue(in).(map[string]any)
	if !ok {
		t.Fatalf("应返回 map[string]any, 实际 %T", MaskValue(in))
	}
	env, ok := out["env"].(map[string]string)
	if !ok {
		t.Fatalf("嵌套 map[string]string 类型被破坏: %T", out["env"])
	}
	if env["API_KEY"] != "***" {
		t.Errorf("嵌套 map[string]string 的凭据未脱敏: %v", env)
	}
}

// TestMaskValueDoesNotMutateInputContainers 脱敏不得就地修改调用方数据。
func TestMaskValueDoesNotMutateInputContainers(t *testing.T) {
	m := map[string]string{"API_KEY": "sk-3"}
	_ = MaskValue(m)
	if m["API_KEY"] != "sk-3" {
		t.Errorf("入参被就地修改: %v", m)
	}
	s := []string{"api_key=sk-4"}
	_ = MaskValue(s)
	if s[0] != "api_key=sk-4" {
		t.Errorf("入参被就地修改: %v", s)
	}
}

// TestKeySensitivity 锁定键名判定规则(大小写不敏感、分隔符无关)。
func TestKeySensitivity(t *testing.T) {
	cases := []struct {
		key  string
		want sensitivity
	}{
		// 强敏感:单词
		{"token", sensStrong},
		{"TOKEN", sensStrong},
		{"Token", sensStrong},
		{"password", sensStrong},
		{"passwd", sensStrong},
		{"secret", sensStrong},
		{"secrets", sensStrong},
		{"credential", sensStrong},
		{"credentials", sensStrong},
		{"authorization", sensStrong},
		// 强敏感:复合词组
		{"api_key", sensStrong},
		{"API_KEY", sensStrong},
		{"apiKey", sensStrong},
		{"apikey", sensStrong},
		{"x-api-key", sensStrong},
		{"ANTHROPIC_API_KEY", sensStrong},
		{"access_key", sensStrong},
		{"aws_access_key_id", sensStrong},
		{"private_key", sensStrong},
		{"client_secret", sensStrong},
		{"db_secret", sensStrong},
		{"authtoken", sensStrong},
		{"access_token", sensStrong},
		{"session_token", sensStrong},
		// 弱敏感:token 在中间
		{"token_limit", sensWeak},
		{"token_budget", sensWeak},
		// 良性:token 计数类
		{"max_tokens", sensNone},
		{"maxTokens", sensNone},
		{"total_tokens", sensNone},
		{"input_tokens", sensNone},
		{"token_count", sensNone},
		{"tokens_used", sensNone},
		// 良性:不含凭据语义
		{"key", sensNone},
		{"keys", sensNone},
		{"keyword", sensNone},
		{"tokenizer", sensNone},
		{"note", sensNone},
		{"", sensNone},
	}
	for _, c := range cases {
		if got := keySensitivity(c.key); got != c.want {
			t.Errorf("keySensitivity(%q) = %v, 期望 %v", c.key, got, c.want)
		}
	}
}

// TestMaskValueKeyRuleBehavior 用真实 payload 验证键名规则的可见效果:
// 强敏感键整值替换,弱敏感键只替换字符串,计数键不误伤。
func TestMaskValueKeyRuleBehavior(t *testing.T) {
	in := map[string]any{
		"API_KEY":      "sk-strong",
		"auth_token":   "sk-weak-strong",
		"token_limit":  "sk-weak",
		"max_tokens":   1000,
		"total_tokens": "2000",
		"key":          "plain-value",
	}
	out := MaskValue(in).(map[string]any)

	if out["API_KEY"] != "***" {
		t.Errorf("API_KEY 应整值替换: %v", out["API_KEY"])
	}
	if out["auth_token"] != "***" {
		t.Errorf("auth_token 应整值替换: %v", out["auth_token"])
	}
	if out["token_limit"] != "***" {
		t.Errorf("token_limit 是字符串,应被替换: %v", out["token_limit"])
	}
	if out["max_tokens"] != 1000 {
		t.Errorf("max_tokens 是用量统计,不应被掩码: %v", out["max_tokens"])
	}
	if out["total_tokens"] != "2000" {
		t.Errorf("total_tokens 是用量统计,不应被掩码: %v", out["total_tokens"])
	}
	if out["key"] != "plain-value" {
		t.Errorf("单词 key 不应被判为凭据: %v", out["key"])
	}
}

// TestMaskValueWeakKeyKeepsNumber 弱敏感键下的数字不是凭据,必须保留。
func TestMaskValueWeakKeyKeepsNumber(t *testing.T) {
	in := map[string]any{"token_limit": 7}
	out := MaskValue(in).(map[string]any)
	if out["token_limit"] != 7 {
		t.Errorf("弱敏感键下的数字应保留: %v", out["token_limit"])
	}
}

// TestMaskPasswdForm 回归:值形态里的 passwd=xxx 此前不被脱敏。
func TestMaskPasswdForm(t *testing.T) {
	got := Mask("db passwd=hunter2")
	if strings.Contains(got, "hunter2") {
		t.Errorf("passwd=xxx 未脱敏: %q", got)
	}
	if !strings.Contains(got, "***") {
		t.Errorf("期望出现 ***: %q", got)
	}
}

// TestSetLevelCheckedRejectsInvalid 非法日志级别必须被拒绝且不改变当前级别。
func TestSetLevelCheckedRejectsInvalid(t *testing.T) {
	old := logger.Load()
	t.Cleanup(func() { logger.Store(old) })

	if err := SetLevelChecked("error"); err != nil {
		t.Fatalf("设置合法级别失败: %v", err)
	}
	if err := SetLevelChecked("bogus"); err == nil {
		t.Fatal("非法级别应返回 error")
	}
	if !strings.Contains(SetLevelChecked("bogus").Error(), "debug") {
		t.Errorf("错误信息应列出合法取值")
	}
	if get().Enabled(context.Background(), slog.LevelDebug) {
		t.Error("非法级别不应改变当前级别(仍应为 error)")
	}
	if !get().Enabled(context.Background(), slog.LevelError) {
		t.Error("当前级别应是 error")
	}
}

// TestSetLevelInvalidKeepsLevelAndWarns 兼容入口 SetLevel 遇非法值:
// 保持当前级别 + 留下 WARN 记录(不再静默回落 info)。
func TestSetLevelInvalidKeepsLevelAndWarns(t *testing.T) {
	oldLogger := logger.Load()
	t.Cleanup(func() { logger.Store(oldLogger) })

	var buf bytes.Buffer
	oldWarn := warnLogger
	warnLogger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	t.Cleanup(func() { warnLogger = oldWarn })

	if err := SetLevelChecked("warn"); err != nil {
		t.Fatalf("设置合法级别失败: %v", err)
	}
	SetLevel("bogus")

	if get().Enabled(context.Background(), slog.LevelInfo) {
		t.Error("非法级别不应把级别改成 info")
	}
	if !strings.Contains(buf.String(), "bogus") {
		t.Errorf("非法级别必须留下 WARN 记录: %s", buf.String())
	}
}
