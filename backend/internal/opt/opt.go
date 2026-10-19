// Package opt distinguishes "absent", "null" and "value" in JSON PATCH bodies.
package opt

import (
	"bytes"
	"encoding/json"
)

// Field is a JSON field that may be omitted (Set=false), explicitly null (Set && Null),
// or carry a value.
type Field[T any] struct {
	Set   bool
	Null  bool
	Value T
}

// UnmarshalJSON is only called when the key is present.
func (f *Field[T]) UnmarshalJSON(b []byte) error {
	f.Set = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		f.Null = true
		return nil
	}
	return json.Unmarshal(b, &f.Value)
}

// AllowsNull marks Field as accepting an explicit JSON null (see httpx.DecodeJSON).
func (Field[T]) AllowsNull() bool { return true }

// Of returns a set, non-null field.
func Of[T any](v T) Field[T] { return Field[T]{Set: true, Value: v} }

// Null returns a set, null field.
func Null[T any]() Field[T] { return Field[T]{Set: true, Null: true} }

// Ptr returns nil when absent or null, else a pointer to the value.
func (f Field[T]) Ptr() *T {
	if !f.Set || f.Null {
		return nil
	}
	v := f.Value
	return &v
}
