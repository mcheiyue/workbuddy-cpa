package wbmodels

import (
	"fmt"
	"strings"
	"sync"
)

// ModelResolver 是消费方（executor）定义的接口契约：公开 ID → 内部上游 key。
type ModelResolver interface {
	ResolveModel(authID, publicModelID string) (string, error)
}

// modelRecord 存储单条映射记录：公开 ID → 内部 ID。
type modelRecord struct {
	internalID string
}

// Registry per-AuthID 的模型反向映射注册表，RWMutex 并发安全。
// 快照语义：每次 ForAuth 调用替换整个 authID 桶（原子写入），读侧无锁快照。
type Registry struct {
	mu     sync.RWMutex
	byAuth map[string]map[string]modelRecord
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{byAuth: make(map[string]map[string]modelRecord)}
}

// Store 存储一个 authID 的完整映射快照（原子替换）。
func (r *Registry) Store(authID string, mapping map[string]string) {
	r.StoreWithMeta(authID, mapping, nil)
}

// StoreWithMeta 存储映射快照（本阶段 meta 固定为 nil，预留扩展点）。
func (r *Registry) StoreWithMeta(authID string, mapping map[string]string, _ any) {
	if r == nil {
		return
	}
	authID = strings.TrimSpace(authID)
	if authID == "" {
		return
	}
	snapshot := make(map[string]modelRecord, len(mapping))
	for publicID, internalID := range mapping {
		publicID = strings.TrimSpace(publicID)
		internalID = strings.TrimSpace(internalID)
		if publicID == "" || internalID == "" {
			continue
		}
		snapshot[publicID] = modelRecord{internalID: internalID}
	}
	r.mu.Lock()
	r.byAuth[authID] = snapshot
	r.mu.Unlock()
}

// ResolveModel 实现 ModelResolver 接口：按 authID 查公开 ID → 内部 key。
// 未知公开 ID 返回结构化错误。
func (r *Registry) ResolveModel(authID, publicModelID string) (string, error) {
	internalID := r.resolve(authID, publicModelID)
	if internalID == "" {
		return "", &ModelNotFoundError{PublicID: publicModelID, AuthID: authID}
	}
	return internalID, nil
}

// Resolve 返回内部 key（空串 = 未找到）。
func (r *Registry) Resolve(authID, publicID string) string {
	return r.resolve(authID, publicID)
}

func (r *Registry) resolve(authID, publicID string) string {
	if r == nil {
		return ""
	}
	authID = strings.TrimSpace(authID)
	publicID = strings.TrimSpace(publicID)
	if authID == "" || publicID == "" {
		return ""
	}
	r.mu.RLock()
	mapping := r.byAuth[authID]
	record := mapping[publicID]
	if record.internalID == "" {
		// 兼容：尝试带/不带 provider 前缀
		if strings.HasPrefix(strings.ToLower(publicID), provider+"/") {
			record = mapping[publicID[len(provider)+1:]]
		} else {
			record = mapping[provider+"/"+publicID]
		}
	}
	r.mu.RUnlock()
	return record.internalID
}

// ModelNotFoundError 未知公开 ID 时返回的结构化错误。
type ModelNotFoundError struct {
	PublicID string
	AuthID   string
}

func (e *ModelNotFoundError) Error() string {
	return fmt.Sprintf("model_not_found: public_id=%q auth_id=%q", e.PublicID, e.AuthID)
}

// Code 返回错误码，供 CPA 框架分类。
func (e *ModelNotFoundError) Code() string { return "model_not_found" }

// ModelStatic 返回空模型列表（静态目录由上游驱动，无静态基底）。
// 供后续 model.static dispatch 接线。
func ModelStatic() []ModelInfo { return nil }
