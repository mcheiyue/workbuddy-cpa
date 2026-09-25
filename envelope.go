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
