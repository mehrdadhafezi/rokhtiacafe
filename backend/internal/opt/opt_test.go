package opt

import (
	"encoding/json"
	"testing"
)

func TestFieldDistinguishesAbsentNullAndValue(t *testing.T) {
	var v struct {
		A Field[int] `json:"a"`
		B Field[int] `json:"b"`
		C Field[int] `json:"c"`
	}
	if err := json.Unmarshal([]byte(`{"b": null, "c": 7}`), &v); err != nil {
		t.Fatal(err)
	}
	if v.A.Set || !v.B.Set || !v.B.Null || !v.C.Set || v.C.Null || v.C.Value != 7 {
		t.Errorf("got %+v", v)
	}
	if v.A.Ptr() != nil || v.B.Ptr() != nil || *v.C.Ptr() != 7 {
		t.Error("Ptr wrong")
	}
	if err := json.Unmarshal([]byte(`{"c": "x"}`), &v); err == nil {
		t.Error("type mismatch must fail")
	}
}
