package claude

import (
	"fmt"
	"os"
	"testing"

	coreagent "agentworkflow/agent"
	"agentworkflow/agent/internal/stubcli"
)

// TestMain 退出前删掉同形替身的临时目录(替身是编译产物,不该留在
// 磁盘上)。
func TestMain(m *testing.M) {
	code := m.Run()
	stubcli.Cleanup()
	os.Exit(code)
}

// errUnexpected 把"响应形态不对"变成一个可读的错误。
func errUnexpected(resp *coreagent.AgentResponse) error {
	return fmt.Errorf("status=%q decision=%q output=%q error=%q",
		resp.Status, resp.Decision, resp.Output, resp.Error)
}
