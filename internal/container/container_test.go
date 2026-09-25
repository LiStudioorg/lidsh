package container

import (
	"context"
	"testing"

	"lidsh/internal/config"

	"gopkg.in/yaml.v3"
)

func entries(t *testing.T, src string) []*config.Entry {
	t.Helper()
	var es []*config.Entry
	if err := yaml.Unmarshal([]byte(src), &es); err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		_ = e.Children()
	}
	return es
}

type llmService struct{ Model string }

func TestActivationIsDependencyDriven(t *testing.T) {
	// DSH：行序不承载加载语义，激活由服务可用性驱动。故意把 agent 写在 llm 前。
	reg := NewRegistry()
	reg.Register("lidsh-llm", Decl{Provides: []string{"llm"}}, func(a *App, id string, cfg map[string]any) (any, error) {
		model, _ := cfg["model"].(string)
		return &llmService{Model: model}, nil
	})
	reg.Register("lidsh-agent", Decl{Provides: []string{"agent"}, Inject: []string{"llm"}}, func(a *App, id string, cfg map[string]any) (any, error) {
		v, err := a.Require("llm")
		if err != nil {
			return nil, err
		}
		return map[string]any{"llm": v.(*llmService).Model}, nil
	})

	app := New(context.Background(), reg, t.TempDir(), ".", nil)
	err := app.Activate(entries(t, `
- id: agent
  name: lidsh-agent
- id: llm
  name: lidsh-llm
  config:
    model: deepseek-flash
`))
	if err != nil {
		t.Fatal(err)
	}
	order := app.IDs()
	if len(order) != 2 || order[0] != "llm" || order[1] != "agent" {
		t.Errorf("activation order = %v, want [llm agent]", order)
	}
	v, _ := app.Require("agent")
	if got := v.(map[string]any)["llm"]; got != "deepseek-flash" {
		t.Errorf("injected model = %v", got)
	}
}

func TestDisabledRowSkipped(t *testing.T) {
	reg := NewRegistry()
	reg.Register("m", Decl{Provides: []string{"m"}}, func(a *App, id string, cfg map[string]any) (any, error) {
		return id, nil
	})
	app := New(context.Background(), reg, t.TempDir(), ".", nil)
	if err := app.Activate(entries(t, "- id: m\n  name: m\n  disabled: true\n")); err != nil {
		t.Fatal(err)
	}
	if _, ok := app.Lookup("m"); ok {
		t.Error("disabled row must not activate")
	}
}

func TestMissingDependencyFails(t *testing.T) {
	reg := NewRegistry()
	reg.Register("needs", Decl{Provides: []string{"n"}, Inject: []string{"ghost"}},
		func(a *App, id string, cfg map[string]any) (any, error) { return id, nil })
	app := New(context.Background(), reg, t.TempDir(), ".", nil)
	err := app.Activate(entries(t, "- id: needs\n  name: needs\n"))
	if err == nil {
		t.Fatal("want unmet-dependency error")
	}
}

func TestGroupRowsFlatten(t *testing.T) {
	reg := NewRegistry()
	reg.Register("leaf", Decl{Provides: []string{"leaf"}},
		func(a *App, id string, cfg map[string]any) (any, error) { return id, nil })
	app := New(context.Background(), reg, t.TempDir(), ".", nil)
	if err := app.Activate(entries(t, `
- id: tools
  name: tool-group
  group: true
  config:
    - id: bash
      name: leaf
`)); err != nil {
		t.Fatal(err)
	}
	v, ok := app.Lookup("leaf")
	if !ok || v.(string) != "bash" {
		t.Errorf("group child not activated: %v %v", v, ok)
	}
}

func TestEventBusAndWaterfall(t *testing.T) {
	type ToolCall struct{ Name string }
	bus := NewEventBus()

	var seen []string
	On(bus, func(e ToolCall) { seen = append(seen, "simple:"+e.Name) })

	// waterfall：审批钩子改写后放行。
	Hook(bus, func(e ToolCall, next func(ToolCall) error) error {
		e.Name = "approved:" + e.Name
		return next(e)
	})
	// waterfall：拦截钩子不调 next 即短路。
	Hook(bus, func(e ToolCall, next func(ToolCall) error) error {
		if e.Name == "approved:deny-me" {
			return context.Canceled
		}
		return next(e)
	})

	Emit(bus, ToolCall{Name: "x"})
	if len(seen) != 1 {
		t.Errorf("simple emit = %v", seen)
	}

	var final string
	err := Dispatch(bus, ToolCall{Name: "bash"}, func(e ToolCall) error { final = e.Name; return nil })
	if err != nil || final != "approved:bash" {
		t.Errorf("dispatch = %q err=%v", final, err)
	}
	if err := Dispatch(bus, ToolCall{Name: "deny-me"}, func(ToolCall) error { return nil }); err == nil {
		t.Error("intercepting hook must be able to short-circuit")
	}
}
