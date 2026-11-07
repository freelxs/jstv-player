package catvod

import (
	"fmt"
	"sync/atomic"
)

const maxProtocolResponse = 32 << 20

var diagnosticSequence atomic.Uint64

// ProtocolError keeps diagnostic content out of RPC error messages. The
// application supplies LogFunc to record Details through its redaction layer.
type ProtocolError struct {
	Kind    string
	Status  int
	ID      uint64
	Details string
	Cause   error
}

func (e *ProtocolError) Error() string {
	message := "站点请求失败，请稍后重试或换站"
	switch e.Kind {
	case "size":
		message = "站点响应超过大小限制"
	case "json":
		message = "站点返回的数据格式无效"
	case "network":
		message = "无法连接站点，请检查网络后重试"
	}
	if e.Status != 0 {
		message += fmt.Sprintf("（HTTP %d）", e.Status)
	}
	return fmt.Sprintf("%s [诊断 %d]", message, e.ID)
}

func (e *ProtocolError) Unwrap() error { return e.Cause }

func (c *Client) protocolError(kind string, status int, details string, cause error) error {
	e := &ProtocolError{Kind: kind, Status: status, ID: diagnosticSequence.Add(1), Details: details, Cause: cause}
	if c.LogFunc != nil {
		c.LogFunc(fmt.Sprintf("诊断 %d %s", e.ID, details))
	}
	return e
}
