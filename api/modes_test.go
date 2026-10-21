package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"agentworkflow/app/application"
	"agentworkflow/workflow/model"
)

// GET /api/modes 必须返回 model.AllModes() 的**逐项一致**结果。
//
// 为什么要这个端点:前端此前自己硬编码了一份 mode 清单 —— 后端加了
// mode 而前端没跟上,用户在下拉里选不到新 mode;前端留着后端已删的
// mode,用户选了必然被校验期判非法(约定 13:同一份语义只有一处定义)。
//
// 这条用例同时是**漂移守卫**:端点返回的清单与 model.AllModes() 必须
// 逐项相同 —— 端点里另写一份字面量清单,这里立刻变红。
func TestModesEndpointMatchesModel(t *testing.T) {
	s := newTestServer(t)

	code, _, raw := doReq(t, s, http.MethodGet, "/api/modes", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/modes 状态码 = %d,期望 200", code)
	}
	var got []string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("响应不是字符串数组: %v (%s)", err, raw)
	}
	want := model.AllModes()
	if len(got) != len(want) {
		t.Fatalf("mode 清单长度 = %d,期望 %d(%v vs %v)", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("mode[%d] = %q,期望 %q(顺序也要一致)", i, got[i], want[i])
		}
	}
	// 端点必须真的返回了内容(防空转:返回空数组也算"一致"的一种假象)。
	if len(got) == 0 {
		t.Error("mode 清单不得为空")
	}
}

// TestModesEndpointAgreesWithDesktopService 服务器模式(REST)与桌面模式
// (Wails 绑定背后的 WorkflowService.Modes)必须给出**同一份**清单。
//
// 约定 10:同一个语义在两种模式下不能有两套数据。桌面模式的下拉走绑定,
// 服务器模式的下拉走 REST —— 两者不一致时,用户在两种模式里看到的
// 可选 mode 不一样。
func TestModesEndpointAgreesWithDesktopService(t *testing.T) {
	a, err := application.NewApp(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	t.Cleanup(func() { _ = a.Repo.Close() })
	s := NewServer(a)

	code, _, raw := doReq(t, s, http.MethodGet, "/api/modes", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/modes 状态码 = %d,期望 200", code)
	}
	var rest []string
	if err := json.Unmarshal(raw, &rest); err != nil {
		t.Fatalf("响应不是字符串数组: %v (%s)", err, raw)
	}
	desktop := a.Workflows.Modes()
	if len(rest) != len(desktop) {
		t.Fatalf("服务器模式 %v 与桌面模式 %v 长度不一致", rest, desktop)
	}
	for i := range desktop {
		if rest[i] != desktop[i] {
			t.Errorf("mode[%d]: 服务器模式 %q ≠ 桌面模式 %q", i, rest[i], desktop[i])
		}
	}
}

// TestFrontendFallbackModesMatchModel 前端的**兜底** mode 清单
// (frontend/src/pages/Designer.tsx 的 FALLBACK_MODES)必须与
// model.AllModes() 逐项一致。
//
// 兜底清单只在"后端还没返回 / 返回异常"时用,但它**出现在用户面前**
// (下拉里):不一致时用户会看到一个与后端不同的清单,选了后端不认的
// mode → 保存时被校验期判非法,而界面给不出任何解释(约定 13:
// 同一份语义只有一处定义)。
//
// 这是**跨语言**的契约:前端清单在 TS 文件里,后端清单在 Go 里,
// 只有把两边放在一起比才能发现漂移。
func TestFrontendFallbackModesMatchModel(t *testing.T) {
	path := filepath.Join("..", "frontend", "src", "pages", "Designer.tsx")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", path, err)
	}
	src := string(raw)
	start := regexp.MustCompile(`FALLBACK_MODES\s*=\s*\[`).FindStringIndex(src)
	if start == nil {
		t.Fatalf("%s 里找不到 FALLBACK_MODES 声明(前端兜底清单被改名/删除?)", path)
	}
	end := regexp.MustCompile(`\]`).FindStringIndex(src[start[1]:])
	if end == nil {
		t.Fatalf("%s 里 FALLBACK_MODES 声明没有闭合", path)
	}
	body := src[start[1] : start[1]+end[0]]
	items := regexp.MustCompile(`"([^"]*)"`).FindAllStringSubmatch(body, -1)
	got := make([]string, 0, len(items))
	for _, it := range items {
		got = append(got, it[1])
	}
	want := model.AllModes()
	if len(got) != len(want) {
		t.Fatalf("前端兜底清单 %v 与后端 model.AllModes() %v 不一致", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("mode[%d]: 前端兜底 %q ≠ 后端 %q", i, got[i], want[i])
		}
	}
}
