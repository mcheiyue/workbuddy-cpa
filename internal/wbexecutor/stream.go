package wbexecutor

import (
	"bytes"
	"context"
	"io"
	"net/http"
)

// buildUpstreamRequest 构造上游请求（URL + 出站头）；body 由调用方携带。
func buildUpstreamRequest(ctx context.Context, req ExecuteRequest, body []byte) (*http.Request, *ExecError) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, req.ChatBaseURL+chatCompletionsPath, bytes.NewReader(body))
	if err != nil {
		return nil, &ExecError{Kind: ErrClient, Status: http.StatusBadRequest, Msg: "create request failed"}
	}
	BuildChatHeaders(httpReq, req.Cred, req.ConversationID, "", "")
	return httpReq, nil
}

// streamReader 把 StreamHandle 逐段读取适配为 io.Reader（sticky EOF/error）。
type streamReader struct {
	handle StreamHandle
	buf    []byte
	err    error
}

func (s *streamReader) Read(p []byte) (int, error) {
	for len(s.buf) == 0 {
		if s.err != nil {
			return 0, s.err
		}
		payload, done, err := s.handle.Read()
		if len(payload) > 0 {
			s.buf = payload
		}
		switch {
		case err != nil:
			s.err = err
		case done:
			s.err = io.EOF
		}
	}
	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	return n, nil
}

// drainStream 读完上游流（状态码错误体聚合），上限 1MB。
func drainStream(h StreamHandle) []byte {
	var out []byte
	const limit = 1 << 20
	for len(out) < limit {
		payload, done, err := h.Read()
		out = append(out, payload...)
		if err != nil || done {
			return out
		}
	}
	return out
}
