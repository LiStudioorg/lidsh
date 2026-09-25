package container

import (
	"os"
	"reflect"
	"sync"
)

func stderr() *os.File { return os.Stderr }

// EventBus 是类型化事件总线，对应 Cordis 的 ctx.on/ctx.emit。
//
// Cordis 的事件名是字符串、载荷是元组；Go 里用「事件类型 = topic」更贴合类型系统：
// On[T] 订阅 T，Emit[T] 广播 T。waterfall 型事件（DSH 里请求带 next() 的形态）
// 用 Hook[T] 表达：处理器可改写载荷或短路。
type EventBus struct {
	mu        sync.RWMutex
	simple    map[reflect.Type][]any // func(T) 订阅者
	waterfall map[reflect.Type][]any // func(T, next func(T) error) error 钩子
}

func NewEventBus() *EventBus {
	return &EventBus{
		simple:    map[reflect.Type][]any{},
		waterfall: map[reflect.Type][]any{},
	}
}

// On 订阅事件类型 T。返回取消函数。
func On[T any](b *EventBus, handler func(T)) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	t := reflect.TypeOf((*T)(nil)).Elem()
	b.simple[t] = append(b.simple[t], handler)
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			list := b.simple[t]
			for i, h := range list {
				if reflect.ValueOf(h).Pointer() == reflect.ValueOf(handler).Pointer() {
					b.simple[t] = append(list[:i], list[i+1:]...)
					return
				}
			}
		})
	}
}

// Emit 广播一个事件，同步调用全部订阅者。
func Emit[T any](b *EventBus, event T) {
	b.mu.RLock()
	t := reflect.TypeOf((*T)(nil)).Elem()
	handlers := append([]any(nil), b.simple[t]...)
	b.mu.RUnlock()
	for _, h := range handlers {
		h.(func(T))(event)
	}
}

// Hook 注册一个 waterfall 处理器，签名 func(payload T, next func(T) error) error。
// 对应 Cordis 的 (request, next) => … 形态：处理器可以不调 next 即短路。
func Hook[T any](b *EventBus, handler func(T, func(T) error) error) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	t := reflect.TypeOf((*T)(nil)).Elem()
	b.waterfall[t] = append(b.waterfall[t], handler)
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			list := b.waterfall[t]
			for i, h := range list {
				if reflect.ValueOf(h).Pointer() == reflect.ValueOf(handler).Pointer() {
					b.waterfall[t] = append(list[:i], list[i+1:]...)
					return
				}
			}
		})
	}
}

// Dispatch 沿钩子链执行 waterfall，末端调用 final。
func Dispatch[T any](b *EventBus, payload T, final func(T) error) error {
	b.mu.RLock()
	t := reflect.TypeOf((*T)(nil)).Elem()
	hooks := append([]any(nil), b.waterfall[t]...)
	b.mu.RUnlock()

	var run func(i int, p T) error
	run = func(i int, p T) error {
		if i == len(hooks) {
			return final(p)
		}
		return hooks[i].(func(T, func(T) error) error)(p, func(next T) error { return run(i+1, next) })
	}
	return run(0, payload)
}
