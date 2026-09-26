package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mcheiyue/workbuddy-cpa/internal/wbexecutor"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type hostHTTPStreamResponse struct {
	StatusCode int
	Headers    http.Header
	StreamID   string
}

func (r *hostHTTPStreamResponse) UnmarshalJSON(data []byte) error {
	var wire struct {
		StatusCodeCamel int         `json:"StatusCode"`
		StatusCodeSnake int         `json:"status_code"`
		Headers         http.Header `json:"Headers"`
		HeadersLower    http.Header `json:"headers"`
		StreamIDCamel   string      `json:"StreamID"`
		StreamIDSnake   string      `json:"stream_id"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	r.StatusCode = wire.StatusCodeCamel
	if r.StatusCode == 0 {
		r.StatusCode = wire.StatusCodeSnake
	}
	r.Headers = wire.Headers
	if r.Headers == nil {
		r.Headers = wire.HeadersLower
	}
	r.StreamID = wire.StreamIDCamel
	if r.StreamID == "" {
		r.StreamID = wire.StreamIDSnake
	}
	return nil
}

type hostHTTPStreamChunk struct {
	Payload []byte
	Done    bool
	Error   string
}

func (c *hostHTTPStreamChunk) UnmarshalJSON(data []byte) error {
	var wire struct {
		PayloadCamel []byte `json:"Payload"`
		PayloadSnake []byte `json:"payload"`
		DoneCamel    bool   `json:"Done"`
		DoneSnake    bool   `json:"done"`
		ErrorCamel   string `json:"Error"`
		ErrorSnake   string `json:"error"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	c.Payload = wire.PayloadCamel
	if c.Payload == nil {
		c.Payload = wire.PayloadSnake
	}
	c.Done = wire.DoneCamel || wire.DoneSnake
	c.Error = wire.ErrorCamel
	if c.Error == "" {
		c.Error = wire.ErrorSnake
	}
	return nil
}

func hostHTTPDoStream(callbackID, method, url string, header http.Header, body []byte, call func(string, any) (json.RawMessage, error)) (hostHTTPStreamResponse, error) {
	raw, err := call(pluginabi.MethodHostHTTPDoStream, map[string]any{
		"host_callback_id": callbackID,
		"request": map[string]any{
			"Method":       method,
			"URL":          url,
			"Headers":      header,
			"Body":         body,
			"wire_profile": &pluginapi.HTTPWireProfile{HTTP1Only: true, DisableAutoCompression: true},
		},
	})
	if err != nil {
		return hostHTTPStreamResponse{}, fmt.Errorf("host http do_stream: %w", err)
	}
	var resp hostHTTPStreamResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return hostHTTPStreamResponse{}, fmt.Errorf("decode host http stream response: %w", err)
	}
	if strings.TrimSpace(resp.StreamID) == "" {
		return hostHTTPStreamResponse{}, fmt.Errorf("host http stream returned no stream_id")
	}
	return resp, nil
}

func readHostHTTPStream(callbackID, streamID string, call func(string, any) (json.RawMessage, error)) (hostHTTPStreamChunk, error) {
	raw, err := call(pluginabi.MethodHostHTTPStreamRead, map[string]any{
		"host_callback_id": callbackID,
		"stream_id":        streamID,
	})
	if err != nil {
		return hostHTTPStreamChunk{}, fmt.Errorf("host http stream read: %w", err)
	}
	var chunk hostHTTPStreamChunk
	if err := json.Unmarshal(raw, &chunk); err != nil {
		return hostHTTPStreamChunk{}, fmt.Errorf("decode host http stream chunk: %w", err)
	}
	return chunk, nil
}

func closeHostHTTPStream(callbackID, streamID string, call func(string, any) (json.RawMessage, error)) {
	_, _ = call(pluginabi.MethodHostHTTPStreamClose, map[string]any{
		"host_callback_id": callbackID,
		"stream_id":        streamID,
	})
}

func makeHostStreamDoer(callbackID string) wbexecutor.StreamDoer {
	return func(_ context.Context, method, url string, header http.Header, body []byte) (wbexecutor.StreamHandle, error) {
		resp, err := hostHTTPDoStream(callbackID, method, url, header, body, callHostJSON)
		if err != nil {
			return wbexecutor.StreamHandle{}, err
		}
		sid := resp.StreamID
		return wbexecutor.StreamHandle{
			StatusCode: resp.StatusCode,
			Headers:    resp.Headers,
			Read: func() ([]byte, bool, error) {
				chunk, rerr := readHostHTTPStream(callbackID, sid, callHostJSON)
				if rerr != nil {
					return nil, false, rerr
				}
				if chunk.Error != "" {
					return chunk.Payload, false, fmt.Errorf("upstream stream: %s", chunk.Error)
				}
				return chunk.Payload, chunk.Done, nil
			},
			Close: func() { closeHostHTTPStream(callbackID, sid, callHostJSON) },
		}, nil
	}
}
