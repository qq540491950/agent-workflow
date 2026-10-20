package logx

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

// maskPlaceholder 是敏感值被替换后的占位符。
const maskPlaceholder = "***"

// sensitivePattern 匹配"值形态"的敏感键值对(key=value / key: value /
// "key": "value"),值可带可选的 Bearer 前缀。
//
// 注意:这条规则是"宁可误伤也不漏"的最后一道防线 —— 日志/diff 文本里
// 出现 token=xxx 就整体替换,即使它其实是普通代码(误伤评估见
// keySensitivity 的说明)。
var sensitivePattern = regexp.MustCompile(
	`(?i)((?:api[_-]?key|token|password|passwd|secret|authorization|credential)s?"?\s*[:=]\s*"?(?:bearer\s+)?)([^",\s}&]+)`)

// sensitiveHint 是敏感词的公共子串。正则很贵,而事件总线的热路径上
// 每个 payload 都要过一遍(Emit 是每节点每次执行都调用的),所以先用
// 廉价的 strings.Contains 预筛:绝大多数正常文本(node 名、summary、
// 状态名)根本不含这些子串,可直接跳过正则。
var sensitiveHint = []string{"key", "token", "password", "passwd", "secret", "authorization", "credential"}

// maybeSensitive 是 mayContainSensitive 的别名(语义更直白)。
func mayContainSensitive(s string) bool {
	for _, h := range sensitiveHint {
		if strings.Contains(strings.ToLower(s), h) {
			return true
		}
	}
	return false
}

// Mask 脱敏文本中的敏感键值对(含 Bearer 前缀形式)。
func Mask(s string) string {
	if !mayContainSensitive(s) {
		return s
	}
	return sensitivePattern.ReplaceAllString(s, `${1}***`)
}

// ---------------------------------------------------------------------------
// 键名判定规则
//
// 判定分三层,全部大小写不敏感、分隔符无关:
//
//   - 强敏感(sensStrong):整值替换为 "***",与值的类型无关。
//   - 弱敏感(sensWeak):只有字符串值才替换(数字/布尔不是凭据形态,
//     这样 max_tokens / token_count 这类用量统计不会被抹掉)。
//   - 非敏感(sensNone):只看值的形态(key=value 形态由 Mask 处理)。
//
// 强敏感命中条件:
//  1. 强敏感词(整词,复数同义):password / passwd / secret / credential(s) /
//     authorization —— 如 password / db_secret / client_credentials。
//  2. 强敏感词组(相邻两词):api_key / client_key / access_key / private_key /
//     encryption_key / signing_key / api_secret / auth_key —— 如
//     ANTHROPIC_API_KEY / x-api-key / aws_access_key_id 的 access_key 部分。
//  3. 无分隔紧凑写法:apikey / accesskey / secretkey / authtoken /
//     accesstoken / refreshtoken / idtoken / sessiontoken / bearertoken。
//  4. 键名以 token / tokens 结尾:token / user_token / auth_token /
//     github_token —— token 在末尾几乎总是凭据。
//
// 弱敏感:token 出现在键名中间(token_count / prompt_token 之类)。
//
// 明确不敏感的写法(防误伤):
//   - max_tokens / total_tokens / token_count 等 token 计数键(白名单)。
//   - key / keys / keyword / tokenizer 等不含凭据语义的词。
//   - 单词 key 单独出现时不敏感(太泛:"key":"value" 多半是普通配置)。
// ---------------------------------------------------------------------------

// sensitivity 表示键名的敏感强度。
type sensitivity int

const (
	sensNone   sensitivity = iota // 非敏感
	sensWeak                      // 弱敏感:仅字符串值需要替换
	sensStrong                    // 强敏感:整值替换(与值类型无关)
)

// strongSensitiveWords 是强敏感词(整词匹配,复数同义)。
var strongSensitiveWords = map[string]bool{
	"password":      true,
	"passwd":        true,
	"secret":        true,
	"credential":    true,
	"credentials":   true,
	"authorization": true,
}

// strongSensitivePhrases 是强敏感词组(相邻两个词命中即判定)。
var strongSensitivePhrases = map[string]bool{
	"api_key":        true,
	"client_key":     true,
	"access_key":     true,
	"private_key":    true,
	"encryption_key": true,
	"signing_key":    true,
	"api_secret":     true,
	"auth_key":       true,
}

// strongSensitiveCompact 是完全没有分隔符的紧凑写法。
var strongSensitiveCompact = map[string]bool{
	"apikey":        true,
	"accesskey":     true,
	"privatekey":    true,
	"secretkey":     true,
	"encryptionkey": true,
	"signingkey":    true,
	"clientsecret":  true,
	"authtoken":     true,
	"accesstoken":   true,
	"refreshtoken":  true,
	"idtoken":       true,
	"sessiontoken":  true,
	"bearertoken":   true,
}

// tokenCountKeys 是 token 计数类良性键:值是用量统计数字,不是凭据。
// 命中即判定为非敏感(避免把 max_tokens 这类配置读数抹掉)。
var tokenCountKeys = map[string]bool{
	"max_tokens": true, "min_tokens": true, "total_tokens": true,
	"input_tokens": true, "output_tokens": true, "prompt_tokens": true,
	"completion_tokens": true, "cached_tokens": true, "reasoning_tokens": true,
	"token_count": true, "token_counts": true, "tokens_used": true,
	"num_tokens": true, "token_usage": true, "usage_tokens": true,
}

// keySensitivity 判定键名是否敏感。
func keySensitivity(key string) sensitivity {
	if key == "" || !mayContainSensitive(key) {
		return sensNone
	}
	words, compact, norm := splitKey(key)
	if tokenCountKeys[norm] {
		return sensNone
	}
	if strongSensitiveCompact[compact] {
		return sensStrong
	}
	for _, w := range words {
		if strongSensitiveWords[w] || strongSensitiveWords[singular(w)] {
			return sensStrong
		}
	}
	for i := 0; i+1 < len(words); i++ {
		if strongSensitivePhrases[words[i]+"_"+words[i+1]] {
			return sensStrong
		}
	}
	for i, w := range words {
		if w != "token" && w != "tokens" {
			continue
		}
		if i == len(words)-1 {
			return sensStrong // token 在末尾:几乎总是凭据
		}
		return sensWeak // token 在中间:按值类型决定
	}
	return sensNone
}

// splitKey 归一化键名:非字母数字视为分隔符,驼峰边界也切分
// (apiKey → api_key)。返回词序列、无分隔紧凑形式、分隔符归一形式。
func splitKey(key string) (words []string, compact string, norm string) {
	var b strings.Builder
	runes := []rune(key)
	for i, r := range runes {
		switch {
		case r >= 'A' && r <= 'Z':
			if i > 0 {
				prev := runes[i-1]
				if !(prev >= 'A' && prev <= 'Z') {
					b.WriteRune('_') // 驼峰边界
				}
			}
			b.WriteRune(r - 'A' + 'a')
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	norm = b.String()
	words = strings.FieldsFunc(norm, func(r rune) bool { return r == '_' })
	compact = strings.ReplaceAll(norm, "_", "")
	return words, compact, norm
}

// singular 去掉英文复数尾缀(长度 > 3 才去,避免把 "access" 变成 "acces")。
func singular(w string) string {
	if len(w) > 3 && strings.HasSuffix(w, "s") {
		return w[:len(w)-1]
	}
	return w
}

// ---------------------------------------------------------------------------
// 结构化脱敏
// ---------------------------------------------------------------------------

// MaskValue 递归脱敏任意结构(字符串/嵌套 map/slice/结构体/指针)里的敏感值。
// 事件流的三个出口(SQLite 落库 / SSE 推送 / Wails IPC)都经这里:
// 节点失败的 error 文本常带 CLI stderr,而 stderr 里常有
// "ANTHROPIC_API_KEY=sk-..." 这类凭据 —— 日志侧此前已脱敏(见 maskArgs),
// 但事件/审计流是旁路,同一份敏感值照样落库并推给前端。
//
// 两条线索都认:键名是敏感词 → 整值替换为 ***;值本身含 key=value 形态
// → 就地替换(保持上下文可读)。非字符串值原样返回。
//
// 覆盖面:凡是能装字符串的容器都遍历 —— map[string]any / map[string]string /
// map[string][]string / map[string]map[string]string / []string / []any /
// [][]string / 结构体(按 json tag 取字段名)/ 指针 / 切片套切片。
// 容器类型保持不变(不转成 map[string]any),避免破坏调用方的类型断言。
func MaskValue(v any) any { return maskRecursive("", v) }

// maskRecursive 带键名线索的脱敏入口。
func maskRecursive(key string, v any) any {
	if key != "" {
		switch keySensitivity(key) {
		case sensStrong:
			if !emptyString(v) {
				return maskPlaceholder
			}
		case sensWeak:
			if _, ok := v.(string); ok && !emptyString(v) {
				return maskPlaceholder
			}
		}
	}
	return maskValueAny(v)
}

// maskWithKey 带键名上下文脱敏(键名不敏感时按值形态处理)。
func maskWithKey(key string, v any) any {
	switch keySensitivity(key) {
	case sensStrong:
		if !emptyString(v) {
			return maskPlaceholder
		}
	case sensWeak:
		if _, ok := v.(string); ok && !emptyString(v) {
			return maskPlaceholder
		}
	}
	return maskValueAny(v)
}

// emptyString 判断值是否为空字符串。空值不含任何凭据,脱敏成 "***"
// 只是噪音(例如未配置的可选凭据字段),故保持原样。
func emptyString(v any) bool {
	s, ok := v.(string)
	return ok && s == ""
}

// maskValueAny 对"没有键名线索"的值做脱敏。
func maskValueAny(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		return Mask(t)
	case error:
		if t == nil {
			return nil
		}
		// fmt.Sprint 会捕获 Error() 内部的 panic,不会把事件总线带崩。
		return Mask(fmt.Sprint(t))
	case fmt.Stringer:
		return Mask(fmt.Sprint(t))
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = maskWithKey(k, val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = maskValueAny(val)
		}
		return out
	}
	// 其余容器类型(map[string]string、[]string、结构体、指针、切片套切片…)
	rv := reflect.ValueOf(v)
	out, changed := maskReflect(rv, sensNone)
	if !changed {
		return v
	}
	return out.Interface()
}

// maskReflect 用反射处理"非常规"容器类型,保持容器类型不变。
//
// sens 是键名线索;changed=false 表示没有任何内容需要脱敏,调用方应保留原值。
func maskReflect(v reflect.Value, sens sensitivity) (reflect.Value, bool) {
	switch v.Kind() {
	case reflect.Interface:
		return maskReflectInterface(v, sens)
	case reflect.Pointer:
		if v.IsNil() {
			return v, false
		}
		out, changed := maskReflect(v.Elem(), sens)
		if !changed {
			return v, false
		}
		res := reflect.New(v.Type().Elem())
		res.Elem().Set(out)
		return res, true
	case reflect.String:
		if v.Len() == 0 {
			return v, false // 空值不含凭据
		}
		if sens != sensNone {
			return reflect.ValueOf(maskPlaceholder).Convert(v.Type()), true
		}
		masked := Mask(v.String())
		if masked == v.String() {
			return v, false
		}
		return reflect.ValueOf(masked).Convert(v.Type()), true
	case reflect.Map:
		return maskReflectMap(v, sens)
	case reflect.Slice:
		return maskReflectSlice(v, sens)
	case reflect.Array:
		return maskReflectArray(v, sens)
	case reflect.Struct:
		return maskReflectStruct(v, sens)
	default:
		// 数字/布尔/函数等:装不下字符串,也不是凭据形态。
		return v, false
	}
}

// maskReflectInterface 处理接口值。只有空接口(any)能承载 "***" 占位符,
// error 这类非空接口装不下字符串 —— 此时保留原值。
func maskReflectInterface(v reflect.Value, sens sensitivity) (reflect.Value, bool) {
	if v.IsNil() {
		return v, false
	}
	empty := v.Type().NumMethod() == 0
	inner := v.Elem()
	if inner.Kind() == reflect.String && inner.Len() == 0 {
		return v, false // 空字符串不含凭据,保持原样
	}
	if sens == sensStrong {
		if empty {
			return reflect.ValueOf(maskPlaceholder), true
		}
		sens = sensNone
	}
	if sens == sensWeak {
		if inner.Kind() == reflect.String {
			if empty {
				return reflect.ValueOf(maskPlaceholder), true
			}
			return v, false
		}
		sens = sensNone
	}
	out, changed := maskReflect(inner, sens)
	if !changed || !out.Type().AssignableTo(v.Type()) {
		return v, false
	}
	res := reflect.New(v.Type()).Elem()
	res.Set(out)
	return res, true
}

// maskReflectMap 遍历 map:键名为字符串时按键名判定敏感度。
func maskReflectMap(v reflect.Value, sens sensitivity) (reflect.Value, bool) {
	if v.IsNil() {
		return v, false
	}
	res := reflect.MakeMapWithSize(v.Type(), v.Len())
	changed := false
	iter := v.MapRange()
	for iter.Next() {
		k := iter.Key()
		elem := iter.Value()
		s := sens
		if s == sensNone && k.Kind() == reflect.String {
			s = keySensitivity(k.String())
		}
		out, ch := maskReflect(elem, s)
		if ch {
			changed = true
		}
		if !out.IsValid() || !out.Type().AssignableTo(v.Type().Elem()) {
			out = elem
		}
		res.SetMapIndex(k, out)
	}
	return res, changed
}

// maskReflectSlice 遍历切片(不传键名线索:切片元素没有键)。
func maskReflectSlice(v reflect.Value, sens sensitivity) (reflect.Value, bool) {
	if v.IsNil() {
		return v, false
	}
	// []byte 等字节切片:整体按文本处理(里面可能藏着 "API_KEY=sk-...")。
	if v.Type().Elem().Kind() == reflect.Uint8 {
		return maskByteSlice(v)
	}
	res := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
	changed := false
	for i := 0; i < v.Len(); i++ {
		out, ch := maskReflect(v.Index(i), sens)
		if ch {
			changed = true
		}
		if !out.IsValid() || !out.Type().AssignableTo(v.Type().Elem()) {
			out = v.Index(i)
		}
		res.Index(i).Set(out)
	}
	return res, changed
}

// maskByteSlice 把字节切片当文本脱敏(元素类型保持不变)。
func maskByteSlice(v reflect.Value) (reflect.Value, bool) {
	raw := v.Convert(reflect.TypeOf([]byte{})).Bytes()
	masked := Mask(string(raw))
	if masked == string(raw) {
		return v, false
	}
	out := []byte(masked)
	res := reflect.MakeSlice(v.Type(), len(out), len(out))
	for i, b := range out {
		res.Index(i).SetUint(uint64(b))
	}
	return res, true
}

// maskReflectArray 遍历数组。
func maskReflectArray(v reflect.Value, sens sensitivity) (reflect.Value, bool) {
	res := reflect.New(v.Type()).Elem()
	changed := false
	for i := 0; i < v.Len(); i++ {
		out, ch := maskReflect(v.Index(i), sens)
		if ch {
			changed = true
		}
		if !out.IsValid() || !out.Type().AssignableTo(v.Type().Elem()) {
			out = v.Index(i)
		}
		res.Index(i).Set(out)
	}
	return res, changed
}

// maskReflectStruct 遍历结构体:字段名取 json tag(与实际序列化一致),
// 未导出字段原样保留(整体拷贝后再逐字段替换)。
func maskReflectStruct(v reflect.Value, sens sensitivity) (reflect.Value, bool) {
	t := v.Type()
	res := reflect.New(t).Elem()
	res.Set(v) // 先整体拷贝(含未导出字段),再替换敏感字段
	changed := false
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		s := sens
		if s == sensNone {
			if name := jsonFieldName(f); name != "" {
				s = keySensitivity(name)
			}
		}
		out, ch := maskReflect(v.Field(i), s)
		if !ch || !out.IsValid() || !out.Type().AssignableTo(f.Type) {
			continue
		}
		changed = true
		res.Field(i).Set(out)
	}
	return res, changed
}

// jsonFieldName 取字段的 JSON 名(json tag 优先,否则用字段名)。
func jsonFieldName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return ""
	}
	name := strings.Split(tag, ",")[0]
	if name == "" {
		return f.Name
	}
	return name
}
