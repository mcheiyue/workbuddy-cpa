package main

import (
	"encoding/json"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

// okEnvelope 将 result 序列化后包装为成功 Envelope。
func okEnvelope(result any) ([]byte, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return json.Marshal(pluginabi.Envelope{OK: true, Result: raw})
}

// errorEnvelope 返回包含 code/message 的失败 Envelope。
func errorEnvelope(code, message string) []byte {
	return errorEnvelopeStatus(code, message, 0)
}

// errorEnvelopeStatus 返回带 HTTP 状态码的失败 Envelope。
func errorEnvelopeStatus(code, message string, status int) []byte {
	raw, err := json.Marshal(pluginabi.Envelope{
		OK:    false,
		Error: &pluginabi.Error{Code: code, Message: message, HTTPStatus: status},
	})
	if err != nil {
		return []byte(`{"ok":false,"error":{"code":"marshal_error","message":"failed to encode error envelope"}}`)
	}
	return raw
}

// unwrapHostEnvelope 解包宿主 RPC Envelope（cabi.go 的 C ABI 路径调用）。
// OK=false 时透传宿主真实错误串（如 "host callback ID is not open"），
// 而不是吞成固定文案，保证调用方能读到根因。
// body 非 Envelope 形状时原样返回，不报错。
func unwrapHostEnvelope(body []byte) (json.RawMessage, error) {
	var envelope pluginabi.Envelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return json.RawMessage(body), nil
	}
	if !envelope.OK {
		if envelope.Error != nil && envelope.Error.Message != "" {
			return nil, simpleErr(envelope.Error.Message)
		}
		return nil, simpleErr("host callback failed")
	}
	return envelope.Result, nil
}
