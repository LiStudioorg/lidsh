// Provider 解析：把配置（provider 名 + 端点 + key + 模型）映射到 llm.Adapter。
package agent

import (
	"fmt"

	"lidsh/internal/llm"
)

// Resolver 按 provider 名返回适配器。
type Resolver interface {
	Adapter(provider string) (llm.Adapter, error)
}

// StaticResolver 是朴素的 provider→adapter 映射（M1a：静态注册表）。
// 配置驱动（settings/credentials 层）在后续里程碑接入闭包解析。
type StaticResolver struct {
	adapters map[string]llm.Adapter
}

// NewStaticResolver 构造静态解析器。
func NewStaticResolver(adapters map[string]llm.Adapter) *StaticResolver {
	return &StaticResolver{adapters: adapters}
}

// Adapter 按名取适配器。
func (r *StaticResolver) Adapter(provider string) (llm.Adapter, error) {
	a, ok := r.adapters[provider]
	if !ok {
		return nil, fmt.Errorf("no adapter for provider %q", provider)
	}
	return a, nil
}
