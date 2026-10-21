package agent

import (
	"os"
	"sort"
	"strings"

	"agentworkflow/logx"
)

// 凭据脱敏:适配器把凭据注入子进程环境后,CLI 常把环境变量原样打进
// stderr("ANTHROPIC_AUTH_TOKEN=sk-..."),而进程失败时错误消息会把
// stderr 拼进去 —— 于是凭据进了执行记录、事件流与前端。
//
// 适配器是**唯一知道凭据原值**的地方(上层只拿到掩码后的形态),
// 所以在这里按"确实注入过的值"精确脱敏,而不是按正则猜形态 ——
// 形态猜测既会漏(裸 token、"API key sk-xxx"这类形态匹配不到),
// 又会误伤正常文本。

// sensitiveEnvSegments 是环境变量名里出现这些**词段**时,该变量的值
// 视为凭据。键名按 "_"/"-" 切段后整段比较,避免 MONKEY 这类
// "碰巧含 key"的名字被误判。
var sensitiveEnvSegments = map[string]bool{
	"key": true, "keys": true, "apikey": true, "privatekey": true, "sessionkey": true,
	"token": true, "tokens": true, "accesstoken": true, "refreshtoken": true,
	"secret": true, "secrets": true,
	"password": true, "passwd": true, "pwd": true,
	"credential": true, "credentials": true,
	"auth": true, "authorization": true,
}

// minSecretLen 是参与脱敏的值的最短长度:过短的值("1"、"true")
// 在 stderr 里到处都有,脱敏它们会把正常文本搅成一片 "***"。
const minSecretLen = 6

// Redactor 按已注入的凭据值做精确脱敏。零值可用(不做任何替换)。
type Redactor struct {
	values []string
}

// ParentEnv 返回当前进程的环境变量表(键 → 值)。
//
// 传入 NewRedactor 可以把**父进程**环境里的凭据也纳入脱敏:
// 子进程继承父环境,CLI 把继承来的凭据打进 stderr 是常事。
func ParentEnv() map[string]string {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	return env
}

// NewRedactor 从若干张环境变量表里收集凭据值。
// 同一张表里重复的值只留一份;长值优先替换(短值若是长值的子串,
// 先替换长值才不会漏)。
//
// 传入 ParentEnv() 可以把**父进程**环境里的凭据也纳入脱敏:
// 子进程继承父环境,CLI 把继承来的凭据打进 stderr 是常事。
func NewRedactor(envs ...map[string]string) *Redactor {
	r := &Redactor{}
	seen := map[string]bool{}
	for _, env := range envs {
		for k, v := range env {
			if v == "" || len(v) < minSecretLen || seen[v] {
				continue
			}
			if !IsSensitiveEnvName(k) {
				continue
			}
			seen[v] = true
			r.values = append(r.values, v)
		}
	}
	sort.Slice(r.values, func(i, j int) bool { return len(r.values[i]) > len(r.values[j]) })
	return r
}

// Text 把 text 里出现过的凭据值替换成 ***。
func (r *Redactor) Text(text string) string {
	if r == nil || text == "" || len(r.values) == 0 {
		return text
	}
	for _, v := range r.values {
		text = strings.ReplaceAll(text, v, MaskedValue)
	}
	return text
}

// IsSensitiveEnvName 判断环境变量名是否指向凭据。
// 键名按 "_"/"-" 切段,任一段命中敏感词即认为是凭据名。
func IsSensitiveEnvName(name string) bool {
	norm := strings.ToLower(name)
	for _, seg := range strings.FieldsFunc(norm, func(r rune) bool {
		return r == '_' || r == '-'
	}) {
		if sensitiveEnvSegments[seg] {
			return true
		}
	}
	return false
}

// RedactResponse 对响应里的**错误文本**做凭据脱敏。
//
// 只动 Error:Output 是 Agent 的正文,就地掩码会破坏内容
// (节点明细的 API 出口另有 logx.Mask 兜底)。
func (r *Redactor) RedactResponse(resp *AgentResponse) *AgentResponse {
	if resp == nil || resp.Error == "" {
		return resp
	}
	resp.Error = r.Text(resp.Error)
	return resp
}

// WarnCredentialArgs 检查 CLI 参数里是否含**凭据形态**的值,命中则告警。
//
// ---- 为什么需要它(P1-3)----
//
// 子进程的 argv 对**本机所有进程**可见:ps aux、/proc/<pid>/cmdline、
// 各类进程监控/审计工具都能读到。Agent 配置里的 extra_args 原样进 argv ——
// 于是 "--api-key sk-xxx" 这种凭据等于半公开(日志与 API 出口的脱敏管不到它)。
//
// 正确做法:凭据放 Agent 的**环境变量**(env,只在子进程里,别的进程读不到
// 别人的 environ 除非同 uid 且刻意去读),extra_args 只放非敏感参数。
//
// ---- 判定口径(唯一来源)----
//
// "值呈凭据形态"这条规则全仓只有一份:logx/credential.go。
// logx.MaskCredentialValues(v) != v ⇔ v 呈凭据形态(只认值形态,不看键名)。
// 这里只取它的**判断**,不取它的替换结果。
//
// ---- 为什么不改写 argv(关键)----
//
// 绝不把 argv 里的值脱敏后再下发:掩码哨兵 "***" 不是真凭据,下发过去
// CLI 鉴权必然失败(约定 5:哨兵只表示"这里曾经被掩过")。
// 所以这里**只告警**,由用户自己把凭据挪到环境变量。
//
// 只检查 Agent 配置带来的参数(extra_args),不检查 prompt/task 正文:
// 那部分是用户的任务内容,不是"配置里的凭据",混进来会刷屏。
func WarnCredentialArgs(agentID string, args []string) {
	var indexes []int
	for i, a := range args {
		if logx.MaskCredentialValues(a) != a {
			indexes = append(indexes, i)
		}
	}
	if len(indexes) == 0 {
		return
	}
	// 只报位置,不报值:告警本身不能成为新的泄漏面。
	logx.Warn("CLI 参数里含凭据形态的值:命令行参数对本机所有进程可见"+
		"(ps aux、/proc/<pid>/cmdline),凭据应改放在 Agent 的环境变量(env)里,"+
		"extra_args 只放非敏感参数",
		"agent", agentID, "arg_count", len(indexes), "arg_indexes", indexes)
}
