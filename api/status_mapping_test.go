package api

// 容量类错误码 → HTTP 状态的契约(spec"错误码"一节):
//
//	WORKSPACE_BUSY / SYSTEM_BUSY → 409(请求合法,现在不能跑)
//	QUEUE_FULL                   → 429(队列也满了)
//
// 此前这三个码未进 statusForError,按 KindStateError 落默认 400 ——
// 客户端无法区分"参数错"(不该重试)与"容量不足"(应该重试/排队)。
// 全链路触发需要长跑执行占位,时序脆弱;映射本身就是要钉的契约,
// 这里对映射函数逐码断言(api 包内可见)。
import (
	"net/http"
	"testing"

	"agentworkflow/workflow/model"
)

func TestStatusForError_CapacityCodes(t *testing.T) {
	cases := []struct {
		code string
		want int
	}{
		{"WORKSPACE_BUSY", http.StatusConflict},
		{"SYSTEM_BUSY", http.StatusConflict},
		{"QUEUE_FULL", http.StatusTooManyRequests},
		{"WORKFLOW_BUSY", http.StatusConflict},
	}
	for _, c := range cases {
		se := model.NewError(model.KindStateError, c.code, "容量类错误")
		if got := statusForError(se); got != c.want {
			t.Errorf("statusForError(%s) = %d, want %d", c.code, got, c.want)
		}
	}
}
