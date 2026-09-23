package actions

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestRegistryWriteGate(t *testing.T) {
	r := NewRegistry(false)
	r.Register(Action{Name: "demo.read", Handler: func(context.Context, json.RawMessage) (any, error) { return 1, nil }})
	r.Register(Action{Name: "demo.write", Mutating: true, Handler: func(context.Context, json.RawMessage) (any, error) { return 2, nil }})

	if _, err := r.Lookup("demo.read"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Lookup("demo.write"); !errors.Is(err, ErrWriteDisabled) {
		t.Fatalf("write action err = %v", err)
	}
	if _, err := r.Lookup("nope"); !errors.Is(err, ErrUnknownAction) {
		t.Fatalf("unknown err = %v", err)
	}
	for _, info := range r.List() {
		if info.Name == "demo.write" && info.Enabled {
			t.Error("write action must be listed as disabled")
		}
	}
}

func TestRegisterRejectsBadNames(t *testing.T) {
	for _, name := range []string{"", "Bad", "a..b", "a/b", "rm -rf"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Register(%q) should panic", name)
				}
			}()
			NewRegistry(false).Register(Action{Name: name, Handler: func(context.Context, json.RawMessage) (any, error) { return nil, nil }})
		}()
	}
}

func TestDecodeParamsStrict(t *testing.T) {
	var v struct {
		ID string `json:"id"`
	}
	if err := DecodeParams(json.RawMessage(`{"id":"x"}`), &v); err != nil || v.ID != "x" {
		t.Fatalf("decode = %v %+v", err, v)
	}
	if err := DecodeParams(json.RawMessage(`{"id":"x","extra":1}`), &v); err == nil {
		t.Fatal("unknown fields must be rejected")
	}
	if err := DecodeParams(nil, &v); err != nil {
		t.Fatal("empty params must be accepted")
	}
}
