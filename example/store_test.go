package example_test

import (
	"encoding/json"
	"testing"

	"github.com/quantumcycle/go-ignore-cov/example"
)

func TestOrderStatusString(t *testing.T) {
	cases := []struct {
		status example.OrderStatus
		want   string
	}{
		{example.Pending, "pending"},
		{example.Processing, "processing"},
		{example.Shipped, "shipped"},
		{example.Delivered, "delivered"},
		{example.Cancelled, "cancelled"},
	}
	for _, c := range cases {
		if got := c.status.String(); got != c.want {
			t.Errorf("status %d: got %q, want %q", c.status, got, c.want)
		}
	}
}

func TestOrderEncode(t *testing.T) {
	o := example.Order{ID: "ord-1", Status: example.Shipped, Total: 42.0}
	b := o.Encode()
	var decoded example.Order
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ID != o.ID {
		t.Errorf("got ID %q, want %q", decoded.ID, o.ID)
	}
}

func TestStore(t *testing.T) {
	s := example.NewStore()
	o := &example.Order{ID: "ord-1", Status: example.Pending, Total: 9.99}
	s.Add(o)

	got, ok := s.Get("ord-1")
	if !ok {
		t.Fatal("expected order to exist")
	}
	if got.ID != o.ID {
		t.Errorf("got ID %q, want %q", got.ID, o.ID)
	}

	_, ok = s.Get("missing")
	if ok {
		t.Error("expected missing order to not exist")
	}
}
